// Command tarnhelm runs a command with $$KEY placeholders resolved from a
// dotenv file, and redacts every dotenv value from the command's output.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/samyooole/tarnhelm/internal/dotenv"
	"github.com/samyooole/tarnhelm/internal/expand"
	"github.com/samyooole/tarnhelm/internal/redact"
	"github.com/samyooole/tarnhelm/internal/runner"
)

// Exit codes for tarnhelm's own failures (conventions of env(1)/docker run).
const (
	exitUsage         = 2
	exitInternal      = 125
	exitNotExecutable = 126
	exitNotFound      = 127
)

const defaultMaxOutput = 16 << 20

const usage = `Usage: tarnhelm run [--env-file PATH] [--max-output BYTES] -- COMMAND [ARG...]

Run COMMAND with $$KEY placeholders in its arguments replaced by values from a
dotenv file. Every non-empty dotenv value is redacted from stdout and stderr.

Quote arguments containing placeholders so the shell leaves $$ alone:
    tarnhelm run -- curl -H 'Authorization: Bearer $$API_KEY' https://example.com

Use $$$$KEY to pass the literal text $$KEY.

Options:
    --env-file PATH     dotenv file to read (default: .env)
    --max-output BYTES  combined stdout+stderr limit (default: 16777216)
    --help              show this help

Other commands:
    tarnhelm --version  print the version
`

// version is set at build time with -ldflags "-X main.version=..."; when
// empty, the module version recorded by "go install ...@vX.Y.Z" is used.
var version string

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "tarnhelm %s\n", versionString())
		return 0
	case "run":
	default:
		fmt.Fprintf(stderr, "tarnhelm: unknown command %q\n\n%s", args[0], usage)
		return exitUsage
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	envFile := fs.String("env-file", ".env", "")
	maxOutput := fs.Int64("max-output", defaultMaxOutput, "")
	help := fs.Bool("help", false, "")

	// Flags must precede "--"; locate it ourselves so it is mandatory.
	rest := args[1:]
	sep := -1
	for i, a := range rest {
		if a == "--" {
			sep = i
			break
		}
	}
	flagArgs := rest
	if sep >= 0 {
		flagArgs = rest[:sep]
	}
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		fmt.Fprintf(stderr, "tarnhelm: %v\n\n%s", err, usage)
		return exitUsage
	}
	if *help {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if sep < 0 {
		fmt.Fprintf(stderr, "tarnhelm: missing \"--\" before the command\n\n%s", usage)
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tarnhelm: unexpected argument before \"--\"\n\n%s", usage)
		return exitUsage
	}
	command := rest[sep+1:]
	if len(command) == 0 {
		fmt.Fprintf(stderr, "tarnhelm: no command supplied\n\n%s", usage)
		return exitUsage
	}
	if *maxOutput <= 0 {
		fmt.Fprintf(stderr, "tarnhelm: --max-output must be positive\n")
		return exitUsage
	}

	vars, err := dotenv.Load(*envFile)
	if err != nil {
		fmt.Fprintf(stderr, "tarnhelm: %v\n", err)
		return exitInternal
	}
	r := redact.New(dotenv.SecretValues(vars))
	fail := func(code int, format string, a ...any) int {
		fmt.Fprint(stderr, r.String(fmt.Sprintf("tarnhelm: "+format+"\n", a...)))
		return code
	}

	argv, err := expand.Args(command, vars)
	if err != nil {
		return fail(exitInternal, "%v; command not run", err)
	}

	res, err := runner.Run(runner.Config{
		Argv:        argv,
		MaxOutput:   *maxOutput,
		GracePeriod: 5 * time.Second,
	})
	// Error messages name the command as the user wrote it (unexpanded).
	name := command[0]
	switch {
	case errors.Is(err, runner.ErrNotFound):
		return fail(exitNotFound, "%s: executable not found", name)
	case errors.Is(err, runner.ErrNotExecutable):
		return fail(exitNotExecutable, "%s: cannot execute", name)
	case errors.Is(err, runner.ErrOutputLimit):
		return fail(exitInternal, "output limit of %d bytes exceeded; command terminated and output discarded", *maxOutput)
	case err != nil:
		return fail(exitInternal, "%s: failed to run command", name)
	}

	_, _ = stdout.Write(r.Bytes(res.Stdout))
	_, _ = stderr.Write(r.Bytes(res.Stderr))
	if res.Signal != nil {
		fail(0, "interrupted by %v; command terminated", res.Signal)
	}
	return res.ExitCode
}
