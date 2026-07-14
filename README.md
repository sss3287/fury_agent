# fury_agent

A small, auditable command-line **model agent** written in Go. It compiles to a
single static binary that is bound to **exactly one** model provider at build
time, streams model turns, calls local tools under an explicit policy, and writes
a **complete append-only audit trail** of every model request/response and tool
execution.

---

## TL;DR — get it running in 60 seconds

The fastest path uses the built-in **fake provider**, which needs no API key and
no network:

```bash
# 1. Build a binary wired to the offline fake provider
CGO_ENABLED=0 go build -tags provider_fake \
  -ldflags "-X github.com/ahhh/fury_agent/internal/buildinfo.Provider=fake \
            -X github.com/ahhh/fury_agent/internal/buildinfo.Model=fake-1 \
            -X github.com/ahhh/fury_agent/internal/buildinfo.ProviderTag=provider_fake" \
  -o dist/agent ./cmd/agent

# 2. Run one instruction, with streaming and a tool round-trip, allowing tools
#    to run inside the current directory
./dist/agent run --stream --allow-root "$(pwd)" "run echo via the process tool and summarize"

# 3. Read the audit log it just wrote
./dist/agent replay-log logs/$(date -u +%F)/ses_*
```

To talk to a **real** provider, build with that provider's tag and a real model,
then set the API key in the environment:

```bash
CGO_ENABLED=0 go build -tags provider_anthropic \
  -ldflags "-X github.com/ahhh/fury_agent/internal/buildinfo.Provider=anthropic \
            -X github.com/ahhh/fury_agent/internal/buildinfo.Model=claude-opus-4-8 \
            -X github.com/ahhh/fury_agent/internal/buildinfo.ProviderTag=provider_anthropic" \
  -o dist/agent-anthropic ./cmd/agent

export ANTHROPIC_API_KEY=sk-ant-...
./dist/agent-anthropic run --stream "Explain what this repository does."
```

> One binary = one provider + one default model, embedded at build time. This is
> a deliberate design choice (see **Architecture** below), not a limitation you
> work around at runtime.

---

## Why this design

- **Auditable by construction.** Every outbound model request and inbound
  response is recorded as a completed turn-pair; every tool call records exact
  argv, working directory, timing, exit status, and captured stdout/stderr. Logs
  are newline-delimited JSON (`.jsonl`): appendable, streamable, and recoverable
  after a crash.
- **One provider per binary.** Go build tags link exactly one provider adapter.
  A build with no provider tag, or two provider tags, fails to compile with a
  clear message — you can never ship an ambiguous binary.
- **Thin REST, no mandatory SDKs.** Providers are implemented directly on
  `net/http` + `encoding/json`, so logging is complete and consistent and unknown
  JSON fields / new API features pass through untouched.
- **A native escape hatch.** Alongside the narrow common interface, every build
  exposes a native client that can reach *any* provider endpoint and field, even
  ones the common interface does not model yet.
- **Single static binary.** Builds with `CGO_ENABLED=0`, no external shared
  libraries, cross-compiles to Linux/macOS/Windows on amd64/arm64.

---

## Architecture

The core agent loop never knows which provider was compiled in. Data flows
through a narrow common interface (`model.Provider`); provider-specific detail is
preserved via `json.RawMessage` extension points and a native client.

```
                       ┌───────────────────────────────────────────┐
   instruction ─────▶  │              internal/agent                │
   (stdin/arg/file)    │  loop · limits · stream accumulator · IDs  │
                       └───────┬───────────────────────────┬────────┘
                               │ model.Provider            │ ToolRunner
                     ┌─────────▼─────────┐        ┌─────────▼──────────┐
                     │ internal/provider │        │  internal/tools    │
                     │  anthropic /      │        │  registry · policy │
                     │  openai / gemini  │        │  process · fs ·    │
                     │  (+ fake)         │        │  shell (gated)     │
                     └─────────┬─────────┘        └─────────┬──────────┘
                               │ internal/transport         │ exec (argv, no shell)
                               │  instrumented http ·        │  OS process-group kill
                               │  retry · SSE · auth-inject  │
                               └─────────┬──────────────────┘
                                         │ every request/response/tool call
                               ┌─────────▼──────────┐
                               │  internal/logging  │
                               │ events.jsonl ·     │
                               │ turns.jsonl ·      │
                               │ summary.json ·     │
                               │ hash chain · redact│
                               └────────────────────┘
```

### Packages

