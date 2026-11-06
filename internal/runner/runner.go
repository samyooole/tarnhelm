// Package runner executes a child process in its own process group, captures
// its output in bounded memory, and handles interruption.
package runner

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Errors returned by Run. They deliberately carry no argument text, because
// arguments may contain expanded secrets.
var (
	ErrNotFound      = errors.New("executable not found")
	ErrNotExecutable = errors.New("executable cannot be run")
	ErrStart         = errors.New("failed to start command")
	ErrOutputLimit   = errors.New("output limit exceeded")
)

// Config controls a single execution.
type Config struct {
	Argv        []string      // fully expanded argument vector; never logged
	MaxOutput   int64         // combined stdout+stderr byte limit
	GracePeriod time.Duration // time between forwarded signal and SIGKILL
}

// Result describes a completed execution. Stdout and Stderr are raw and must
// be redacted by the caller before being written anywhere.
type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
	Signal         os.Signal // signal that interrupted tarnhelm, if any
}

// limitBuf is a shared byte budget for stdout and stderr.
type limitBuf struct {
	mu       sync.Mutex
	used     int64
	max      int64
	exceeded bool
	onExceed func()
}

type capture struct {
	lb  *limitBuf
	buf []byte
}

func (c *capture) Write(p []byte) (int, error) {
	c.lb.mu.Lock()
	defer c.lb.mu.Unlock()
	if c.lb.exceeded {
		return 0, ErrOutputLimit
	}
	if c.lb.used+int64(len(p)) > c.lb.max {
		c.lb.exceeded = true
		c.lb.onExceed()
		return 0, ErrOutputLimit
	}
	c.lb.used += int64(len(p))
	c.buf = append(c.buf, p...)
	return len(p), nil
}

// Run executes cfg.Argv. A non-nil error means the command could not be run
// or its output was discarded; Result is then only meaningful for ExitCode.
func Run(cfg Config) (Result, error) {
	cmd := exec.Command(cfg.Argv[0], cfg.Argv[1:]...)
	if cmd.Err != nil {
		return Result{ExitCode: 127}, classifyStartErr(cmd.Err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = childStdin()
	// If a background grandchild keeps the pipes open after the child exits,
	// stop waiting for it after this long.
	cmd.WaitDelay = 2 * time.Second

	// pgid stays 0 until Start returns. Output may arrive before then, so
	// killGroup must never call kill(-0, ...), which would signal tarnhelm's
	// own process group.
	var pgid atomic.Int64
	killGroup := func(sig syscall.Signal) {
		if p := int(pgid.Load()); p > 0 {
			_ = syscall.Kill(-p, sig)
		}
	}

	lb := &limitBuf{max: cfg.MaxOutput, onExceed: func() { killGroup(syscall.SIGKILL) }}
	stdout, stderr := &capture{lb: lb}, &capture{lb: lb}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return Result{ExitCode: 127}, classifyStartErr(err)
	}
	pgid.Store(int64(cmd.Process.Pid))
	lb.mu.Lock()
	if lb.exceeded { // limit hit before pgid was known
		killGroup(syscall.SIGKILL)
	}
	lb.mu.Unlock()

	done := make(chan struct{})
	var (
		gotSig os.Signal
		sigMu  sync.Mutex
	)
	go func() {
		var timer <-chan time.Time
		for {
			select {
			case <-done:
				return
			case s := <-sigs:
				sigMu.Lock()
				first := gotSig == nil
				if first {
					gotSig = s
				}
				sigMu.Unlock()
				if first {
					killGroup(s.(syscall.Signal))
					timer = time.After(cfg.GracePeriod)
				} else {
					killGroup(syscall.SIGKILL) // second signal: no more waiting
				}
			case <-timer:
				killGroup(syscall.SIGKILL)
			}
		}
	}()

	waitErr := cmd.Wait()
	close(done)

	sigMu.Lock()
	res := Result{Signal: gotSig}
	sigMu.Unlock()
	if res.Signal != nil {
		// Make sure nothing in the group survives an interrupt.
		killGroup(syscall.SIGKILL)
	}

	lb.mu.Lock()
	exceeded := lb.exceeded
	lb.mu.Unlock()
	if exceeded {
		return Result{ExitCode: 1}, ErrOutputLimit
	}

	res.Stdout, res.Stderr = stdout.buf, stderr.buf
	res.ExitCode = exitCode(cmd.ProcessState)
	if res.Signal != nil {
		res.ExitCode = 128 + int(res.Signal.(syscall.Signal))
	}
	// ErrWaitDelay (pipes held open by a grandchild) is not a failure of the
	// command itself; any other wait error with no ProcessState is.
	if waitErr != nil && cmd.ProcessState == nil {
		return res, ErrStart
	}
	return res, nil
}

func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return 1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

func classifyStartErr(err error) error {
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrPermission), errors.Is(err, syscall.ENOEXEC):
		return ErrNotExecutable
	default:
		return ErrStart
	}
}

// childStdin passes through piped or redirected stdin. A terminal is not
// passed: the child runs in its own process group, so reading from the
// terminal would stop it with SIGTTIN, and tarnhelm is not interactive.
func childStdin() *os.File {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil // /dev/null
	}
	return os.Stdin
}
