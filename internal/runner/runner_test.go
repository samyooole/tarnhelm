package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func run(t *testing.T, argv ...string) (Result, error) {
	t.Helper()
	return Run(Config{Argv: argv, MaxOutput: 1 << 20, GracePeriod: time.Second})
}

func TestSuccessAndArgs(t *testing.T) {
	res, err := run(t, "sh", "-c", `printf '%s|' "$@"; echo err >&2`, "_", "a b", "", "c")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "a b||c|" {
		t.Errorf("stdout %q", res.Stdout)
	}
	if string(res.Stderr) != "err\n" {
		t.Errorf("stderr %q", res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit %d", res.ExitCode)
	}
}

func TestNoShellInterpretation(t *testing.T) {
	res, err := run(t, "echo", "hello", "|", "cat", "*", "$(id)")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "hello | cat * $(id)\n" {
		t.Errorf("stdout %q", res.Stdout)
	}
}

func TestExitCode(t *testing.T) {
	res, err := run(t, "sh", "-c", "exit 42")
	if err != nil || res.ExitCode != 42 {
		t.Fatalf("exit %d err %v", res.ExitCode, err)
	}
	res, err = run(t, "sh", "-c", "kill -TERM $$")
	if err != nil || res.ExitCode != 128+15 {
		t.Fatalf("signalled exit %d err %v", res.ExitCode, err)
	}
}

func TestNotFound(t *testing.T) {
	_, err := run(t, "tarnhelm-no-such-binary-xyz")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	_, err = run(t, "./no/such/path")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound for path, got %v", err)
	}
}

func TestNotExecutable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "noexec")
	os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600)
	_, err := run(t, p)
	if !errors.Is(err, ErrNotExecutable) {
		t.Errorf("want ErrNotExecutable, got %v", err)
	}
}

func TestErrorsDoNotContainArgs(t *testing.T) {
	_, err := run(t, "/no/such/sk-secret-in-path", "sk-secret-arg")
	if err == nil || strings.Contains(err.Error(), "sk-secret") {
		t.Errorf("bad error %v", err)
	}
}

func TestWorkingDirectory(t *testing.T) {
	wd, _ := os.Getwd()
	res, err := run(t, "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(res.Stdout)) != wd {
		t.Errorf("pwd %q want %q", res.Stdout, wd)
	}
}

func TestOutputLimit(t *testing.T) {
	res, err := Run(Config{
		Argv:        []string{"sh", "-c", "head -c 5000 /dev/zero; echo leaked-secret >&2; sleep 30"},
		MaxOutput:   1000,
		GracePeriod: time.Second,
	})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("want ErrOutputLimit, got %v", err)
	}
	if len(res.Stdout) != 0 || len(res.Stderr) != 0 {
		t.Error("captured output must be discarded")
	}
}

func TestOutputLimitCombined(t *testing.T) {
	_, err := Run(Config{
		Argv:        []string{"sh", "-c", "head -c 600 /dev/zero; head -c 600 /dev/zero >&2"},
		MaxOutput:   1000,
		GracePeriod: time.Second,
	})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("limit must apply to stdout+stderr combined, got %v", err)
	}
}

func TestOutputExactlyAtLimit(t *testing.T) {
	res, err := Run(Config{Argv: []string{"head", "-c", "1000", "/dev/zero"}, MaxOutput: 1000, GracePeriod: time.Second})
	if err != nil || len(res.Stdout) != 1000 {
		t.Fatalf("err %v len %d", err, len(res.Stdout))
	}
}

func TestBackgroundGrandchildDoesNotHang(t *testing.T) {
	start := time.Now()
	res, err := run(t, "sh", "-c", "sleep 30 & echo done")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != "done\n" {
		t.Errorf("stdout %q", res.Stdout)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("waited for background grandchild")
	}
}

// TestInterruptKillsGroup sends SIGINT to the test process itself; Run has
// signal.Notify installed so the test process is not terminated.
func TestInterruptKillsGroup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
	}{
		{"cooperative", `sleep 600 & echo $$ $! > "$1"; wait`},
		// Ignores SIGINT; must be SIGKILLed after the grace period.
		{"stubborn", `trap '' INT; sleep 600 & echo $$ $! > "$1"; while :; do wait; done`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidfile := filepath.Join(t.TempDir(), "pids")
			type out struct {
				res Result
				err error
			}
			ch := make(chan out, 1)
			go func() {
				r, e := Run(Config{
					Argv:        []string{"sh", "-c", tc.script, "_", pidfile},
					MaxOutput:   1 << 20,
					GracePeriod: 500 * time.Millisecond,
				})
				ch <- out{r, e}
			}()
			pids := waitForPids(t, pidfile)
			syscall.Kill(os.Getpid(), syscall.SIGINT)

			var o out
			select {
			case o = <-ch:
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not return after interrupt")
			}
			if o.err != nil {
				t.Fatal(o.err)
			}
			if o.res.Signal != syscall.SIGINT || o.res.ExitCode != 130 {
				t.Errorf("signal %v exit %d", o.res.Signal, o.res.ExitCode)
			}
			for _, pid := range pids {
				waitDead(t, pid)
			}
		})
	}
}

func waitForPids(t *testing.T, path string) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if f := strings.Fields(string(b)); err == nil && len(f) == 2 {
			var pids []int
			for _, s := range f {
				p, _ := strconv.Atoi(s)
				pids = append(pids, p)
			}
			return pids
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("child did not write pid file")
	return nil
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		// A reparented zombie awaiting reaping also counts as dead.
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
			if f := strings.Fields(string(b)); len(f) > 2 && f[2] == "Z" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("process %d still alive", pid)
}
