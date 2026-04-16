# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

### Go Backend
```bash
go build ./cmd/control-plane/   # Builds sniffit-control binary
go build ./cmd/sniffit/          # Builds sniffit-probe binary (requires libpcap)
go build ./cmd/test-trigger/     # Builds test traffic simulator
```

### UI (React + TypeScript + Vite)
```bash
cd ui
bun install              # Install dependencies
bun run dev              # Start Vite dev server (proxies /api to :8181)
bun run build            # Production build
bun test                 # Run vitest tests
```

### Docker
```bash
docker build -f Dockerfile.control-plane -t sniffit-control .
docker build -f Dockerfile.probe -t sniffit-probe .
```

## Project Architecture

SniffIt is a RabbitMQ/AMQP observability platform with two main components:

### Probe (`cmd/sniffit/`)
High-performance packet sniffer using `google/gopacket` with `AF_PACKET` for raw socket capture and `tcpassembly` for TCP stream reassembly. Parses AMQP 0.9.1 frames and evaluates rules defined in `internal/rules/`. Sends alerts to the control plane.

### Control Plane (`cmd/control-plane/`)
HTTP server (port 8080) that:
- Receives alerts from probes via HTTP POST
- Streams real-time alerts to UI via SSE (`/api/events`)
- Stores topology state (queues, exchanges, consumers) in-memory
- Forwards alerts to AI agent (`internal/ai/`) for diagnostic analysis
- Serves the React UI built into the binary

### AI Agent (`internal/ai/agent.go`)
Diagnostic pipeline using OpenRouter API (Qwen/Claude/GPT models) with Ollama fallback. Correlates AMQP/HTTP signals with pod metrics for failure explanations.

### Kubernetes Sidecar Injection
The webhook (`webhook/mutator.go`) implements a `MutatingWebhookConfiguration` that injects the `sniffit-sidecar` container into pods labeled `sniffit/inject=true`. The sidecar runs with `NET_RAW` and `NET_ADMIN` capabilities for packet capture.

## Entry Points

| Binary | Purpose | Key Flags |
|--------|---------|-----------|
| `cmd/control-plane/main.go` | Control plane server | `--addr` (default `:8080`) |
| `cmd/sniffit/main.go` | Packet sniffer | `--iface`, `--port`, `--name` |
| `cmd/test-trigger/main.go` | Traffic simulator for testing | `--host`, `--port` |

## Key Internal Packages

- `internal/amqp/` - AMQP 0.9.1 frame decoder (Connection, Channel, Basic frames)
- `internal/capture/` - Packet capture using gopacket with TCP stream reassembly
- `internal/rules/` - Rule evaluation engine; rules evaluate AMQP frames and HTTP responses, emit alerts at info/warn/error severity
- `internal/alerting/` - Alert dispatcher (sends alerts to control plane)
- `internal/controlplane/server.go` - HTTP server with SSE streaming and webhook receiver
- `internal/correlator/` - Pod metrics correlation with AMQP signals

## Configuration

- `.env` - API keys for OpenRouter (`OPENROUTER_API_KEY`, `OPENROUTER_MODEL`) and Ollama (`OLLAMA_URL`, `OLLAMA_MODEL`)
- `charts/sniffit/values.yaml` - Helm chart defaults (image repos, resources, `externalMonitors` for non-K8s RabbitMQ)
- `ui/vite.config.ts` - Vite dev server proxies `/api` to `localhost:8181`

## Running Tests

```bash
# Go tests
go test ./...

# UI tests (from ui/ directory)
bun test
```
