# MCP Launcher - Getting Started

A web UI for browsing a catalog of MCP servers, configuring them, and deploying them as MCPServer custom resources on Kubernetes.

## Prerequisites

- Go 1.22+
- A running Kubernetes/OpenShift cluster (accessible via `~/.kube/config`)
- The [mcp-lifecycle-operator](https://github.com/kubernetes-sigs/mcp-lifecycle-operator) installed on the cluster (provides the MCPServer CRD and controller)

## Quick Start (local)

```bash
cd /home/matzew/Work/MCP_UI/mcp-launcher

# 1. Build the binary
make build

# 2. Create the mcp-catalog namespace and apply catalog ConfigMaps
make catalog

# 3. Start the UI on :8080
make run
```

Open http://localhost:8080 in your browser.

## What Each Step Does

| Command        | Description                                                                 |
|----------------|-----------------------------------------------------------------------------|
| `make build`   | Compiles `bin/mcp-launcher`                                                 |
| `make catalog` | Creates `mcp-catalog` namespace and applies catalog ConfigMaps from `deploy/catalog/` |
| `make run`     | Runs the binary with `CATALOG_NAMESPACE=mcp-catalog TARGET_NAMESPACE=default` |

## Environment Variables

| Variable            | Default       | Description                              |
|---------------------|---------------|------------------------------------------|
| `CATALOG_NAMESPACE` | `mcp-catalog` | Namespace where catalog ConfigMaps live  |
| `TARGET_NAMESPACE`  | `default`     | Namespace where MCPServer CRs are created |
| `LISTEN_ADDR`       | `:8080`       | HTTP listen address                      |

## Catalog

The catalog is a set of ConfigMaps in the `mcp-catalog` namespace, one per MCP server. Each ConfigMap has the label `mcp.x-k8s.io/catalog-entry: "true"` and contains a `server.json` key with the server metadata.

Current catalog entries (in `deploy/catalog/`):

- **everything-mcp** - Reference test server, no credentials needed
- **kubernetes-mcp** - Kubernetes cluster interaction, needs ServiceAccount + config
- **satellite-mcp** - Red Hat Satellite integration, needs CA cert + URL
- **ansible-mcp** - Ansible Automation Platform, needs URL + OAuth token
- **insights-mcp** - Red Hat Insights/Lightspeed, needs client ID + secret

To add a new entry, create a ConfigMap YAML in `deploy/catalog/` and run `make catalog`.

## Container Build and In-Cluster Deployment

```bash
# Build and push container image with ko
make ko-build

# Or with podman
make docker-build docker-push

# Deploy to cluster (RBAC + Deployment + Service)
make deploy

# Remove from cluster
make undeploy
```

## All Make Targets

```
make help
```
