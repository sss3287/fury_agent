# Plan: Lightweight Cross-Platform Go Model Agent

**Status:** Proposed implementation plan  
**Primary goal:** Produce a small, auditable Go agent that compiles to a single cross-platform binary and includes exactly one model-provider implementation per build.

## 1. Product definition

Build a command-line agent that:

- Runs as a single native executable with no required runtime other than the operating system.
- Is compiled for exactly one provider family at a time: OpenAI, Anthropic, Gemini, or a future custom router.
- Is compiled with a default model identifier and normally cannot switch provider or model at runtime.
- Supports streaming and non-streaming model turns, function/tool calls, structured output, multimodal message parts, usage metadata, reasoning/thinking options where available, cancellation, retries, and provider-native features.
- Can reach every endpoint and request field exposed by the selected provider through a provider-native escape hatch, even when the feature is not represented by the common agent interface yet.
- Executes local tools under explicit policy and records the command, arguments, working directory, timing, exit status, stdout, stderr, truncation, and errors.
- Writes append-only JSON logs containing complete model request/response turn pairs and tool executions.
- Remains easy to extend with new model IDs and a custom model router without changing the agent loop.

## 2. Explicit non-goals for the first release

- A graphical interface.
- A multi-provider binary that dynamically selects OpenAI, Anthropic, or Gemini at runtime.
- A general-purpose sandbox with strong operating-system isolation. The first release provides policy, timeouts, path controls, and process controls; strong isolation should use an external container, VM, or OS sandbox.

- A distributed agent service, persistent queue, or multi-user server.
- A universal schema that erases provider-specific semantics.

### Optional Linux process-tree telemetry

When enabled, a native Go eBPF observer attaches to generic syscall-entry,
syscall-exit, and process-fork tracepoints for the agent process. A BPF PID map
propagates scope to descendants, and a ring buffer streams selected syscall
entry/return events into the session event log. The implementation targets
Linux amd64 and arm64 and does not use a separate `bpftrace` process or require
a C compiler at runtime. `--enable-ebpf` is best-effort; `--require-ebpf` fails
the run before provider initialization if the kernel does not permit map
creation or tracepoint attachment. Captured pathname strings are sensitive;
arbitrary syscall arguments and buffers are excluded. This is observability
only and does not enforce policy or isolate the process tree.

## 3. Key architectural decisions

### 3.1 Compile one provider into each binary

Use Go build constraints so only one provider package is linked into a build.

Suggested tags:

- `provider_openai`
- `provider_anthropic`
- `provider_gemini`
- `provider_router`

Each tagged file exposes the same constructor:

```go
func NewCompiledProvider(cfg ProviderConfig, transport *http.Client) (Provider, error)
```

Add compile-failure guards for these invalid states:

- No provider tag selected.
- More than one provider tag selected.

Provider identity is a compile-time constant. Embed the default model and optional base URL with linker variables:

```text
-X main.buildProvider=openai
-X main.buildModel=gpt-...
-X main.buildBaseURL=https://...
-X main.buildVersion=<git tag or commit>
-X main.buildCommit=<commit>
-X main.buildTime=<UTC timestamp>
```

Runtime model overrides should be disabled by default. A separate build flag such as `allow_model_override=true` may enable an allow-listed override for development builds without permitting provider changes.

### 3.2 Prefer a thin REST implementation over mandatory provider SDKs

Use `net/http`, `encoding/json`, and a small SSE/WebSocket layer. Reasons:

- Keeps the dependency graph and binary size controlled.
- Makes request and response logging complete and consistent.
- Avoids forcing the core agent to inherit every provider SDK abstraction.
- Allows unknown JSON fields and new API features to pass through using `json.RawMessage`.
- Makes a future custom router a natural additional transport adapter.

Official SDKs can still be evaluated as optional implementation details inside a provider package, but the public interfaces must not depend on SDK types.

### 3.3 Use a narrow common interface plus a complete native interface

The common interface powers the agent loop. The native interface preserves full provider coverage.

```go
type Provider interface {
    Identity() ProviderIdentity
    Capabilities(ctx context.Context) (Capabilities, error)
    Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
    Stream(ctx context.Context, req GenerateRequest) (EventStream, error)
    CountTokens(ctx context.Context, req TokenCountRequest) (TokenCountResponse, error)
    Native() NativeClient
}

type NativeClient interface {
    Do(ctx context.Context, req NativeRequest) (NativeResponse, error)
    Stream(ctx context.Context, req NativeRequest) (NativeEventStream, error)
}
```

