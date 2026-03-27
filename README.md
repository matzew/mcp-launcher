# 🚀 MCP Launcher

A web UI for browsing, configuring, and deploying [MCP](https://modelcontextprotocol.io/) servers on Kubernetes / OpenShift.

## ✨ Features

- 📋 **Catalog** — Browse MCP servers from labeled ConfigMaps using the [MCP Registry](https://github.com/modelcontextprotocol/registry) `server.json` standard
- ⚙️ **Configure** — Fill in environment variables, arguments, file mounts, and ServiceAccount via web form
- 👁️ **Live YAML Preview** — See the `MCPServer` CR update in real-time as you type (htmx)
- ▶️ **Deploy** — Create `MCPServer` custom resources, Secrets, and ConfigMaps with one click
- 🚀 **One-Click Deploy** — Catalog entries with a `crTemplate` get a "Deploy" button directly on the card, skipping the configuration form
- 🔒 **Root Override** — UI checkbox to set `runAsNonRoot: false` for images that require root (e.g. Insights, Satellite)
- 🌐 **Gateway Integration** — Automatically creates `HTTPRoute` resources for deployed MCPServer instances via a configurable Gateway API gateway
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
      "defaultPort": 8080,
      "runAsRoot": false,
      "needsServiceAccount": true,
      "serviceAccountHint": "Needs a ServiceAccount bound to the 'view' ClusterRole",
      "configMaps": [{
        "label": "Server Config",
        "defaultContent": "key = \"value\"\n",
        "fileName": "config.toml",
        "mountPath": "/etc/mcp-config"
      }],
      "crTemplate": {
        "source": {
          "type": "ContainerImage",
          "containerImage": { "ref": "quay.io/example/my-mcp-server:latest" }
        },
        "config": { "port": 8080, "path": "/mcp" }
      }
    }
  }
}
```

When `crTemplate` is present, the catalog card shows a one-click "Deploy" button that creates the `MCPServer` CR directly, skipping the configuration form.

Kubernetes-specific extensions live under `_meta` → `io.openshift/k8s` per the standard's publisher metadata mechanism.

## 🛠️ Quick Start

### Prerequisites

- Kubernetes / OpenShift cluster with the [MCP Lifecycle Operator](https://github.com/kubernetes-sigs/mcp-lifecycle-operator) installed
- `kubectl` / `oc` configured

### Deploy

Single-file install (namespaces, RBAC, deployment, service, and sample catalog):

```bash
kubectl apply -f dist/mcp-launcher.yaml
```

Or step-by-step:

```bash
make catalog    # Create mcp-catalog namespace + sample ConfigMaps
make deploy     # Create mcp-system namespace + RBAC + Deployment + Service
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
make image-build    # Build container image with podman (ubi9/go-toolset + ubi9-micro)
make image-push     # Push to quay.io/matzew/mcp-launcher:latest
make image          # Build and push in one step
```

### Cluster Operations

```bash
make install        # Apply dist/mcp-launcher.yaml (single-file install)
make uninstall      # Remove everything installed by dist/mcp-launcher.yaml
make deploy         # Deploy RBAC, launcher, and catalog to cluster
make undeploy       # Remove launcher and catalog from cluster
make rollout        # Restart the launcher deployment and wait for readiness
make release        # Build, push, deploy, and rollout (full release cycle)
make dist           # Regenerate dist/mcp-launcher.yaml from deploy/ manifests
```

## 📁 Project Structure

```
├── main.go                        # HTTP server, routes, kubeconfig
├── catalog/
│   ├── types.go                   # MCP Registry-aligned structs
│   ├── types_test.go
│   ├── catalog.go                 # ConfigMap-backed catalog store
│   └── catalog_test.go
├── handlers/
│   ├── handlers.go                # HTTP handlers (catalog, configure, deploy, delete, gateway)
│   └── handlers_test.go
├── templates/                     # Go HTML templates (htmx)
│   └── partials/                  # htmx partial fragments
├── deploy/
│   ├── catalog/                   # Sample catalog ConfigMaps (5 servers)
│   ├── deployment.yaml            # Deployment + Service
│   └── rbac/                      # ServiceAccount, ClusterRole, ClusterRoleBinding
├── dist/
│   └── mcp-launcher.yaml          # Single-file install (all resources)
├── docs/                          # Architecture and design documentation
├── Dockerfile                     # Multi-stage: ubi9/go-toolset → ubi9-micro
└── Makefile
```

## 📄 License

Apache License 2.0 — see [LICENSE](LICENSE).
