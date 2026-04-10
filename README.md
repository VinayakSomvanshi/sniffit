# SniffIt: Professional RabbitMQ Observability Platform

SniffIt is a powerful, low-overhead observability operator for RabbitMQ and AMQP 0.9.1 environments. It combines deep protocol sniffing with AI-driven diagnostics to help you transition from "something is wrong" to "here is exactly why" in seconds.

![SniffIt Dashboard](https://raw.githubusercontent.com/vinayak/sniffit/main/assets/dashboard_preview.png)

## 🌟 Key Features

- **Zero-Code Instrumentation**: Automatically injects AMQP sniffers into your Kubernetes pods via a sidecar pattern. No application code changes required.
- **AI-Driven Diagnostics**: Real-time failure analysis using OpenRouter-integrated LLMs (Qwen/Claude/GPT).
- **Multi-Protocol Monitoring**: Simultaneously sniffs AMQP (5672) and Management HTTP (15672) to catch both message bus failures and administrative security breaches.
- **Handshake Monitoring**: Detects server-side timeout overrides and configuration drifts (e.g., heartbeat clamping) that are usually hidden in internal logs.
- **Protocol Insights**: Deep inspection of AMQP frames (Connection, Channel, Basic) to catch authentication failures, topology mismatches, and consumer timeouts.
- **Unified Dashboard**: A high-performance, Indigo-themed React dashboard for real-time telemetry and rule management.
- **Hybrid Support**: Monitor both Kubernetes-native sidecars and external RabbitMQ brokers on standalone VMs or bare-metal.

## 🚀 Quick Start (Local Testing)

### 1. Start the Control Plane
```bash
export OPENROUTER_API_KEY="your-key"
go run cmd/control-plane/main.go --addr=":8080"
```

### 2. Run the External Probe
```bash
# Sniff on loopback for a local RabbitMQ instance
sudo ./sniffit-probe --iface lo --port 5672 --name local-rabbit
```

Visit **http://localhost:8080** to view the live dashboard.

## ☸️ Kubernetes Deployment

Deploy the SniffIt operator using the official Helm chart:

```bash
helm install sniffit ./charts/sniffit \
  --namespace sniffit-system --create-namespace \
  --set config.openRouterKey="YOUR_API_KEY"
```

To enable sidecar injection for a namespace:
```bash
kubectl label namespace your-app-ns sniffit/inject=true
```

### Monitoring External Brokers (Managed/VM)
To monitor an external RabbitMQ instance (e.g., CloudAMQP or a standalone cluster), add it to your `values.yaml`:

```yaml
externalMonitors:
  - name: "prod-cluster-01"
    host: "10.0.0.5"
    port: 5672
```
SniffIt will automatically deploy a dedicated sniffer pod to track this host.

## 🛠️ Architecture

- **Probe**: A high-performance Go sniffer using `gopacket` and `tcpassembly` to reconstruct AMQP streams from the wire.
- **Control Plane**: A specialized ingestion engine that deduplicates alerts, stores history in-memory, and provides a real-time SSE stream to the UI.
- **AI Agent**: Asynchronous diagnostic pipeline that correlates network signals with pod metrics to provide deep English explanations of failures.

## 📄 License
MIT License.
