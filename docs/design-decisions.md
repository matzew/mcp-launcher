# Design Decisions

## Why stdlib net/http (no framework)

The application has a small number of routes (7) with straightforward
request handling. Go 1.22+ `http.ServeMux` supports method-based routing
and path parameters (`{name}`, `{namespace}`), eliminating the need for
third-party routers. Fewer dependencies means fewer CVEs to track and
simpler upgrades.

## Why dynamic client for MCPServer

MCPServer is a CRD defined by an external operator. Using
`k8s.io/client-go/dynamic` avoids generating typed clients or importing
the operator's Go module. The launcher only needs Create, List, and Delete
operations on unstructured objects, which the dynamic client handles well.
This also keeps the go.mod dependency list small.

## Why ConfigMap-backed catalog

ConfigMaps are the simplest Kubernetes-native storage mechanism that
requires no additional infrastructure. Catalog entries are small JSON
documents (< 4KB each). The label selector
`mcp.x-k8s.io/catalog-entry=true` provides efficient filtering. Catalog
entries can be managed with `kubectl apply` and version-controlled
alongside the deployment manifests.

## Why ownerReferences for resource cleanup

Setting ownerReferences on Secrets and ConfigMaps to point at the parent
MCPServer CR means Kubernetes garbage collection handles cleanup
automatically when the CR is deleted. This is more reliable than manual
cleanup and follows standard Kubernetes controller patterns. The explicit
cleanup in `Delete()` is a safety net for edge cases.

## Why UBI9 (Red Hat Universal Base Image)

The target platform is OpenShift, which requires certified base images for
production use. UBI9-micro provides a minimal footprint (~30MB) while
meeting Red Hat certification requirements. The multi-stage build uses
ubi9/go-toolset as the builder and ubi9-micro as the runtime image.

## Why no cmd/ directory

mcp-launcher is a single binary with a single entry point. The standard Go
project layout recommends `cmd/` for projects with multiple binaries. With
only one binary and ~75 lines in main.go, a flat layout keeps the project
simple. The binary name is set in the Makefile (`bin/mcp-launcher`).

## Why htmx instead of a JavaScript framework

htmx provides dynamic UI updates (live YAML preview, polling for server
status, inline delete) with HTML attributes instead of JavaScript code.
This keeps the frontend in Go templates without a build step, bundler, or
node_modules. The htmx script is loaded from CDN (~14KB gzipped).