`NativeRequest` must support:

- HTTP method.
- Relative provider path or approved absolute router URL.
- Query parameters.
- Additional non-secret headers.
- Raw body bytes or `json.RawMessage`.
- Response mode: JSON, bytes, SSE, or WebSocket where supported.
- Timeout and retry policy override.

The transport must always inject authentication itself. Callers must not supply authorization headers through `NativeRequest`.

### 3.4 Preserve provider-native content

Define a useful shared message model, but include native extensions at every important level:

```go
type GenerateRequest struct {
    Model        string
    System       []ContentPart
    Messages     []Message
    Tools        []ToolDefinition
    ToolChoice   ToolChoice
    Output       OutputConstraint
    Stream       bool
    MaxOutput    int
    Temperature  *float64
    Metadata     map[string]string
    Native       json.RawMessage
}
```

Responses should retain:

- Normalized text and tool calls.
- Provider request ID and response ID.
- Stop/finish reason.
- Usage and cached-token metadata.
- Reasoning/thinking metadata that the provider permits the client to receive.
- Citations and grounding metadata.
- Raw provider response JSON.
- Unknown event types in streaming mode.

Never discard an unknown provider event. Log it and expose it as a native stream event.

## 4. Proposed repository layout

```text
cmd/agent/
  main.go
  compiled_provider_openai.go
  compiled_provider_anthropic.go
  compiled_provider_gemini.go
  compiled_provider_router.go
  provider_guard_*.go

internal/agent/
  loop.go
  state.go
  limits.go
  errors.go

internal/model/
  types.go
  content.go
  tools.go
  stream.go
  native.go

internal/provider/openai/
  client.go
  responses.go
  streaming.go
  native.go
  translate.go

internal/provider/anthropic/
  client.go
  messages.go
  streaming.go
  native.go
  translate.go

internal/provider/gemini/
  client.go
  interactions.go
  generate_content.go
  live.go
  native.go
  translate.go

internal/provider/router/
  client.go
  auth.go
  native.go
  translate.go

internal/transport/
  client.go
  retry.go
  sse.go
  websocket.go
  instrumentation.go
  redaction.go

internal/tools/
  registry.go
  executor.go
  policy.go
  result.go
  shell/
  filesystem/
  http/

internal/logging/
  logger.go
  events.go
  turns.go
  rotation.go
  redact.go
  integrity.go

internal/config/
  build.go
  runtime.go
  validate.go

internal/buildinfo/
  info.go

testdata/
  provider_fixtures/
  streams/
  logs/

scripts/
  build-matrix.sh
  build-matrix.ps1

Makefile
README.md
SECURITY.md
```

## 5. Agent loop

The core loop must not know which provider was compiled.

1. Create a session and write a `session_start` event.
2. Accept a user instruction from stdin, a CLI argument, or a file.
3. Build the normalized model request.
4. Assign `session_id`, `turn_id`, `attempt_id`, and a monotonic sequence number.
5. Write the complete outbound request to the turn log before transmission.
6. Call the provider in streaming or non-streaming mode.
7. Persist stream events as they arrive and accumulate a canonical final response.
8. If the model requests tools:
   - Validate tool name and arguments.
   - Apply policy and approval requirements.
   - Execute with context cancellation and timeout.
   - Write a complete tool execution record.
   - Add the tool result to conversation state.
   - Continue the same logical turn with a new model attempt.
9. When no more tools are requested, write the completed turn pair.
10. Stop on success, cancellation, a configured maximum number of model calls, maximum tool calls, maximum elapsed time, or budget limit.

Required loop limits:

- Maximum model calls per run.
- Maximum tool calls per run and per turn.
- Maximum wall-clock duration.
- Maximum cumulative input/output bytes.
- Maximum tool output bytes.
- Maximum retry count.
- Optional token or cost budget when usage data is available.

## 6. API coverage strategy

### 6.1 Common agent capability tier

Implement and test these capabilities for every provider where the provider supports them:

- Text input and output.
- Streaming text and event accumulation.
- System/developer instructions.
- Multi-turn conversation history.
- Function/tool declarations and tool results.
- Parallel tool calls when the provider allows them.
- Structured JSON output or schema-constrained output.
- Images, documents, audio, and other supported content parts without forcing unsupported conversions.
- Usage/token data.
- Reasoning/thinking controls and returned metadata.
- Prompt/context caching controls.
- Provider request IDs, errors, retry hints, and rate-limit metadata.
- Cancellation and timeouts.

