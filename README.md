# tarnhelm

is a secret-aware command runner for coding agents, allowing them to execute commands requiring credentials without routinely exposing those credentials in their context.

The problem:
```bash
export SECRET_KEY=sk_2_serum

echo '$SECRET_KEY' # oh no! bye bye secret key~
```

What `tarnhelm` does instead:

```bash
tarnhelm load .env # follows open .env format, or
tarnhelm export SECRET_KEY=sk_2_serum

echo '$$SECRET_KEY' # oops no secret key for u dario 

tarnhelm run -- echo '$$SECRET_KEY' #[REDACTED]
tarnhelm run privileged-op '$$SECRET_KEY' #access granted to the privileged-op!
```

## Principles
1. Secret never appears in expanded command shown to agent; resolve placeholders internally and never echo expanded arguments
2. Secret never appears verbatim in normal stdout/stderr; hence capture streams and redact before forwarding
3. Agent doesn't need to load .env into its context; hence this Go binary reads `.env` directly
4. Existing CLI tools continue working within `tarnhelm`; hence need the ability to launch arbitrary executables with substituted arguments
5. No server or persistent process; hence a single Go executable
6. Works with .env files; hence use standard dotenv parsing
7. No special agent integration required; hence agent can invoke the CLI through normal terminal tool

