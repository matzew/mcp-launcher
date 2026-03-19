# Code Conventions

## Error Handling

- Wrap errors with context: `fmt.Errorf("listing catalog configmaps: %w", err)`.
- HTTP handlers return appropriate status codes (400, 404, 500) with
  descriptive messages.
- Non-critical errors (e.g., cleanup during delete) are silently ignored
  since ownerReferences handle the common case.

## Constructor Injection

- All dependencies are passed through `New()` constructors.
- `catalog.NewStore(client, namespace)` — takes a `kubernetes.Interface`.
- `handlers.New(store, clientset, dynClient, namespace)` — takes all deps.
- This enables testing with fake clients without global state.

## Template Loading

- Always use `pageTemplate(files...)` — never call `template.ParseFiles`
  directly in handlers.
- Layout is always the first file parsed; page templates define a `content`
  block.
- Partials (e.g., `running-list.html`) can be parsed standalone for htmx
  partial responses.

## Owner References

- MCPServer CR is created first to obtain its UID.
- Owned Secrets and ConfigMaps set `ownerReferences` pointing to the CR.
- Kubernetes garbage collection deletes owned resources when the CR is removed.
- `Delete()` handler also does explicit cleanup as a safety net.

## Labels

- Catalog ConfigMaps: `mcp.x-k8s.io/catalog-entry=true`
- Managed resources: `app.kubernetes.io/managed-by=mcp-launcher`

## Form Field Naming

- `catalog-name` — server name from catalog
- `instance-name` — Kubernetes resource name
- `namespace` — target namespace
- `env-{VAR_NAME}` — environment variable values
- `arg-{NAME}` — package argument values
- `file-{KEY}` — secret file mount content
- `configmap-content` — inline ConfigMap content
- `configmap-ref` — existing ConfigMap reference

## sanitizeKey Rules

- Replaces underscores with dashes: `_` → `-`
- Converts to lowercase: `UPPER` → `upper`
- Used for Kubernetes Secret keys derived from environment variable names.
