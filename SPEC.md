# Tarnhelm — v0.1 Implementation Specification

## 1. Objective

Build `tarnhelm`, a lightweight, stateless Go command runner that allows AI coding agents to execute commands requiring credentials without routinely exposing those credentials in their context.

Tarnhelm reads secrets from an existing `.env` file, substitutes opaque `$$KEY` references into command arguments, executes the command, and redacts known secrets from stdout and stderr before returning output to the caller.

**Tarnhelm is designed to prevent accidental secret exposure, not deliberate exfiltration by a malicious coding agent.**

### Design principles

1. **No secret disclosure through command expansion.** Resolve placeholders internally and never echo expanded command arguments.
2. **Redacted stdout and stderr.** Capture child output and replace exact secret values before forwarding it.
3. **No agent access to `.env` contents required.** Tarnhelm reads the file directly.
4. **Compatibility with existing CLI tools.** Support arbitrary executables and arguments without modifying those tools.
5. **Stateless execution.** Load the `.env` file for each invocation; retain no state afterward.
6. **No infrastructure.** Single Go executable, without a server, database or persistent background process.
7. **Linux only.** Target Linux AMD64 initially.
8. **Minimal dependencies.** Prefer the Go standard library except for established dotenv parsing.

## 2. Intended usage

Given an existing `.env`:

```dotenv
GITLAB_TOKEN=glpat-example123
API_KEY=sk-example456
```

Execute commands using the following syntax:

```bash
# Explicitly specify the dotenv file
tarnhelm run --env-file .env -- \
  curl -H 'PRIVATE-TOKEN: $$GITLAB_TOKEN' \
  https://gitlab.example.com/api/v4/projects

# Default to .env in the current working directory
tarnhelm run -- curl \
  -H 'Authorization: Bearer $$API_KEY' \
  https://api.example.com

# Attempt to print a secret
tarnhelm run -- echo '$$API_KEY'
# [REDACTED]

# Ordinary commands work
tarnhelm run -- echo 'hello world'
# hello world
```

The ordinary shell is not modified.

For example:

```bash
echo '$$API_KEY'
# $$API_KEY
```

Tarnhelm only interprets placeholders inside commands invoked through `tarnhelm run`.

Shell arguments containing `$$KEY` must be appropriately quoted so Bash does not expand `$$` into its process ID.

## 3. CLI contract

Implement one command:

```text
tarnhelm run [--env-file PATH] -- COMMAND [ARG...]
```

### Flags

| Flag | Behaviour |
|---|---|
| `--env-file PATH` | Path to the dotenv file. Default: `./.env` |
| `--max-output BYTES` | Maximum captured output size. Default: 16 MiB |
| `--help` | Display usage information |

The `--` separator is required.

Paths should be interpreted relative to the current working directory.

### Execution sequence

1. Parse flags and command arguments.
2. Read the selected dotenv file.
3. Parse the dotenv contents.
4. Identify valid `$$KEY` references in command arguments.
5. Resolve every reference against the parsed secrets.
6. Abort before execution if a referenced key is missing.
7. Substitute values internally, preserving argument boundaries.
8. Execute the target command as a child process.
9. Capture stdout and stderr.
10. Redact all known secret values.
11. Forward sanitised output.
12. Propagate the child's exit status.

Tarnhelm must not print expanded commands or secrets during any stage.

## 4. Dotenv loading

Use a maintained Go dotenv parser, such as `github.com/joho/godotenv`.

Requirements:

- Read the specified file for every invocation.
- Support standard dotenv syntax, including quoted values and comments.
- Reject malformed files with a descriptive error.
- Reject an empty dotenv file only if a referenced secret cannot be resolved.
- Do not automatically search parent directories.
- Do not modify the `.env` file.
- Do not copy its contents into another persistent store.
- Do not load the secrets into the parent shell.
- Do not inject all parsed secrets into the child process's environment.

For v0.1, the parsed dotenv file is the authoritative source for placeholder resolution.

Only secrets explicitly referenced in arguments are substituted.

However, **all non-empty values in the selected dotenv file should be included in stdout/stderr redaction**, even if they were not referenced by the command.

This protects against incidental output of another known credential.