### 6.2 Provider-native endpoint tier

Every selected provider build must expose the native client so all provider endpoints remain reachable. Add typed convenience methods in priority order, but do not block endpoint access on typed support.

Candidate typed groups:

- Generation or interaction endpoints.
- Models and capabilities discovery.
- Token counting.
- Files and uploads.
- Embeddings.
- Batch operations.
- Caches or stored context.
- Images, audio, video, realtime/live, or other media endpoints.
- Provider-hosted tools.
- Moderation or safety endpoints.

A native endpoint conformance test should verify arbitrary JSON requests, binary uploads/downloads, SSE, and unknown response fields.

### 6.3 Flagship and future model support

Do not encode a closed enum of model IDs in core code. Treat the build-time model as a validated string and use capability discovery or a maintained capability table only to improve validation and defaults.

Model-specific behavior belongs in provider capability rules, not in the agent loop. Unknown models should be allowed in development builds and rejected only when a provider proves they are invalid.

## 7. Provider adapter notes

### 7.1 OpenAI build

Primary agent path: the current Responses-style interaction API, including streaming events and tool calls.

The adapter should also preserve native access to conversations, files/uploads, embeddings, batches, images, audio, realtime, models, moderation, and other platform endpoints made available to the API key.

Important implementation detail: the normalized response must support multiple output item types rather than assuming a single assistant string.

### 7.2 Anthropic build

Primary agent path: Messages, including SSE streaming, content blocks, tool-use blocks, stop reasons, token counting, and thinking/reasoning blocks when enabled and returned.

Preserve ordered content blocks. Do not flatten mixed text, tool, citation, document, or thinking content into one string.

Native coverage should include models, files, message batches, token counting, and beta headers/features available to the account.

### 7.3 Gemini build

Support both the recommended interaction-oriented API and standard content generation endpoints when useful. Include SSE streaming and the live bidirectional API as separate transport modes.

Preserve Gemini parts, candidates, function calls/responses, grounding metadata, safety results, thought signatures where applicable, and provider-hosted tool results.

Native coverage should include models, files, token counting, embeddings, caches, batches, generated media, live APIs, and other platform resources.

### 7.4 Future router build

The router adapter must use the same `Provider` and `NativeClient` contracts. Build-time configuration should include:

- Router base URL.
- Router protocol dialect: OpenAI-compatible, Anthropic-compatible, Gemini-compatible, or custom.
- Authentication scheme identifier, not the secret.
- Default routed model name.
- Optional tenant/project header names.

Secrets and tenant values remain runtime inputs. The router adapter should support request signing as a pluggable authentication strategy.

## 8. Tool system

### 8.1 Tool interface

```go
type Tool interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, call ToolCall) ToolResult
}
```

Each tool result must contain:

- Tool call ID.
- Tool name.
- Validated arguments.
- Start and end timestamps.
- Monotonic duration.
- Status: success, error, timeout, cancelled, denied, or truncated.
- Structured result.
- Human-readable result for the model.
- Native execution metadata.

### 8.2 Initial tools

Keep the initial set small:

1. **Filesystem:** read, write, patch, list, stat, and search inside approved roots.
2. **Process:** execute an argv array without a shell by default.
3. **Shell:** explicit shell execution, disabled unless policy enables it.
4. **HTTP:** optional outbound HTTP with hostname and method policy.

Use separate OS-specific process-control files for Unix and Windows.

### 8.3 Command execution record

For every process or shell call, record:

- Exact executable.
- Exact argv array.
- Rendered command for display only.
- Whether a shell was used and which shell.
- Working directory.
- Environment variable names provided; values redacted by default.
- Stdin byte count and optional captured stdin subject to logging policy.
- PID when available.
- Start/end UTC timestamps and monotonic duration in nanoseconds.
- Exit code or terminating signal.
- Timeout/cancellation state.
- Full stdout and stderr up to configured caps.
- Original byte counts and truncation flags.
- SHA-256 digests of complete streams when the complete stream can be hashed while truncating stored content.

Never reconstruct execution from a display string. Execute the stored argv form.

## 9. Logging and observability

### 9.1 File format

Use newline-delimited JSON (`.jsonl`) rather than one large JSON array. JSONL is appendable, streamable, easy to recover after a crash, and suitable for command-line processing.

