# Security & audit-logging policy

`fury_agent` writes **complete** model request/response logs by design — that is
the product's purpose. This makes its logs intrinsically sensitive. Read this
before running it against real providers or real data.

## What is logged

- **Raw model content** — prompts, system instructions, tool arguments, and
  model output — is logged in full when raw-content logging is enabled (the
  default for this audit build). This can contain source code, credentials the
  *model* was shown, personal data, and confidential documents.
- **Complete tool executions** — exact executable and argv, working directory,
  timing, exit status, and captured stdout/stderr up to configured caps, with a
  SHA-256 of the full stream even when stored content is truncated.
- **Optional eBPF process telemetry** — every syscall entry/exit for the agent
  process tree, raw syscall arguments, process/thread IDs, kernel monotonic
  timestamps, pathname arguments for selected calls, and tagged virtual-memory
  and cross-process memory operations. Enable with `--enable-ebpf`. Raw
  arguments and paths can contain sensitive addresses and names. Pointer
  arguments are logged as addresses; pointed-to buffers and ordinary CPU
  memory writes are not captured.
- **HTTP transport metadata** — method, sanitized URL, query *keys*, response
  status, timings, provider request IDs, and rate-limit headers.

## What is NEVER logged

Transport secrets are not model content and are always redacted, regardless of
raw-content settings:

- `Authorization`, `Proxy-Authorization`, `Cookie` / `Set-Cookie`, `X-Api-Key`,
  `Api-Key`, `X-Goog-Api-Key`, and other configured sensitive headers.
- API keys passed as query values never appear in logs — only query *keys* are
  recorded and the sanitized URL drops the query string entirely.
- Any literal registered with the redactor (the API key value is registered
  automatically) is masked wherever it appears, including inside request and
  response bodies.

A secret-leak test (`internal/transport/client_test.go`) asserts the credential
never appears in the serialized HTTP records.

## Credential handling

- API keys are read **only** from environment variables
  (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, or a configured
  `api_key_env`). They are **never** accepted via linker flags and never
  embedded in the binary.
- The transport injects authentication itself. Callers — including the native
  escape hatch — cannot supply credential headers; any they set are stripped
  before the request is sent.

## Execution safety

- Processes run as an **argv array without a shell** by default.
- The `filesystem` tool is confined to explicitly approved roots
  (`--allow-root`). With no approved roots it is not registered.
- The `shell` tool is **disabled** unless enabled by policy (`--enable-shell`).
- Every tool runs under a timeout and context cancellation; on Unix the whole
  process group is signalled so child processes are cleaned up.
- This release provides policy, timeouts, and path/process controls — **not**
  strong OS isolation. For untrusted workloads run the agent inside a container,
  VM, or OS sandbox.

## Log durability & integrity

- Session logs are created with restrictive permissions (`0600` files, `0700`
  dirs) and flushed after each write; an optional `fsync` mode is available.
- An optional per-event **hash chain** lets `agent replay-log` detect tampering
  or truncation. The scanner tolerates a truncated final line for crash
  recovery.
- Logging failures are visible: in strict mode (`--strict-logging`) the run
  stops; otherwise a degraded-logging warning is surfaced.

## Handling logs

- Treat `logs/` as sensitive. Apply your own retention, access control, and
  encryption-at-rest as your environment requires.
- Do not commit session logs to version control.

## Reporting a vulnerability

Please report security issues privately to the repository owner rather than
opening a public issue.
