# AGENTS.md — mcp-launcher

Agent instructions for working with this codebase.

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Build & Run](#build--run)
- [Testing](#testing)
- [Code Conventions](#code-conventions)
- [Dependencies](#dependencies)
- [Deployment](#deployment)

## Overview

mcp-launcher is a web UI for browsing, configuring, and deploying MCP
(Model Context Protocol) servers on Kubernetes/OpenShift. Users browse a
ConfigMap-backed catalog, fill in a configuration form, and the launcher
creates MCPServer custom resources with owned Secrets and ConfigMaps.

## Architecture

| Package    | Responsibility                                    |
|------------|---------------------------------------------------|
| `main`     | HTTP server, route wiring, kubeconfig builder     |
| `catalog`  | ConfigMap-backed catalog store, MCP Registry types|
| `handlers` | HTTP handlers, YAML preview, resource creation    |
| `templates`| Go HTML templates with htmx integration           |

Data flow: browser → handlers → catalog.Store → k8s API (ConfigMaps) for
reads; browser → handlers → dynamic client → MCPServer CRD for writes.

See `docs/architecture.md` for the full system context diagram.

## Build & Run

```bash
make build          # Compile to bin/mcp-launcher
make run            # Build + run locally (uses kubeconfig)
make test           # fmt + vet + go test ./...
make fmt            # go fmt ./...
make vet            # go vet ./...
make image          # Build + push container image (podman)
make deploy         # Apply RBAC + deployment + catalog to cluster
make release        # image + deploy + rollout (full cycle)
make dist           # Regenerate dist/mcp-launcher.yaml
make install        # Apply single-file distribution
```

Environment variables: `CATALOG_NAMESPACE` (default: mcp-catalog),
`TARGET_NAMESPACE` (default: default), `LISTEN_ADDR` (default: :8080).

## Testing

- Use `k8s.io/client-go/kubernetes/fake` for core API tests.
- Use `k8s.io/client-go/dynamic/fake` for MCPServer CRD tests.
- Table-driven tests with descriptive subtest names.
- Run: `go test ./... -v -race -count=1`
- Template tests use `"../templates"` as the template directory.
- Helper functions (parsePort, sanitizeKey, buildYAMLPreview) are tested
  directly without HTTP infrastructure.

## Code Conventions

- **Constructor injection**: all dependencies passed via `New()` constructors.
- **Error wrapping**: use `fmt.Errorf("context: %w", err)`.
- **No CGO**: builds use `CGO_ENABLED=0` for static binaries.
- **Template loading**: always via `pageTemplate()` — never inline parsing.
- **Owner references**: Secrets/ConfigMaps set ownerReferences to the parent
  MCPServer CR so Kubernetes GC handles cleanup.
- **Labels**: managed resources use `app.kubernetes.io/managed-by=mcp-launcher`.
- **Form fields**: prefixed by type (`env-`, `arg-`, `file-`, `configmap-`).
- **sanitizeKey**: converts `UPPER_CASE` to `lower-case` for k8s key names.
- **No cmd/ directory**: single binary, main.go at repo root.

## Dependencies

| Dependency         | Version  | Purpose                         |
|--------------------|----------|---------------------------------|
| k8s.io/api         | v0.35.2  | Kubernetes API types            |
| k8s.io/apimachinery| v0.35.2  | API machinery (metav1, runtime) |
| k8s.io/client-go   | v0.35.2  | Kubernetes client library       |

CRD: `MCPServer` in group `mcp.x-k8s.io`, version `v1alpha1`,
resource `mcpservers`. Managed by an external operator.

## Deployment

- **Base image**: UBI9-micro (ubi9-micro:latest)
- **Builder image**: ubi9/go-toolset
- **Registry**: quay.io/matzew/mcp-launcher
- **Target platform**: OpenShift (works on vanilla Kubernetes too)
- **Namespace**: mcp-system (launcher), mcp-catalog (catalog ConfigMaps)
- **Container tool**: podman (set via `CONTAINER_TOOL` in Makefile)
- **Security**: runs as non-root (UID 65532), read-only root filesystem

See `docs/` for deeper architectural and design context.