Do not treat an empty dotenv value as a secret to redact.

## 5. Placeholder semantics

Recognise placeholders matching:

```text
$$[A-Za-z_][A-Za-z0-9_]*
```

Examples:

```text
$$API_KEY
Bearer $$GITLAB_TOKEN
postgres://user:$$PASSWORD@localhost:5432/db
```

### Requirements

- Variable names are case-sensitive.
- Multiple placeholders may appear in one argument.
- The same placeholder may appear multiple times.
- Placeholder substitution must preserve argument boundaries.
- Ordinary `$KEY` expressions are not interpreted.
- Missing keys cause execution to fail before the child starts.
- Empty values are valid only if explicitly permitted; default to rejecting empty referenced values.
- No recursive expansion of replacement values.

### Escaping

Support a literal placeholder escape:

```text
$$$$API_KEY
```

Tarnhelm should interpret this as the literal string:

```text
$$API_KEY
```

It must not resolve the escaped reference.

Document this behaviour.

## 6. Process execution

Use Go's `os/exec` package.

### Requirements

- Execute binaries directly.
- Do not implicitly invoke Bash or another shell.
- Preserve command argument boundaries.
- Inherit the parent's working directory.
- Inherit the ordinary parent environment.
- Do not add dotenv secrets to the child environment automatically.
- Preserve the target command's exit status.
- Capture stdout and stderr independently.
- Forward only sanitised output.
- Do not print expanded command arguments when execution fails.

### Process lifecycle

Use Linux process groups to manage child processes.

On interruption:

1. Forward a termination signal to the child process group.
2. Allow a short grace period.
3. Force termination if necessary.
4. Exit with an appropriate status.

Do not leave runaway child processes.

### Explicit non-goal

Tarnhelm does not interpret shell constructs such as pipes, redirections, globbing or command substitution.

For example:

```bash
tarnhelm run -- echo hello '|' cat
```

Must pass `|` as a literal argument, not create a pipeline.

Shell execution can be added later if required, but must not be an implicit feature of v0.1.

## 7. Output redaction

This is the most important component.

Tarnhelm must capture child stdout and stderr before allowing either stream to reach the terminal.

Before forwarding output, replace every exact occurrence of every non-empty dotenv value with:

```text
[REDACTED]
```

### Examples

Input:

```text
Authentication failed for glpat-example123
```

Output:

```text
Authentication failed for [REDACTED]
```

Input:

```text
API_KEY=sk-example456
```

Output:

```text
API_KEY=[REDACTED]
```

### Requirements

- Redact stdout.
- Redact stderr.
- Redact Tarnhelm-generated error messages.
- Redact every non-empty dotenv value.
- Handle multiple secrets.
- Handle overlapping secrets.
- Match longer secrets before shorter secrets.
- Preserve ordinary output.
- Never forward raw child output.

### Implementation strategy

For v0.1, use **bounded buffering rather than streaming**.

1. Capture stdout and stderr in memory.
2. Enforce a combined output-size limit.
3. Wait for the command to terminate.
4. Redact both buffers.
5. Write sanitised output to the corresponding parent streams.

This avoids edge cases where a secret spans multiple read chunks.

The default combined maximum output size is 16 MiB.

If the limit is exceeded:

- Terminate the child process group.
- Discard unsanitised captured output.
- Return a safe error indicating the output limit was exceeded.
- Exit with a non-zero status.

Do not silently truncate and continue without making that behaviour explicit.

### Redaction limitations

Exact-match redaction does not protect against:

- Base64-encoded values.
- URL-encoded values.
- Hashes.
- Substrings of credentials.
- Deliberately fragmented output.
- Secret exfiltration through network requests.
- Files written by child processes.
- Child processes bypassing captured streams.

Document these limitations.

## 8. Error handling

The program must fail safely without disclosing secrets.

| Condition | Expected behaviour |
|---|---|
| `.env` does not exist | Clear error, no execution |
| Invalid dotenv syntax | Sanitised parsing error |
| Missing secret reference | Fail before child execution |
| Empty referenced secret | Fail before child execution |
| Executable not found | Sanitised error |
| Child exits non-zero | Return redacted output and propagate exit code |
| Output exceeds 16 MiB | Terminate child and discard raw output |
| Command interrupted | Terminate child process group |
| No command supplied | Print usage error |