Recommended files per session:

```text
logs/<date>/<session_id>/events.jsonl
logs/<date>/<session_id>/turns.jsonl
logs/<date>/<session_id>/summary.json
logs/<date>/<session_id>/artifacts/
```

- `events.jsonl`: every model transport event, retry, tool event, policy decision, and lifecycle event.
- `turns.jsonl`: completed logical turn pairs with outbound input, all provider attempts, tool calls/results, and final model output.
- `summary.json`: final status, totals, provider/build metadata, usage, timing, and integrity information.

### 9.2 Common event envelope

Every event should use a stable envelope:

```json
{
  "schema_version": "1.0",
  "event_id": "evt_...",
  "sequence": 42,
  "timestamp_utc": "2026-07-14T18:22:31.123456Z",
  "monotonic_ns": 912345678,
  "event_type": "model_response_complete",
  "session_id": "ses_...",
  "turn_id": "turn_...",
  "attempt_id": "attempt_...",
  "tool_call_id": null,
  "provider": "openai",
  "model": "build-time-model-id",
  "build": {
    "version": "...",
    "commit": "...",
    "provider_tag": "provider_openai",
    "go_version": "...",
    "target_os": "...",
    "target_arch": "..."
  },
  "data": {}
}
```

### 9.3 Turn-pair record

A turn is a logical user-to-final-assistant exchange and may contain several model requests because tools were executed.

```json
{
  "schema_version": "1.0",
  "session_id": "ses_...",
  "turn_id": "turn_...",
  "started_at": "...",
  "completed_at": "...",
  "duration_ns": 0,
  "input": {
    "normalized": {},
    "provider_request": {},
    "request_body_sha256": "..."
  },
  "attempts": [
    {
      "attempt_id": "attempt_...",
      "request": {},
      "stream_events": [],
      "response": {},
      "raw_response": {},
      "http": {},
      "usage": {},
      "duration_ns": 0
    }
  ],
  "tool_executions": [],
  "output": {
    "normalized": {},
    "provider_response": {},
    "response_body_sha256": "..."
  },
  "status": "success"
}
```

For large streams, `turns.jsonl` may reference an event sequence range instead of duplicating every stream delta. The final accumulated request and response must still be present in the turn record.

### 9.4 HTTP and streaming instrumentation

Create an instrumented `http.RoundTripper` that records:

- Method, sanitized URL, query keys, request headers, and body.
- DNS, connection, TLS, request-write, first-byte, and total timing where available.
- Status code and response headers.
- Provider request IDs and rate-limit headers.
- Response body and stream frames.
- Retry number, backoff, retry reason, and final disposition.

Authentication headers, API keys, cookies, and configured sensitive headers must never be persisted. Raw model content is logged because that is a product requirement, but transport secrets are not model content and must always be redacted.

### 9.5 Log durability, rotation, and integrity

- Open session logs with restrictive permissions.
- Flush after each completed model request and tool execution.
- Provide an optional `fsync` mode for audit-sensitive runs.
- Rotate by session and optionally by size.
- Cap individual field sizes and write truncation metadata.
- Optionally chain event hashes: each event stores the previous event hash and its own canonical JSON hash.
- Never fail the agent silently when logging fails. Either stop the run in strict audit mode or emit a visible degraded-logging error in normal mode.

### 9.6 Privacy and secret handling

Complete model logs can contain credentials, source code, personal data, and confidential documents. Therefore:

- API keys are accepted only through environment variables, OS credential helpers, stdin, or injected files; never linker flags.
- Authorization and cookie headers are always redacted.
- Environment values are redacted unless explicitly allow-listed for capture.
- Add configurable redaction rules for JSON paths, headers, regex patterns, and tool arguments.
- Add retention and maximum-storage settings.
- Document that raw-content logging is intentionally sensitive.
- Provide optional application-level encryption for log files in a later milestone.

## 10. Runtime configuration

Provider and default model are build-time values. Runtime configuration is limited to operational settings:

```text
API credential source
log directory
log level
raw-content logging on/off (on for the requested audit build)
strict logging mode
timeouts and retries
maximum model/tool calls
maximum output sizes
tool policy and approved filesystem roots
network allow-list
interactive approval policy
proxy and custom CA settings
```

Configuration precedence:

1. Compiled invariants.
2. Explicit command-line flags.
3. Configuration file.
4. Environment variables.
5. Safe defaults.

