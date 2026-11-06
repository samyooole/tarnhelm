package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Synthetic credentials only.
const (
	secretA = "glpat-synthetic-AAAA1111"
	secretB = "sk-synthetic-BBBB2222"
)

var tarnhelmBin, fakeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tarnhelm-test")
	if err != nil {
		panic(err)
	}
	tarnhelmBin = filepath.Join(dir, "tarnhelm")
	fakeBin = filepath.Join(dir, "fakecli")
	for bin, pkg := range map[string]string{tarnhelmBin: ".", fakeBin: "./testdata/fakecli"} {
		if out, err := exec.Command("go", "build", "-o", bin, pkg).CombinedOutput(); err != nil {
			panic(string(out))
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	code           int
}

// tarnhelm runs the built binary in a fresh directory containing envContent
// as .env (if non-nil).
func tarnhelm(t *testing.T, envContent *string, args ...string) result {
	t.Helper()
	dir := t.TempDir()
	if envContent != nil {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(*envContent), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(tarnhelmBin, args...)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{so.String(), se.String(), code}
}

func env(s string) *string { return &s }

var stdEnv = env("A=" + secretA + "\nB='" + secretB + "'\nEMPTY=\nPLAIN_WORD=hello\n")

func assertNoSecrets(t *testing.T, r result) {
	t.Helper()
	for _, s := range []string{secretA, secretB} {
		if strings.Contains(r.stdout, s) || strings.Contains(r.stderr, s) {
			t.Errorf("secret leaked:\nstdout=%q\nstderr=%q", r.stdout, r.stderr)
		}
	}
}

// The integration test required by the spec.
func TestSecretRoundTrip(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", fakeBin, "verify", secretA, "$$A")
	if r.code != 0 {
		t.Fatalf("exit %d (fakecli did not receive expected plaintext?) stderr=%q", r.code, r.stderr)
	}
	assertNoSecrets(t, r)
	if r.stdout != "stdout got: [REDACTED]\n" {
		t.Errorf("stdout %q", r.stdout)
	}
	if r.stderr != "stderr got: [REDACTED]\n" {
		t.Errorf("stderr %q", r.stderr)
	}
}

func TestEmbeddedAndMultiple(t *testing.T) {
	want := "u:" + secretA + "@h/" + secretB + "/" + secretA
	r := tarnhelm(t, stdEnv, "run", "--", fakeBin, "verify", want, "u:$$A@h/$$B/$$A")
	if r.code != 0 {
		t.Fatalf("exit %d stderr=%q", r.code, r.stderr)
	}
	assertNoSecrets(t, r)
	if r.stdout != "stdout got: u:[REDACTED]@h/[REDACTED]/[REDACTED]\n" {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestArgumentWithSpaces(t *testing.T) {
	r := tarnhelm(t, env("K='two words'\n"), "run", "--", fakeBin, "verify", "x two words y", "x $$K y")
	if r.code != 0 {
		t.Fatalf("exit %d stderr=%q", r.code, r.stderr)
	}
}

func TestUnreferencedSecretsRedacted(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", "echo", "leak "+secretB+" and hello")
	if r.stdout != "leak [REDACTED] and [REDACTED]\n" {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestOrdinaryCommand(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", "echo", "plain world", "$NOT_TOUCHED", "|", "cat")
	if r.code != 0 || r.stdout != "plain world $NOT_TOUCHED | cat\n" || r.stderr != "" {
		t.Errorf("%+v", r)
	}
}

func TestEscape(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", "echo", "$$$$A")
	if r.stdout != "$$A\n" {
		t.Errorf("stdout %q", r.stdout)
	}
}

func TestNoEnvInjection(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", fakeBin, "env")
	if r.code != 0 {
		t.Fatal(r.stderr)
	}
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.HasPrefix(line, "A=") || strings.HasPrefix(line, "B=") || strings.HasPrefix(line, "PLAIN_WORD=") {
			t.Errorf("dotenv var injected into child env: %q", line)
		}
	}
	if strings.Contains(r.stdout, "[REDACTED]") {
		t.Error("a secret value reached the child environment")
	}
}

func TestInheritsParentEnv(t *testing.T) {
	t.Setenv("TARNHELM_TEST_PARENT", "inherited")
	r := tarnhelm(t, stdEnv, "run", "--", fakeBin, "env")
	if !strings.Contains(r.stdout, "TARNHELM_TEST_PARENT=inherited") {
		t.Error("parent environment not inherited")
	}
}

func TestEnvFileFlag(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "custom.env")
	os.WriteFile(p, []byte("X="+secretA+"\n"), 0o600)
	r := tarnhelm(t, nil, "run", "--env-file", p, "--", fakeBin, "verify", secretA, "$$X")
	if r.code != 0 {
		t.Fatalf("exit %d stderr=%q", r.code, r.stderr)
	}
	assertNoSecrets(t, r)
}

func TestExitCodePropagated(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", fakeBin, "exit", "7")
	if r.code != 7 || r.stdout != "exiting\n" {
		t.Errorf("%+v", r)
	}
}

func TestNonZeroExitStillRedacts(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", "sh", "-c", `echo "$1"; echo "$1" >&2; exit 4`, "_", "$$A")
	if r.code != 4 || r.stdout != "[REDACTED]\n" || r.stderr != "[REDACTED]\n" {
		t.Errorf("%+v", r)
	}
}

func TestFailures(t *testing.T) {
	cases := []struct {
		name     string
		env      *string
		args     []string
		code     int
		inStderr string
	}{
		{"no args", stdEnv, nil, exitUsage, "Usage"},
		{"unknown subcommand", stdEnv, []string{"load"}, exitUsage, "unknown command"},
		{"missing separator", stdEnv, []string{"run", "echo", "hi"}, exitUsage, `missing "--"`},
		{"no command", stdEnv, []string{"run", "--"}, exitUsage, "no command"},
		{"bad flag", stdEnv, []string{"run", "--nope", "--", "true"}, exitUsage, "flag provided but not defined"},
		{"missing env file", nil, []string{"run", "--", "echo", "hi"}, exitInternal, "env file not found"},
		{"malformed env", env("BAD=\"" + secretA + "\n"), []string{"run", "--", "echo", "hi"}, exitInternal, "invalid dotenv syntax"},
		{"missing key", stdEnv, []string{"run", "--", "echo", "$$NOPE"}, exitInternal, `"NOPE" is not defined`},
		{"empty key", stdEnv, []string{"run", "--", "echo", "$$EMPTY"}, exitInternal, `"EMPTY" is empty`},
		{"empty env file + ref", env(""), []string{"run", "--", "echo", "$$A"}, exitInternal, "not defined"},
		{"not found", stdEnv, []string{"run", "--", "no-such-binary-" + secretA}, exitNotFound, "executable not found"},
		{"not found via ref", stdEnv, []string{"run", "--", "/x/$$A"}, exitNotFound, "/x/$$A: executable not found"},
		{"output limit", stdEnv, []string{"run", "--max-output", "1000", "--", "sh", "-c", `echo "$1"; head -c 5000 /dev/zero`, "_", "$$A"}, exitInternal, "output limit of 1000 bytes exceeded"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := tarnhelm(t, c.env, c.args...)
			if r.code != c.code {
				t.Errorf("exit %d want %d", r.code, c.code)
			}
			if !strings.Contains(r.stderr, c.inStderr) {
				t.Errorf("stderr %q missing %q", r.stderr, c.inStderr)
			}
			if c.name == "output limit" && r.stdout != "" {
				t.Errorf("raw output not discarded: %q", r.stdout)
			}
			assertNoSecrets(t, r)
		})
	}
}

func TestEmptyEnvFileWithoutRefs(t *testing.T) {
	r := tarnhelm(t, env(""), "run", "--", "echo", "ok")
	if r.code != 0 || r.stdout != "ok\n" {
		t.Errorf("%+v", r)
	}
}

func TestDefaultOutputLimit(t *testing.T) {
	r := tarnhelm(t, stdEnv, "run", "--", fakeBin, "flood", "16777217")
	if r.code != exitInternal || r.stdout != "" || !strings.Contains(r.stderr, "16777216 bytes exceeded") {
		t.Errorf("exit %d stdout len %d stderr %q", r.code, len(r.stdout), r.stderr)
	}
	r = tarnhelm(t, stdEnv, "run", "--", fakeBin, "flood", "16777216")
	if r.code != 0 || len(r.stdout) != 16777216 {
		t.Errorf("exactly 16 MiB should pass: exit %d len %d", r.code, len(r.stdout))
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"run", "--help"}} {
		r := tarnhelm(t, nil, args...)
		if r.code != 0 || !strings.Contains(r.stdout, "Usage: tarnhelm run") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestInterruptBinary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte(*stdEnv), 0o600)
	pidfile := filepath.Join(dir, "pid")
	cmd := exec.Command(tarnhelmBin, "run", "--", "sh", "-c",
		`echo "partial $1"; sleep 600 & echo $! > "$2"; wait`, "_", "$$A", pidfile)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	for i := 0; i < 250 && pid == 0; i++ {
		b, _ := os.ReadFile(pidfile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("no pid")
	}
	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		cmd.Process.Kill()
		t.Fatal("tarnhelm did not exit after SIGINT")
	}
	if cmd.ProcessState.ExitCode() != 130 {
		t.Errorf("exit %d", cmd.ProcessState.ExitCode())
	}
	if so.String() != "partial [REDACTED]\n" || !strings.Contains(se.String(), "interrupted") {
		t.Errorf("stdout %q stderr %q", so.String(), se.String())
	}
	time.Sleep(200 * time.Millisecond)
	if syscall.Kill(pid, 0) == nil {
		if b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); !strings.Contains(string(b), ") Z ") {
			t.Errorf("grandchild %d survived", pid)
		}
	}
}
