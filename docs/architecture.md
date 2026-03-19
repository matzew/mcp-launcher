# Architecture

## System Context

```
┌──────────┐     HTTP      ┌──────────────┐    k8s API    ┌────────────┐
│ Browser  │──────────────→│ mcp-launcher │──────────────→│ API Server │
│ (htmx)   │←──────────────│   :8080      │←──────────────│            │
└──────────┘   HTML/YAML   └──────────────┘               └─────┬──────┘
                                                                │
                                                    ┌───────────┴──────────┐
                                                    │                      │
                                              ┌─────┴─────┐        ┌──────┴──────┐
                                              │ ConfigMaps │        │  MCPServer  │
                                              │ (catalog)  │        │  CRD + CR   │
                                              └───────────┘        └──────┬──────┘
                                                                          │
                                                                   ┌──────┴──────┐
                                                                   │  MCP Server │
                                                                   │  Operator   │
                                                                   └─────────────┘
```

## Package Responsibilities

### `main` (main.go)

- Builds Kubernetes client config (in-cluster or kubeconfig fallback).
- Reads environment variables for namespaces and listen address.
- Wires routes to handler methods on `http.ServeMux` (Go 1.22+ patterns).
- Starts HTTP server.

### `catalog` (catalog/)

- `Store`: reads ConfigMaps labeled `mcp.x-k8s.io/catalog-entry=true`
  from the catalog namespace.
- `ServerEntry` and related types: MCP Registry-aligned data model parsed
  from the `server.json` key in each ConfigMap.
- Helper methods: `K8s()`, `IsOneClick()`, `PrimaryPackage()`, `DisplayTitle()`.

### `handlers` (handlers/)

- `Handler` struct: holds catalog store, core + dynamic Kubernetes clients,
  target namespace, and template directory.
- Route handlers: Catalog, Configure, Preview, Run, QuickDeploy, Running, Delete.
- Resource creation: MCPServer CR via dynamic client, then owned Secrets and
  ConfigMaps via core client with ownerReferences set to the CR.
- YAML preview builder: constructs MCPServer YAML from HTML form values.
- Template rendering: `pageTemplate()` composes layout.html with page templates.

### `templates` (templates/)

- `layout.html`: base layout with CSS, navbar, theme toggle, htmx script.
- `catalog.html`: server card grid with capability badges.
- `configure.html`: two-column form + live YAML preview (htmx-driven).
- `running.html` + `partials/running-list.html`: running instances table
  with 5-second htmx polling and delete actions.

## Data Flow

### Browse Catalog

1. `GET /` → `Catalog()` → `catalog.Store.List()` → list labeled ConfigMaps
2. Parse `server.json` from each ConfigMap → `[]ServerEntry`
3. Render `catalog.html` with server cards

### Configure & Deploy

1. `GET /configure/{name}` → load entry + list ServiceAccounts → render form
2. User fills form → htmx sends `GET /preview/{name}` with form data
3. `buildYAMLPreview()` constructs YAML string → returned as HTML partial
4. `POST /run` → parse form → create MCPServer CR → create owned Secrets/ConfigMaps
5. Redirect to `/running`

### One-Click Deploy

1. `POST /deploy/{name}` → load entry → verify `IsOneClick()`
2. Copy `crTemplate` to spec → create MCPServer CR
3. Create owned ConfigMaps from `defaultContent` if present
4. Redirect to `/running`

### Monitor & Delete

1. `GET /running` → list MCPServer CRs across all namespaces
2. htmx polls every 5s for status updates (HX-Request partial response)
3. `DELETE /server/{ns}/{name}` → cleanup managed Secrets/ConfigMaps → delete CR