Print the effective configuration with secrets redacted via `agent doctor`.

## 11. CLI shape

```text
agent run [instruction]
agent run --input task.md
agent run --stream
agent doctor
agent build-info
agent validate-config
agent replay-log <session-dir>    # validates and summarizes; does not re-execute by default
```

Useful exit codes:

- `0`: success.
- `2`: invalid configuration or request.
- `3`: provider/authentication error.
- `4`: provider rate limit or temporary failure after retries.
- `5`: tool failure.
- `6`: policy denial.
- `7`: timeout/cancellation.
- `8`: required logging failure.
- `10`: internal error.

## 12. Cross-platform build plan

Target the initially supported matrix:

- Linux: `amd64`, `arm64`.
- macOS: `amd64`, `arm64`.
- Windows: `amd64`, `arm64` where supported by dependencies and CI.

Build with `CGO_ENABLED=0` unless a future feature explicitly requires cgo. Avoid dependencies that require native libraries.

Example build intent:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -tags provider_openai \
  -ldflags "-s -w -X main.buildProvider=openai -X main.buildModel=$MODEL -X main.buildCommit=$COMMIT" \
  -o dist/agent-openai-linux-amd64 ./cmd/agent
```

Create equivalent build scripts for PowerShell and Unix shells. Use a release matrix in CI and run the native test subset on each target OS.

Size controls:

- `-trimpath` for reproducibility and path removal.
- `-s -w` for release builds after retaining separate debug symbols where required.
- No embedded CA bundle unless deployment requirements demand it.
- No provider SDK or adapter for providers not selected by the build tag.
- Measure binary size in CI and fail on an agreed regression threshold.

## 13. Error handling and retries

Normalize errors without losing native details:

```go
type ProviderError struct {
    Provider      string
    Operation     string
    HTTPStatus    int
    Code          string
    Message       string
    RequestID     string
    Retryable     bool
    RetryAfter    time.Duration
    Native        json.RawMessage
    Cause         error
}
```

Retry only when safe:

- Connection establishment failures before a request is written.
- Explicit rate limits and transient server errors.
- Idempotent native calls.
- Generation requests only when duplicate effects are understood and each attempt is independently logged.

Use exponential backoff with jitter and respect provider retry headers. Never hide retries inside an SDK or transport without logging them.

## 14. Testing strategy

### 14.1 Unit tests

- Request translation for each provider.
- Response and stream-event translation.
- Unknown-field/event preservation.
- Redaction and secret-header removal.
- Retry classification and backoff.
- Tool argument validation.
- Command timeout, cancellation, truncation, and exit status.
- JSONL schema and hash-chain validation.
- Build-tag provider guards.

### 14.2 Contract tests with local fake servers

Provide fake HTTP, SSE, and WebSocket servers that test:

- Normal response.
- Chunk boundaries in arbitrary positions.
- Malformed JSON and malformed SSE.
- Unknown event types.
- Mid-stream disconnect.
- Rate limits and retry headers.
- Slow first byte and slow body.
- Oversized request and response.
- Binary upload/download.
- Tool call followed by tool result and final answer.

### 14.3 Live smoke tests

Run opt-in tests against real provider accounts:

- One non-streaming turn.
- One streaming turn.
- One tool round trip.
- One structured-output response.
- One multimodal request supported by the selected model.
- Token usage and request ID capture.

Never run live tests by default in forks or pull requests. Mask secrets and retain the same local logs for audit.

### 14.4 Cross-platform tests

On Linux, macOS, and Windows, test:

- Binary startup and build information.
- TLS connection using system trust.
- Unicode arguments and output.
- Paths with spaces.
- Process timeout and child-process cleanup.
- File permissions or ACL behavior.
- Ctrl-C cancellation.
- Log rotation and crash recovery.

## 15. Delivery milestones

### Milestone 0: Decisions and schemas

- Finalize terminology: session, turn, attempt, event, tool call.
- Freeze logging schema version `1.0`.
- Define provider, native, stream, and tool interfaces.
- Define the security and raw-log policy.

**Exit criterion:** interfaces and example logs reviewed before provider code is written.

### Milestone 1: Core executable and logging

- CLI, configuration, build metadata, session lifecycle.
- JSONL writer, turn aggregator, redaction, rotation, strict logging mode.
- Instrumented HTTP transport and SSE parser.
- Build-tag guard implementation.

**Exit criterion:** a fake provider produces a complete turn log and passes crash-recovery tests.

### Milestone 2: First provider vertical slice

Implement one provider end to end, preferably the provider needed first:

- Non-streaming generation.
- Streaming.
- Tool calls.
- Structured output.
- Native `Do` and native streaming.
- Usage, errors, request IDs, and retries.

**Exit criterion:** real smoke tests and golden logs pass.

### Milestone 3: Tool runtime

- Registry and JSON Schema validation.
- Filesystem and process tools.
- Shell tool behind policy.
- Timeouts, cancellation, output caps, and complete execution logging.

**Exit criterion:** multi-step model/tool/model loop works on all target operating systems.

### Milestone 4: Remaining provider adapters

- Anthropic adapter.
- Gemini adapter.
- Capability and translation conformance suite.

**Exit criterion:** the same core agent tests pass against all three separately built binaries.

### Milestone 5: Native endpoint breadth

- Add typed convenience clients for high-value non-agent endpoints.
- Verify generic native coverage for all documented API resource shapes.
- Add upload, download, batch, and realtime/live transport tests.

**Exit criterion:** no provider endpoint category requires changing the core agent interface to be reachable.

### Milestone 6: Router readiness and release hardening

- Router adapter and authentication strategy interface.
- Reproducible release builds and checksums.
- SBOM, dependency scanning, vulnerability scanning, and license report.
- Performance and binary-size budgets.
- Operational and security documentation.

**Exit criterion:** signed release artifacts for the supported OS/architecture matrix.

## 16. Acceptance criteria

The project is ready for its first stable release when all of the following are true:

- A build includes exactly one provider implementation, proven by build metadata and binary inspection.
- Provider and default model are embedded at build time.
- The binary runs without cgo or external shared-library requirements.
- Linux, macOS, and Windows builds pass native smoke tests.
- Streaming, tool calls, structured outputs, cancellation, and retries work for each provider where supported.
- Every outbound model request and inbound response is represented in a completed turn-pair JSON record.
- Every transport stream event can be retained in the event log.
- Every tool execution records exact argv/command metadata, duration, result, exit status, stdout, and stderr subject to explicit size caps.
- Authorization secrets never appear in logs in automated secret-leak tests.
- Unknown provider fields and stream events are preserved.
- Any documented provider endpoint can be called through the native client without adding a new core interface method.
- A failed log write is visible and obeys strict/non-strict policy.
- The agent stops predictably at configured call, time, size, and budget limits.
- Release artifacts include version, commit, provider, model, target OS/architecture, checksum, and SBOM metadata.

## 17. Recommended first implementation choices

- Start with the provider needed for the first real deployment; do not implement three shallow adapters in parallel.
- Use direct REST plus small provider-specific structs and `json.RawMessage` extensions.
- Use `log/slog` for human/operator diagnostics, but use dedicated schema-controlled JSONL writers for audit logs.
- Make raw prompt/response logging enabled in the requested audit build, while always redacting transport credentials.
- Execute processes by argv without a shell by default.
- Keep a provider-native request path from day one.
- Establish size, memory, startup-time, and logging-throughput benchmarks before adding provider SDKs or large dependencies.

## 18. Open decisions to resolve before coding

These choices do not block the architecture, but should be recorded in the repository:

- Which provider and model are the first vertical slice.
- Whether a model override is ever allowed in production builds.
- Whether raw tool stdin and environment values may be captured.
- Default maximum sizes for request bodies, response bodies, stdout, stderr, and stream-event retention.
- Whether strict logging is the production default.
- Required retention period and whether encrypted logs are mandatory.
- Whether shell and outbound HTTP tools are present in the first release.
- Whether provider-hosted tools are exposed directly, normalized into local tool events, or both.
- Whether the router protocol will initially be OpenAI-compatible or custom.

## 19. Documentation references used for this plan

The implementation should be checked against the current official API references at coding time because provider endpoints and event types evolve. Primary references reviewed for this plan were:

- OpenAI API Reference: Responses API, streaming, tools, and platform endpoint catalog.
- Anthropic Claude API Reference: Messages, streaming content blocks, tools, models, files, and token counting.
- Google Gemini API Reference: Interactions, GenerateContent, streaming, Live API, tools, models, files, caches, batches, embeddings, and media resources.
- Go documentation: cross-compilation using target `GOOS`/`GOARCH` and structured logging with `log/slog`.