| Package | Responsibility |
|---|---|
| `internal/model` | Provider-agnostic types: messages, content parts, requests/responses, streaming events, tools, the `Provider` and `NativeClient` interfaces, normalized `ProviderError`. Depends only on the stdlib. |
| `internal/buildinfo` | Compile-time build metadata (provider, model, version, commit, OS/arch) embedded via `-ldflags -X`. |
| `internal/config` | Runtime configuration with precedence (compiled invariants > flags > file > env > defaults) and a dev-only model-override guard. |
| `internal/transport` | Instrumented, retrying `http` client; SSE reader tolerant of arbitrary chunk boundaries; **mandatory auth injection** (callers can never set credential headers); records timings, request IDs, and rate-limit headers with secrets redacted. |
| `internal/logging` | Append-only JSONL writers (event envelopes, turn-pair records, run summary), size caps + SHA-256, optional per-event **hash chain**, header/secret redaction, and a crash-tolerant scanner. |
| `internal/agent` | The provider-agnostic loop, run **limits**, the streaming **accumulator** that rebuilds a canonical response, and error → exit-code mapping. |
| `internal/tools` | Tool registry (implements the loop's `ToolRunner`), execution **policy** (approved roots, shell gate, output caps), and the `process`, `filesystem`, and `shell` tools with OS-specific process-group termination. |
| `internal/provider/*` | One adapter per provider family. Each has `client.go`, `translate.go`, `streaming.go`, `native.go`, and wire types. |
| `cmd/agent` | CLI, config wiring, and the build-tagged `compiled_provider_*.go` selectors + guards. |

### The agent loop

1. Open a session, write `session_start`.
2. Read one instruction (stdin, CLI arg, or `--input file`).
3. Build a normalized request; write the outbound request to the turn log.
4. Call the provider (streaming or not); persist every stream event and
   accumulate a canonical response.
5. If the model requested tools: validate, apply policy, execute with timeout and
   cancellation, record the full execution, feed results back, and continue the
   same logical turn.
6. When no more tools are requested, write the completed turn pair.
7. Stop on success, cancellation, or any configured limit (max model calls, max
   tool calls, wall-clock, cumulative bytes, or token budget).

---

## Supported model providers

Each is a separate build selected by a `-tags provider_*` build tag.

| Provider | Build tag | Primary API | Status |
|---|---|---|---|
| **Anthropic** | `provider_anthropic` | Messages API — SSE content blocks, tool-use blocks, thinking blocks, token counting | ✅ implemented |
| **OpenAI** | `provider_openai` | Responses API — streaming events, tool calls, multiple output items | 🚧 in progress |
| **Gemini** | `provider_gemini` | `generateContent` + streaming, parts/candidates, grounding metadata | 🚧 in progress |
| **Fake** | `provider_fake` | Deterministic offline provider for local/dev/testing (no key, no network) | ✅ implemented |
| **Router** | `provider_router` | Pluggable gateway (OpenAI/Anthropic/Gemini-compatible or custom) | ⏳ planned |

Every real provider build exposes:

- **Common tier** (where the provider supports it): text, streaming + event
  accumulation, system instructions, multi-turn history, tool declarations +
  results, parallel tool calls, structured output, multimodal input, usage/token
  data, reasoning/thinking, request IDs, errors + retry hints, cancellation.
- **Native tier**: a generic `Do` / `Stream` client that reaches any endpoint the
  API key can access, with the transport injecting authentication.

---

## Feature summary

- **Core interfaces** (`internal/model`), build metadata, and layered
  configuration with a model-override guard.
- **JSONL audit logging**: stable event envelopes, turn-pair records, optional
  hash chain, secret/redaction handling, and a crash-recovery scanner that
  tolerates a truncated final line.
- **Instrumented retrying transport** with an SSE reader and mandatory auth
  injection — transport credentials are never persisted.
- **Agent loop** with run limits, a streaming accumulator, and typed
  error → exit-code mapping.
- **Tool runtime**: registry, `process` (argv, **no shell** by default),
  `filesystem` (scoped to approved roots), and a policy-gated `shell` tool, with
  OS-specific process-group termination on Unix and Windows.
- **Build-tag guards**: builds with no provider tag *or* more than one provider
  tag fail to compile with a clear message.
- **Anthropic adapter**: non-streaming + streaming Messages, tool calls, thinking,
  token counting, and the native escape hatch.
- **Tests** covering SSE chunk boundaries, retry classification/backoff, header +
  literal redaction, the hash chain, crash recovery, the agent loop
  (text / tool-loop / streaming / limits), and the stream accumulator.

---

## CLI

```
agent run [flags] [instruction]     run one instruction (stdin/--input if omitted)
agent doctor                        print effective config, secrets redacted
agent build-info                    print build metadata
agent validate-config [flags]       validate configuration and exit
agent replay-log <session-dir>      validate + summarize a session log (no re-execution)
```

Common `run` flags: `--stream`, `--input <file>`, `--config <file>`,
`--log-dir <dir>`, `--strict-logging`, `--allow-root <dir>` (repeatable),
`--enable-shell`.

Exit codes: `0` success, `2` bad config, `3` provider/auth, `4` provider
temporary, `5` tool failure, `6` policy denied, `7` timeout/cancel, `8` logging
failure, `10` internal.

---

## Logging & audit

Each run writes to `logs/<date>/<session_id>/`:

- `events.jsonl` — every transport event, retry, tool event, and lifecycle event.
- `turns.jsonl` — completed turn pairs: outbound input, all provider attempts,
  tool calls/results, and final output.
- `summary.json` — final status, totals, build metadata, usage, and timing.
- `artifacts/` — reserved for captured artifacts.

Files are created with restrictive permissions and flushed after each write; an
optional `fsync` mode and per-event hash chain support audit-sensitive runs.
Transport secrets (Authorization, API keys, cookies) are **always** redacted;
raw model content is logged intentionally because auditability is a product goal.

---

## Building for release

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -tags provider_anthropic \
  -ldflags "-s -w \
    -X github.com/ahhh/fury_agent/internal/buildinfo.Provider=anthropic \
    -X github.com/ahhh/fury_agent/internal/buildinfo.Model=$MODEL \
    -X github.com/ahhh/fury_agent/internal/buildinfo.Commit=$(git rev-parse --short HEAD)" \
  -o dist/agent-anthropic-linux-amd64 ./cmd/agent
```

Targets: Linux/macOS/Windows on amd64/arm64. A development build may add
`-tags allow_model_override` to permit an allow-listed model override; production
builds omit it and stay bound to the compiled model.

---

## Testing

```bash
go test -tags provider_fake ./...     # unit + contract tests (a provider tag is required)
go vet  -tags provider_fake ./...
```

A provider tag is required because the build-tag guard intentionally fails a
no-provider build.

---

## Status

Foundation and the Anthropic adapter are complete. OpenAI and Gemini adapters,
their contract-test suites, and the router build are in progress. See
`GO_AGENT_PLAN.md` for the full design and milestone plan.

## License

MIT — see [LICENSE](LICENSE).