Avoid logging the complete argument vector after expansion.

Avoid returning raw Go errors that may include expanded command arguments.

The wrapper's own errors must pass through the same redaction mechanism where relevant.

## 9. Project structure

Keep the implementation compact.

```text
tarnhelm/
├── cmd/
│   └── tarnhelm/
│       └── main.go
├── internal/
│   ├── dotenv/
│   │   └── dotenv.go
│   ├── expand/
│   │   └── expand.go
│   ├── redact/
│   │   └── redact.go
│   └── runner/
│       └── runner.go
├── go.mod
├── go.sum
├── README.md
└── .gitignore
```

Avoid introducing unnecessary abstraction layers.

The CLI should be understandable without an elaborate application architecture.

## 10. Automated tests

Write unit and integration tests covering the following.

### Dotenv parsing

- Standard key-value pairs.
- Quoted values.
- Values containing spaces.
- Missing files.
- Empty files.
- Malformed syntax.
- Empty secret values.

### Placeholder expansion

- Single placeholder.
- Multiple placeholders.
- Repeated placeholder.
- Placeholder embedded in an argument.
- Missing secret.
- Empty referenced secret.
- Escaped literal placeholder.
- No recursive expansion.
- Ordinary `$KEY` left untouched.

### Process execution

- Successful executable invocation.
- Correct argument substitution.
- Arguments containing spaces.
- Ordinary commands with no placeholders.
- Executable not found.
- Non-zero child exit code.
- Correct working directory.
- No automatic injection of dotenv secrets into the child environment.
- Signal handling and child cleanup.

### Redaction

- Secret printed to stdout.
- Secret printed to stderr.
- Secret printed multiple times.
- Multiple different secrets.
- Overlapping secret values.
- Secret adjacent to punctuation.
- Secret embedded in a longer string.
- Ordinary output unchanged.
- Output larger than 16 MiB.
- Sanitisation of execution errors.

### Integration test

Create a small test executable that:

1. Accepts a secret as a command argument.
2. Validates that the argument matches the expected synthetic value.
3. Prints the secret to stdout.
4. Prints the secret to stderr.
5. Exits successfully.

Run the executable through Tarnhelm.

The test passes only if:

- The executable receives the expected plaintext.
- The plaintext does not appear in Tarnhelm's stdout.
- The plaintext does not appear in Tarnhelm's stderr.
- The output contains `[REDACTED]` instead.
- The process exits successfully.

Use synthetic credentials only.

## 11. Build and distribution

Target Linux AMD64 initially.

The project must support:

```bash
go test ./...
go build -o tarnhelm ./cmd/tarnhelm
```

Produce a single executable.

No installation script is required for v0.1.

The README should explain how to place the binary on the user's `PATH`.

GoReleaser and GitLab Release automation can be added later. Do not implement them in the first iteration.

## 12. Non-goals

Do not implement:

- `load`, `export`, `list` or persistent secret storage.
- A custom shell.
- A server or daemon.
- MCP integrations.
- GitLab-specific functionality.
- AI agent-specific integrations.
- Remote secret management.
- Encryption or keychain integration.
- Windows or macOS support.
- Network proxies.
- Credential rotation.
- Shell pipes or redirection.
- Automatic `.env` discovery beyond the current working directory.
- A general-purpose security sandbox.

## 13. Definition of done

Tarnhelm v0.1 is complete when:

1. A coding agent can invoke `tarnhelm run` using an existing `.env` file.
2. The agent does not need to read or print the `.env` contents.
3. Referenced secrets are correctly substituted into executable arguments.
4. Commands using those secrets execute successfully.
5. Exact plaintext values from the dotenv file are redacted from captured stdout and stderr.
6. Failures cannot accidentally print expanded arguments or raw captured output through Tarnhelm's normal output path.
7. No persistent secret store or background service is created.
8. All automated tests pass on Linux.
9. The README clearly documents the threat model, usage and limitations.

**Implementation priority:** Correctness and predictable redaction first. Keep the program small, stateless and easy to audit.