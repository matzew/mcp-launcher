# 🚀 MCP Launcher

A web UI for browsing, configuring, and deploying [MCP](https://modelcontextprotocol.io/) servers on Kubernetes / OpenShift.

## ✨ Features

- 📋 **Catalog** — Browse MCP servers from labeled ConfigMaps using the [MCP Registry](https://github.com/modelcontextprotocol/registry) `server.json` standard
- ⚙️ **Configure** — Fill in environment variables, arguments, file mounts, and ServiceAccount via web form
- 👁️ **Live YAML Preview** — See the `MCPServer` CR update in real-time as you type (htmx)
- ▶️ **Deploy** — Create `MCPServer` custom resources, Secrets, and ConfigMaps with one click
- 🗑️ **Cleanup** — Delete running servers and all managed artifacts

## 📦 Catalog Format

Catalog entries are Kubernetes ConfigMaps with the label `mcp.x-k8s.io/catalog-entry=true`, each containing a `server.json` aligned with the MCP Registry standard:

```json
{
  "name": "io.example/my-mcp-server",
  "title": "My MCP Server",
  "description": "Does useful things via MCP",
  "version": "1.0.0",
  "packages": [{
    "registryType": "oci",
    "identifier": "quay.io/example/my-mcp-server:latest",
    "transport": { "type": "streamable-http" },
    "environmentVariables": [
      { "name": "API_KEY", "isSecret": true, "isRequired": true }
    ]
  }],
  "_meta": {
    "io.openshift/k8s": {
      "defaultPort": 8080
    }
  }
}
```

Kubernetes-specific extensions live under `_meta` → `io.openshift/k8s` per the standard's publisher metadata mechanism.

## 🛠️ Quick Start

### Prerequisites

- Kubernetes / OpenShift cluster with the [MCP Lifecycle Operator](https://github.com/kubernetes-sigs/mcp-lifecycle-operator) installed
- `kubectl` / `oc` configured

### Deploy

```bash
# Create namespaces
kubectl create namespace mcp-catalog
kubectl create namespace mcp-system

# Apply catalog entries, RBAC, and deployment
make catalog
make deploy
```

### Access the UI

```bash
kubectl -n mcp-system port-forward svc/mcp-launcher 8080:8080
```

Then open [http://localhost:8080](http://localhost:8080).

## 🏗️ Development

```bash
make build          # Build binary to bin/mcp-launcher
make run            # Build and run locally (uses kubeconfig)
make fmt            # Format code
make vet            # Vet code
make test           # Run tests
```

### Container Image

```bash
make docker-build   # Build with podman (ubi9/go-toolset + ubi9-micro)
make docker-push    # Push to quay.io/matzew/mcp-launcher:latest
```

## 📁 Project Structure

```
├── main.go                    # HTTP server, routes, kubeconfig
├── catalog/
│   ├── types.go               # MCP Registry-aligned structs
│   └── catalog.go             # ConfigMap-backed catalog store
├── handlers/
│   └── handlers.go            # HTTP handlers (catalog, configure, run, delete)
├── templates/                 # Go HTML templates (htmx)
├── deploy/
│   ├── catalog/               # Sample catalog ConfigMaps (5 servers)
│   ├── deployment.yaml        # Deployment + Service
│   └── rbac/                  # ServiceAccount, ClusterRole, ClusterRoleBinding
├── Dockerfile                 # Multi-stage: ubi9/go-toolset → ubi9-micro
└── Makefile
```

## 📄 License

Apache License 2.0 — see [LICENSE](LICENSE).
