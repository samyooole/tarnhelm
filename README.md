# tarnhelm

is a secret-aware command runner for coding agents, allowing them to execute commands requiring credentials without routinely exposing those credentials in their context.

The problem:
```bash
export SECRET_KEY=sk_2_serum

echo "$SECRET_KEY" # oh no! bye bye secret key~
```

What `tarnhelm` does instead:

```bash
# .env (never read by the agent):
#   SECRET_KEY=sk_2_serum

echo '$$SECRET_KEY'                          # $$SECRET_KEY — the ordinary shell is untouched
tarnhelm run -- echo '$$SECRET_KEY'          # [REDACTED]
tarnhelm run -- privileged-op '$$SECRET_KEY' # privileged-op receives sk_2_serum
```

## Principles
1. Secret never appears in expanded command shown to agent; resolve placeholders internally and never echo expanded arguments
2. Secret never appears verbatim in normal stdout/stderr; hence capture streams and redact before forwarding
3. Agent doesn't need to load .env into its context; hence this Go binary reads `.env` directly
4. Existing CLI tools continue working within `tarnhelm`; hence need the ability to launch arbitrary executables with substituted arguments
5. No server or persistent process; hence a single Go executable
6. Works with .env files; hence use standard dotenv parsing
7. No special agent integration required; hence agent can invoke the CLI through normal terminal tool

## Threat model

**Tarnhelm is designed to prevent _accidental_ secret exposure, not deliberate exfiltration by a malicious coding agent.**

It stops secrets ending up in an agent's context through the usual routes: `cat .env`, `export`ed variables echoed back, verbose CLI output, error messages that quote a token. An agent that *wants* the secret can still get it (see [Limitations](#limitations)).

## Install

Requires Go 1.22+. Linux AMD64 is the supported target.

```bash
go build -o tarnhelm ./cmd/tarnhelm
install -m 0755 tarnhelm ~/.local/bin/   # or any directory on your PATH
```

Make sure that directory is on your `PATH`, e.g. add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile.

## Usage

```text
tarnhelm run [--env-file PATH] [--max-output BYTES] -- COMMAND [ARG...]
```

| Flag | Behaviour |
|---|---|
| `--env-file PATH` | dotenv file to read, relative to the current directory. Default: `./.env` (parent directories are not searched) |
| `--max-output BYTES` | Combined stdout + stderr limit. Default: 16 MiB |
| `--help` | Show usage |

The `--` separator is required.

```bash
tarnhelm run --env-file .env -- \
  curl -H 'PRIVATE-TOKEN: $$GITLAB_TOKEN' https://gitlab.example.com/api/v4/projects

tarnhelm run -- psql 'postgres://app:$$DB_PASSWORD@localhost:5432/app' -c 'select 1'
```

### Placeholders

- `$$NAME`, where `NAME` matches `[A-Za-z_][A-Za-z0-9_]*`, is replaced by the value of `NAME` from the dotenv file. Names are case-sensitive.
- A placeholder may appear anywhere inside an argument, any number of times. Argument boundaries are preserved: a value with spaces stays one argument.
- `$$$$NAME` is an escape and produces the literal text `$$NAME`.
- `$$` not followed by a valid name (e.g. `$$`, `$$1`, `$$$X`) is left as is.
- `$NAME` (single dollar) is never interpreted.
- Substituted values are not scanned again (no recursive expansion).
- If any referenced key is missing or has an empty value, tarnhelm exits with an error that names the key, and the command is **not** run.

**Always single-quote arguments containing placeholders.** Unquoted or double-quoted, Bash expands `$$` to its process ID before tarnhelm ever sees it.

### Execution

- The command is executed directly, **not through a shell**. Pipes, redirections, globs and `$(...)` are passed as literal arguments: `tarnhelm run -- echo a '|' cat` prints `a | cat`. If you need a shell, invoke one explicitly and pass secrets as positional parameters:
  ```bash
  tarnhelm run -- sh -c 'curl -sH "Authorization: Bearer $1" https://api.example.com | jq .' _ '$$API_KEY'
  ```
- The child inherits the working directory and the ordinary environment. dotenv values are **not** added to the child's environment; only explicitly referenced values are passed, as arguments.
- Piped/redirected stdin is passed through; an interactive terminal is not (stdin is `/dev/null`).
- On `Ctrl-C` / `SIGTERM` / `SIGHUP` the signal is forwarded to the child's process group; after a 5 second grace period (or a second signal) the group is killed. Output captured so far is redacted and printed.
- Processes the command leaves running in the background after a normal exit are not killed.

### Output and redaction

stdout and stderr are captured in memory, and once the command has exited, **every non-empty value in the dotenv file**, referenced or not, is replaced by `[REDACTED]`. Then stdout is written, followed by stderr.

Consequences to be aware of:

- Every value in the dotenv file is treated as a secret. `DEBUG=true` means every `true` in the output becomes `[REDACTED]`. Keep non-secret configuration out of the file you point tarnhelm at.
- Output is not streamed: nothing appears until the command finishes, and relative ordering between stdout and stderr lines is lost.
- If combined output exceeds `--max-output`, the command is killed, **all** captured output is discarded, and tarnhelm exits with an error.
- Where secrets overlap, the whole combined region is redacted, so no fragment of either survives.

tarnhelm's own error messages pass through the same redaction, and never include the expanded command line.

### Exit status

| Situation | Exit code |
|---|---|
| Command ran | The command's exit code (128+N if it was killed by signal N) |
| Interrupted | 128 + signal number (e.g. 130 for `Ctrl-C`) |
| Usage error | 2 |
| tarnhelm error (env file, missing key, output limit, …) | 125 |
| Command not executable | 126 |
| Command not found | 127 |

### dotenv format

Parsed with [`github.com/joho/godotenv`](https://github.com/joho/godotenv): `KEY=value`, single/double quotes, `#` comments, an optional `export` prefix. Inside double quotes, `${OTHER}` is expanded from earlier keys in the file. The file is read fresh on each invocation and never modified, cached or copied.

## Limitations

Exact-match redaction does **not** protect against:

- Encoded or transformed values: base64, URL-encoding, hex, hashes, case changes, reversal.
- Substrings of a credential, or output deliberately split across lines or interleaved.
- Exfiltration through network requests made by the command.
- Files written by the command (e.g. `tarnhelm run -- sh -c 'echo $1 > out' _ '$$KEY'` then `cat out`).
- Child processes writing directly to the terminal (`/dev/tty`) or to other file descriptors.
- An agent that reads `.env` itself. tarnhelm makes that unnecessary; it does not make it impossible.
- Secrets visible to other local users via `/proc/<pid>/cmdline` while the command runs (arguments are visible to the system).

tarnhelm is not a sandbox. It is a guard rail for well-meaning automation.

## Development

```bash
go test ./...
go build -o tarnhelm ./cmd/tarnhelm
```

Tests use synthetic credentials only.
