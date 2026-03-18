# MCP Launcher: Registry Standard Alignment

**Date:** 2026-03-12

## Summary

Aligned the MCP Launcher catalog format with the official MCP Registry `server.json` standard.
Kubernetes-specific deployment extensions are isolated under `_meta` using the standard's
publisher metadata extension point with reverse-DNS namespaced key `io.openshift/k8s`.

## What Changed

### Old Format (custom)

- Flat `ServerEntry` with `image`, `defaultPort`, `transport` (bare string), `credentials[]` array
- `Credential` struct with type discriminator (`env` / `file` / `arg`)
- Fields: `envName`, `sensitive`, `required`, `secretKey`, `mountPath`, `flag`

### New Format (MCP Registry standard + `_meta` extension)

- `ServerEntry` with `name`, `title`, `description`, `version`, `packages[]`
- `Package` with `registryType`, `identifier`, `transport{type,url}`, `environmentVariables[]`, `packageArguments[]`
- `EnvironmentVariable` with `name`, `isSecret`, `isRequired`, `placeholder`, `default`, `choices`
- `PackageArgument` with `type` (named/positional), `name`, `value`, `isRequired`
- `_meta.io.openshift/k8s` for: `defaultPort`, `needsServiceAccount`, `serviceAccountHint`, `configMaps[]`, `secretMounts[]`

### Comparison with Kubeflow Model Registry

The Kubeflow Model Registry (`kubeflow/model-registry`) uses a separate catalog format focused on
discovery (logos, tools metadata, licenses, provider info). Our format is focused on deployment
(what env vars to configure, what image to run, what port). They are complementary layers:

| Aspect               | MCP Registry standard (ours)         | Kubeflow Model Registry          |
|-----------------------|--------------------------------------|----------------------------------|
| Purpose               | Deployment descriptor                | Discovery catalog                |
| Image ref             | `packages[].identifier`              | `artifacts[].uri`                |
| Transport             | `packages[].transport: {type}`       | `transports: ["http"]`           |
| Config inputs         | `environmentVariables[]`, `packageArguments[]` | Not present              |
| Tool metadata         | Not present                          | `tools[]` with parameters        |
| Rich metadata         | `name`, `title`, `description`       | + `provider`, `license`, `logo`, `readme` |
| Extensions            | `_meta` (namespaced)                 | `customProperties` (typed)       |

---

## Files Modified

### catalog/types.go

```go
package catalog

// ServerEntry represents an MCP server in the catalog, aligned with the MCP Registry standard.
type ServerEntry struct {
	Name        string    `json:"name"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description"`
	Version     string    `json:"version,omitempty"`
	Packages    []Package `json:"packages,omitempty"`

	// Publisher metadata (MCP Registry standard extension point).
	Meta *Meta `json:"_meta,omitempty"`
}

// Meta holds publisher-provided metadata per the MCP Registry _meta extension point.
type Meta struct {
	K8s *KubernetesExtensions `json:"io.openshift/k8s,omitempty"`
}

// K8s returns the Kubernetes extensions, or nil if not set.
func (e *ServerEntry) K8s() *KubernetesExtensions {
	if e.Meta == nil {
		return nil
	}
	return e.Meta.K8s
}

// Package describes a distributable unit of the server (OCI image, npm package, etc.).
type Package struct {
	RegistryType         string                `json:"registryType"`
	Identifier           string                `json:"identifier"`
	Version              string                `json:"version,omitempty"`
	Transport            Transport             `json:"transport,omitempty"`
	EnvironmentVariables []EnvironmentVariable `json:"environmentVariables,omitempty"`
	PackageArguments     []PackageArgument     `json:"packageArguments,omitempty"`
}

// Transport describes how clients communicate with the server.
type Transport struct {
	Type string `json:"type"`
	URL  string `json:"url,omitempty"`
}

// EnvironmentVariable describes an environment variable the server needs.
type EnvironmentVariable struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	IsSecret    bool     `json:"isSecret,omitempty"`
	IsRequired  bool     `json:"isRequired,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty"`
	Format      string   `json:"format,omitempty"`
	Choices     []string `json:"choices,omitempty"`
}

// PackageArgument describes a command-line argument the server accepts.
type PackageArgument struct {
	Type        string `json:"type"` // "named" or "positional"
	Name        string `json:"name,omitempty"`
	Value       string `json:"value,omitempty"`
	IsRequired  bool   `json:"isRequired,omitempty"`
	IsSecret    bool   `json:"isSecret,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Description string `json:"description,omitempty"`
}

// KubernetesExtensions holds Kubernetes-specific configuration with no standard equivalent.
type KubernetesExtensions struct {
	DefaultPort         int32         `json:"defaultPort,omitempty"`
	NeedsServiceAccount bool          `json:"needsServiceAccount,omitempty"`
	ServiceAccountHint  string        `json:"serviceAccountHint,omitempty"`
	ConfigMaps          []ConfigMap   `json:"configMaps,omitempty"`
	SecretMounts        []SecretMount `json:"secretMounts,omitempty"`
}

// ConfigMap describes a ConfigMap the server needs.
type ConfigMap struct {
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
	DefaultContent string `json:"defaultContent,omitempty"`
	FileName       string `json:"fileName,omitempty"`
	IsRequired     bool   `json:"isRequired,omitempty"`
}

// SecretMount describes a file that should be mounted from a Secret.
type SecretMount struct {
	SecretKey   string `json:"secretKey"`
	MountPath   string `json:"mountPath"`
	Description string `json:"description,omitempty"`
	IsRequired  bool   `json:"isRequired,omitempty"`
}

// PrimaryPackage returns the first package from the entry, or nil if none exist.
func (e *ServerEntry) PrimaryPackage() *Package {
	if len(e.Packages) == 0 {
		return nil
	}
	return &e.Packages[0]
}

// DisplayTitle returns the Title if set, otherwise falls back to Name.
func (e *ServerEntry) DisplayTitle() string {
	if e.Title != "" {
		return e.Title
	}
	return e.Name
}
```

### catalog/catalog.go

No changes needed. Reads `server.json` from ConfigMaps and unmarshals into `ServerEntry`.

### handlers/handlers.go

```go
package handlers

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/matzew/mcp-launcher/catalog"
)

var mcpServerGVR = schema.GroupVersionResource{
	Group:    "mcp.x-k8s.io",
	Version:  "v1alpha1",
	Resource: "mcpservers",
}

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	catalog         *catalog.Store
	clientset       kubernetes.Interface
	dynamicClient   dynamic.Interface
	targetNamespace string
	templateDir     string
}

// New creates a new Handler.
func New(
	catalogStore *catalog.Store,
	clientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	targetNamespace string,
) *Handler {
	return &Handler{
		catalog:         catalogStore,
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		targetNamespace: targetNamespace,
		templateDir:     "templates",
	}
}

// pageTemplate parses the layout together with page-specific templates.
// This ensures each page gets its own "content" definition without conflicts.
func (h *Handler) pageTemplate(files ...string) (*template.Template, error) {
	paths := []string{filepath.Join(h.templateDir, "layout.html")}
	for _, f := range files {
		paths = append(paths, filepath.Join(h.templateDir, f))
	}
	return template.ParseFiles(paths...)
}

// Catalog renders the server catalog page.
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	entries, err := h.catalog.List(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to load catalog: %v", err), http.StatusInternalServerError)
		return
	}

	tmpl, err := h.pageTemplate("catalog.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("template parse error: %v", err), http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ActivePage": "catalog",
		"Servers":    entries,
		"Namespace":  h.targetNamespace,
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// Configure renders the configuration form for a server.
func (h *Handler) Configure(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, err := h.catalog.Get(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	saList, _ := h.clientset.CoreV1().ServiceAccounts(h.targetNamespace).List(
		r.Context(), metav1.ListOptions{},
	)
	var serviceAccounts []string
	if saList != nil {
		for _, sa := range saList.Items {
			serviceAccounts = append(serviceAccounts, sa.Name)
		}
	}

	tmpl, err := h.pageTemplate("configure.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("template parse error: %v", err), http.StatusInternalServerError)
		return
	}

	pkg := entry.PrimaryPackage()

	data := map[string]any{
		"ActivePage":      "configure",
		"Server":          entry,
		"Package":         pkg,
		"Namespace":       h.targetNamespace,
		"ServiceAccounts": serviceAccounts,
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// Preview renders the YAML preview partial (called by htmx).
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, err := h.catalog.Get(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	yaml := buildYAMLPreview(r, entry, h.targetNamespace)
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<pre><code id="yaml-output">%s</code></pre>`, template.HTMLEscapeString(yaml))
}

// Run creates the Secret(s) and MCPServer CR.
func (h *Handler) Run(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	serverName := r.FormValue("catalog-name")
	entry, err := h.catalog.Get(r.Context(), serverName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	pkg := entry.PrimaryPackage()

	instanceName := r.FormValue("instance-name")
	namespace := r.FormValue("namespace")
	if namespace == "" {
		namespace = h.targetNamespace
	}
	image := r.FormValue("image")
	port := r.FormValue("port")

	var defaultPort int32
	if k8s := entry.K8s(); k8s != nil {
		defaultPort = k8s.DefaultPort
	}

	ctx := r.Context()

	spec := map[string]any{
		"image": image,
		"port":  parsePort(port, defaultPort),
	}

	// Collect fixed args from packageArguments with a preset value
	var args []string
	if pkg != nil {
		for _, arg := range pkg.PackageArguments {
			if arg.Value != "" {
				if arg.Type == "named" && arg.Name != "" {
					args = append(args, arg.Name, arg.Value)
				} else {
					args = append(args, arg.Value)
				}
			}
		}
	}

	var envVars []any
	var secretCreated bool
	secretName := instanceName + "-credentials"
	secretData := map[string]string{}

	// Environment variables
	if pkg != nil {
		for _, ev := range pkg.EnvironmentVariables {
			value := r.FormValue("env-" + ev.Name)
			if value == "" {
				continue
			}
			secretData[sanitizeKey(ev.Name)] = value
			envVars = append(envVars, map[string]any{
				"name": ev.Name,
				"valueFrom": map[string]any{
					"secretKeyRef": map[string]any{
						"name": secretName,
						"key":  sanitizeKey(ev.Name),
					},
				},
			})
			secretCreated = true
		}
	}

	// Secret file mounts
	if k8s := entry.K8s(); k8s != nil {
		for _, sm := range k8s.SecretMounts {
			fileContent := r.FormValue("file-" + sm.SecretKey)
			if fileContent == "" {
				continue
			}
			fileSecretName := instanceName + "-" + strings.TrimSuffix(sm.SecretKey, ".pem") + "-secret"
			if err := h.createSecret(ctx, namespace, fileSecretName, map[string]string{
				sm.SecretKey: fileContent,
			}); err != nil {
				http.Error(w, fmt.Sprintf("failed to create secret: %v", err), http.StatusInternalServerError)
				return
			}
			spec["secretRef"] = map[string]any{"name": fileSecretName}
			spec["secretMountPath"] = sm.MountPath
			spec["secretKey"] = sm.SecretKey
		}
	}

	// User-provided package arguments
	if pkg != nil {
		for _, arg := range pkg.PackageArguments {
			if arg.Value != "" {
				continue // already handled above
			}
			value := r.FormValue("arg-" + arg.Name)
			if value != "" {
				if arg.Type == "named" && arg.Name != "" {
					args = append(args, arg.Name, value)
				} else {
					args = append(args, value)
				}
			}
		}
	}

	if secretCreated {
		if err := h.createSecret(ctx, namespace, secretName, secretData); err != nil {
			http.Error(w, fmt.Sprintf("failed to create secret: %v", err), http.StatusInternalServerError)
			return
		}
	}

	if len(args) > 0 {
		spec["args"] = args
	}
	if len(envVars) > 0 {
		spec["env"] = envVars
	}

	sa := r.FormValue("service-account")
	if sa != "" {
		spec["serviceAccountName"] = sa
	}

	// ConfigMap support: create from content or reference existing
	configMapRef := r.FormValue("configmap-ref")
	configMapContent := r.FormValue("configmap-content")
	if configMapRef != "" {
		// Use existing ConfigMap
		spec["configMapRef"] = map[string]any{"name": configMapRef}
	} else if configMapContent != "" && entry.K8s() != nil && len(entry.K8s().ConfigMaps) > 0 {
		// Create a new ConfigMap from the provided content
		cmName := instanceName + "-config"
		fileName := entry.K8s().ConfigMaps[0].FileName
		if fileName == "" {
			fileName = "config"
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: namespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": "mcp-launcher",
				},
			},
			Data: map[string]string{
				fileName: configMapContent,
			},
		}
		if _, err := h.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
			http.Error(w, fmt.Sprintf("failed to create ConfigMap: %v", err), http.StatusInternalServerError)
			return
		}
		spec["configMapRef"] = map[string]any{"name": cmName}
	}

	mcpServer := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "mcp.x-k8s.io/v1alpha1",
			"kind":       "MCPServer",
			"metadata": map[string]any{
				"name":      instanceName,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}

	_, err = h.dynamicClient.Resource(mcpServerGVR).Namespace(namespace).Create(
		ctx, mcpServer, metav1.CreateOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create MCPServer: %v", err), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/running", http.StatusSeeOther)
}

// Running renders the list of running MCPServer instances.
func (h *Handler) Running(w http.ResponseWriter, r *http.Request) {
	list, err := h.dynamicClient.Resource(mcpServerGVR).Namespace(h.targetNamespace).List(
		r.Context(), metav1.ListOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list servers: %v", err), http.StatusInternalServerError)
		return
	}

	type serverStatus struct {
		Name     string
		Image    string
		Phase    string
		Port     int64
		Endpoint string
	}

	var servers []serverStatus
	for _, item := range list.Items {
		name := item.GetName()
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		if phase == "" {
			phase = "Pending"
		}
		image, _, _ := unstructured.NestedString(item.Object, "spec", "image")
		port, _, _ := unstructured.NestedInt64(item.Object, "spec", "port")

		endpoint := ""
		if phase == "Running" {
			endpoint = fmt.Sprintf("http://%s.%s.svc:%d", name, h.targetNamespace, port)
		}

		servers = append(servers, serverStatus{
			Name:     name,
			Image:    image,
			Phase:    phase,
			Port:     port,
			Endpoint: endpoint,
		})
	}

	// htmx partial request - just return the list fragment
	if r.Header.Get("HX-Request") == "true" {
		tmpl, err := template.ParseFiles(filepath.Join(h.templateDir, "partials", "running-list.html"))
		if err != nil {
			http.Error(w, fmt.Sprintf("template parse error: %v", err), http.StatusInternalServerError)
			return
		}
		data := map[string]any{"Servers": servers}
		if err := tmpl.ExecuteTemplate(w, "running-list", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	tmpl, err := h.pageTemplate("running.html", filepath.Join("partials", "running-list.html"))
	if err != nil {
		http.Error(w, fmt.Sprintf("template parse error: %v", err), http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ActivePage": "running",
		"Servers":    servers,
		"Namespace":  h.targetNamespace,
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// Delete removes an MCPServer CR and its managed artifacts (Secrets, ConfigMaps).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ctx := r.Context()

	// Clean up managed Secrets and ConfigMaps created by the launcher for this instance.
	// Convention: resources are named <instance>-credentials, <instance>-*-secret, <instance>-config
	managedLabel := "app.kubernetes.io/managed-by=mcp-launcher"

	secrets, err := h.clientset.CoreV1().Secrets(h.targetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedLabel,
	})
	if err == nil {
		for _, s := range secrets.Items {
			if strings.HasPrefix(s.Name, name+"-") {
				_ = h.clientset.CoreV1().Secrets(h.targetNamespace).Delete(ctx, s.Name, metav1.DeleteOptions{})
			}
		}
	}

	configMaps, err := h.clientset.CoreV1().ConfigMaps(h.targetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedLabel,
	})
	if err == nil {
		for _, cm := range configMaps.Items {
			if strings.HasPrefix(cm.Name, name+"-") {
				_ = h.clientset.CoreV1().ConfigMaps(h.targetNamespace).Delete(ctx, cm.Name, metav1.DeleteOptions{})
			}
		}
	}

	// Delete the MCPServer CR itself
	err = h.dynamicClient.Resource(mcpServerGVR).Namespace(h.targetNamespace).Delete(
		ctx, name, metav1.DeleteOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to delete: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) createSecret(ctx context.Context, namespace, name string, data map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-launcher",
			},
		},
		StringData: data,
	}
	_, err := h.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func parsePort(s string, defaultPort int32) int64 {
	if s == "" {
		return int64(defaultPort)
	}
	var p int64
	fmt.Sscanf(s, "%d", &p)
	if p <= 0 {
		return int64(defaultPort)
	}
	return p
}

func sanitizeKey(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", "-"))
}

func buildYAMLPreview(r *http.Request, entry *catalog.ServerEntry, namespace string) string {
	pkg := entry.PrimaryPackage()

	instanceName := r.FormValue("instance-name")
	if instanceName == "" {
		instanceName = entry.Name
	}
	ns := r.FormValue("namespace")
	if ns == "" {
		ns = namespace
	}
	image := r.FormValue("image")
	if image == "" && pkg != nil {
		image = pkg.Identifier
	}
	port := r.FormValue("port")
	if port == "" && entry.K8s() != nil {
		port = fmt.Sprintf("%d", entry.K8s().DefaultPort)
	}

	var b strings.Builder
	b.WriteString("apiVersion: mcp.x-k8s.io/v1alpha1\n")
	b.WriteString("kind: MCPServer\n")
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", instanceName)
	fmt.Fprintf(&b, "  namespace: %s\n", ns)
	b.WriteString("spec:\n")
	fmt.Fprintf(&b, "  image: %s\n", image)
	fmt.Fprintf(&b, "  port: %s\n", port)

	// Service account
	sa := r.FormValue("service-account")
	if sa != "" {
		fmt.Fprintf(&b, "  serviceAccountName: %s\n", sa)
	}

	// ConfigMap ref
	configMapRef := r.FormValue("configmap-ref")
	configMapContent := r.FormValue("configmap-content")
	if configMapRef != "" {
		b.WriteString("  configMapRef:\n")
		fmt.Fprintf(&b, "    name: %s\n", configMapRef)
	} else if configMapContent != "" && entry.K8s() != nil && len(entry.K8s().ConfigMaps) > 0 {
		b.WriteString("  configMapRef:\n")
		fmt.Fprintf(&b, "    name: %s-config\n", instanceName)
	}

	// Args: fixed args from package + user-provided args
	var args []string
	if pkg != nil {
		for _, arg := range pkg.PackageArguments {
			if arg.Value != "" {
				if arg.Type == "named" && arg.Name != "" {
					args = append(args, arg.Name, arg.Value)
				} else {
					args = append(args, arg.Value)
				}
			} else {
				value := r.FormValue("arg-" + arg.Name)
				if value != "" {
					if arg.Type == "named" && arg.Name != "" {
						args = append(args, arg.Name, value)
					} else {
						args = append(args, value)
					}
				}
			}
		}
	}
	if len(args) > 0 {
		b.WriteString("  args:\n")
		for _, a := range args {
			fmt.Fprintf(&b, "    - %s\n", a)
		}
	}

	// Secret file mounts
	if k8s := entry.K8s(); k8s != nil {
		for _, sm := range k8s.SecretMounts {
			content := r.FormValue("file-" + sm.SecretKey)
			if content != "" {
				secretName := instanceName + "-" + strings.TrimSuffix(sm.SecretKey, ".pem") + "-secret"
				b.WriteString("  secretRef:\n")
				fmt.Fprintf(&b, "    name: %s\n", secretName)
				fmt.Fprintf(&b, "  secretMountPath: %s\n", sm.MountPath)
				fmt.Fprintf(&b, "  secretKey: %s\n", sm.SecretKey)
			}
		}
	}

	// Env variables
	var hasEnvVars bool
	if pkg != nil {
		for _, ev := range pkg.EnvironmentVariables {
			value := r.FormValue("env-" + ev.Name)
			if value != "" {
				if !hasEnvVars {
					b.WriteString("  env:\n")
					hasEnvVars = true
				}
				credSecretName := instanceName + "-credentials"
				fmt.Fprintf(&b, "    - name: %s\n", ev.Name)
				b.WriteString("      valueFrom:\n")
				b.WriteString("        secretKeyRef:\n")
				fmt.Fprintf(&b, "          name: %s\n", credSecretName)
				fmt.Fprintf(&b, "          key: %s\n", sanitizeKey(ev.Name))
			}
		}
	}

	return b.String()
}
```

### templates/catalog.html

```html
{{ define "content" }}
<div class="page-header">
  <h2>MCP Server Catalog</h2>
  <p>Browse available MCP servers and deploy them to your cluster</p>
</div>

{{ if not .Servers }}
<div class="card empty-state">
  <p>No servers found in catalog.</p>
  <p>Add ConfigMaps with label <code>mcp.x-k8s.io/catalog-entry=true</code> to the catalog namespace.</p>
</div>
{{ else }}
<div class="grid">
  {{ range .Servers }}
  <div class="card">
    <h3>{{ .DisplayTitle }}</h3>
    <p>{{ .Description }}</p>
    <div>
      {{ if .K8s }}{{ if .K8s.NeedsServiceAccount }}<span class="badge">ServiceAccount</span>{{ end }}{{ end }}
      {{ if .K8s }}{{ if .K8s.ConfigMaps }}<span class="badge">ConfigMap</span>{{ end }}{{ end }}
      {{ if .PrimaryPackage }}{{ if .PrimaryPackage.EnvironmentVariables }}<span class="badge">Credentials</span>{{ end }}{{ end }}
      {{ if .K8s }}{{ if .K8s.SecretMounts }}<span class="badge">File Mount</span>{{ end }}{{ end }}
      {{ $hasEnv := false }}{{ $hasSA := false }}{{ $hasCM := false }}{{ $hasSM := false }}
      {{ if .PrimaryPackage }}{{ if .PrimaryPackage.EnvironmentVariables }}{{ $hasEnv = true }}{{ end }}{{ end }}
      {{ if .K8s }}{{ if .K8s.NeedsServiceAccount }}{{ $hasSA = true }}{{ end }}{{ end }}
      {{ if .K8s }}{{ if .K8s.ConfigMaps }}{{ $hasCM = true }}{{ end }}{{ end }}
      {{ if .K8s }}{{ if .K8s.SecretMounts }}{{ $hasSM = true }}{{ end }}{{ end }}
      {{ if not $hasEnv }}{{ if not $hasSA }}{{ if not $hasCM }}{{ if not $hasSM }}
        <span class="badge badge-green">Ready to run</span>
      {{ end }}{{ end }}{{ end }}{{ end }}
    </div>
    <div style="margin-top: 0.75rem;">
      <a href="/configure/{{ .Name }}" class="btn btn-primary">Configure &amp; Run</a>
    </div>
  </div>
  {{ end }}
</div>
{{ end }}
{{ end }}
```

### templates/configure.html

```html
{{ define "content" }}
<a href="/" class="back-link">&larr; Back to catalog</a>

<div class="page-header" style="margin-top: 1rem;">
  <h2>{{ .Server.DisplayTitle }}</h2>
  <p>{{ .Server.Description }}</p>
</div>

<div class="two-col">

  <!-- Left: Configuration Form -->
  <div>
    <form id="configure-form" method="POST" action="/run">
      <input type="hidden" name="catalog-name" value="{{ .Server.Name }}">

      <div class="section-header">Instance</div>

      <div class="form-group">
        <label for="instance-name">Name</label>
        <input type="text" id="instance-name" name="instance-name" value="{{ .Server.Name }}" required
               pattern="[a-z0-9]([a-z0-9-]*[a-z0-9])?" title="Lowercase letters, numbers, and hyphens">
      </div>

      <div class="form-group">
        <label for="namespace">Namespace</label>
        <input type="text" id="namespace" name="namespace" value="{{ .Namespace }}">
      </div>

      <div class="section-header">Container</div>

      <div class="form-group">
        <label for="image">Image</label>
        <input type="text" id="image" name="image" value="{{ if .Package }}{{ .Package.Identifier }}{{ end }}">
      </div>

      <div class="form-group">
        <label for="port">Port</label>
        <input type="number" id="port" name="port" value="{{ if .Server.K8s }}{{ .Server.K8s.DefaultPort }}{{ end }}" min="1" max="65535">
      </div>

      {{ if .Server.K8s }}{{ if .Server.K8s.NeedsServiceAccount }}
      <div class="section-header">Service Account</div>
      {{ if .Server.K8s.ServiceAccountHint }}
      <p class="hint" style="margin-bottom: 0.5rem;">{{ .Server.K8s.ServiceAccountHint }}</p>
      {{ end }}
      <div class="form-group">
        <label for="service-account">Service Account</label>
        <select id="service-account" name="service-account">
          <option value="">-- select --</option>
          {{ range .ServiceAccounts }}
          <option value="{{ . }}">{{ . }}</option>
          {{ end }}
        </select>
      </div>
      {{ end }}{{ end }}

      {{ if .Server.K8s }}{{ if .Server.K8s.ConfigMaps }}
      <div class="section-header">Configuration</div>
      {{ range .Server.K8s.ConfigMaps }}
      <div class="form-group">
        <label>{{ .Label }}{{ if .IsRequired }} *{{ end }}</label>
        {{ if .Description }}<p class="hint">{{ .Description }}</p>{{ end }}
        <textarea name="configmap-content" placeholder="Paste configuration here...">{{ .DefaultContent }}</textarea>
      </div>
      {{ end }}
      <div class="form-group">
        <label for="configmap-ref">Or use existing ConfigMap</label>
        <input type="text" id="configmap-ref" name="configmap-ref" placeholder="configmap-name (leave empty to create new)">
      </div>
      {{ end }}{{ end }}

      {{ if .Package }}{{ if .Package.EnvironmentVariables }}
      <div class="section-header">Environment Variables</div>
      {{ range .Package.EnvironmentVariables }}
        <div class="form-group">
          <label>{{ .Name }}{{ if .IsRequired }} *{{ end }}</label>
          {{ if .Description }}<p class="hint">{{ .Description }}</p>{{ end }}
          {{ if .Choices }}
          <select name="env-{{ .Name }}" {{ if .IsRequired }}required{{ end }}>
            <option value="">-- select --</option>
            {{ range .Choices }}
            <option value="{{ . }}">{{ . }}</option>
            {{ end }}
          </select>
          {{ else }}
          <input type="{{ if .IsSecret }}password{{ else }}text{{ end }}"
                 name="env-{{ .Name }}"
                 placeholder="{{ .Placeholder }}"
                 value="{{ .Default }}"
                 {{ if .IsRequired }}required{{ end }}>
          {{ end }}
        </div>
      {{ end }}
      {{ end }}{{ end }}

      {{ if .Package }}{{ if .Package.PackageArguments }}
      {{ $hasUserArgs := false }}
      {{ range .Package.PackageArguments }}{{ if not .Value }}{{ $hasUserArgs = true }}{{ end }}{{ end }}
      {{ if $hasUserArgs }}
      <div class="section-header">Arguments</div>
      {{ range .Package.PackageArguments }}
        {{ if not .Value }}
        <div class="form-group">
          <label>{{ if .Name }}{{ .Name }}{{ else }}Argument{{ end }}{{ if .IsRequired }} *{{ end }}</label>
          {{ if .Description }}<p class="hint">{{ .Description }}</p>{{ end }}
          <input type="{{ if .IsSecret }}password{{ else }}text{{ end }}"
                 name="arg-{{ .Name }}"
                 placeholder="{{ .Placeholder }}"
                 {{ if .IsRequired }}required{{ end }}>
        </div>
        {{ end }}
      {{ end }}
      {{ end }}
      {{ end }}{{ end }}

      {{ if .Server.K8s }}{{ if .Server.K8s.SecretMounts }}
      <div class="section-header">File Mounts</div>
      {{ range .Server.K8s.SecretMounts }}
        <div class="form-group">
          <label>{{ .SecretKey }}{{ if .IsRequired }} *{{ end }}</label>
          {{ if .Description }}<p class="hint">{{ .Description }}</p>{{ end }}
          <textarea name="file-{{ .SecretKey }}"
                    placeholder="Paste file content here..."
                    {{ if .IsRequired }}required{{ end }}></textarea>
        </div>
      {{ end }}
      {{ end }}{{ end }}

      <div class="actions">
        <button type="submit" class="btn btn-primary">Run</button>
        <button type="button" class="btn btn-outline"
                onclick="let t=document.getElementById('yaml-output').textContent; navigator.clipboard.writeText(t); this.textContent='Copied!'; setTimeout(()=>this.textContent='Copy YAML',1500)">
          Copy YAML
        </button>
      </div>
    </form>
  </div>

  <!-- Right: Live YAML Preview -->
  <div>
    <div class="section-header">YAML Preview</div>
    <div class="yaml-preview"
         hx-get="/preview/{{ .Server.Name }}"
         hx-trigger="input from:#configure-form delay:300ms"
         hx-include="#configure-form">
      <pre><code id="yaml-output">apiVersion: mcp.x-k8s.io/v1alpha1
kind: MCPServer
metadata:
  name: {{ .Server.Name }}
  namespace: {{ .Namespace }}
spec:
  image: {{ if .Package }}{{ .Package.Identifier }}{{ end }}
  port: {{ if .Server.K8s }}{{ .Server.K8s.DefaultPort }}{{ end }}</code></pre>
    </div>
  </div>

</div>
{{ end }}
```

### deploy/catalog/ansible-mcp.yaml

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: catalog-ansible-mcp
  namespace: mcp-catalog
  labels:
    mcp.x-k8s.io/catalog-entry: "true"
data:
  server.json: |
    {
      "name": "ansible-mcp-server",
      "title": "Ansible Automation Platform",
      "description": "Interact with Ansible Automation Platform (AAP) via MCP",
      "version": "1.0.0",
      "packages": [{
        "registryType": "oci",
        "identifier": "registry.redhat.io/ansible-automation-platform-25/aap-mcp-server-rhel9:latest",
        "transport": { "type": "streamable-http" },
        "environmentVariables": [
          {
            "name": "AAP_URL",
            "description": "AAP Base URL",
            "placeholder": "https://aap.example.com",
            "isRequired": true,
            "isSecret": false
          },
          {
            "name": "AAP_OAUTH_TOKEN",
            "description": "OAuth2 Bearer Token (validated against {base-url}/api/gateway/v1/me/)",
            "isRequired": true,
            "isSecret": true
          }
        ]
      }],
      "_meta": { "io.openshift/k8s": { "defaultPort": 3000 } }
    }
```

### deploy/catalog/everything-mcp.yaml

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: catalog-everything-mcp
  namespace: mcp-catalog
  labels:
    mcp.x-k8s.io/catalog-entry: "true"
data:
  server.json: |
    {
      "name": "everything-mcp-server",
      "title": "Everything MCP (Test)",
      "description": "Reference MCP server for testing (echoes tools, resources, prompts)",
      "version": "1.0.0",
      "packages": [{
        "registryType": "oci",
        "identifier": "quay.io/matzew/mcp-everything:latest",
        "transport": { "type": "streamable-http" }
      }],
      "_meta": { "io.openshift/k8s": { "defaultPort": 3001 } }
    }
```

### deploy/catalog/insights-mcp.yaml

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: catalog-insights-mcp
  namespace: mcp-catalog
  labels:
    mcp.x-k8s.io/catalog-entry: "true"
data:
  server.json: |
    {
      "name": "insights-mcp-server",
      "title": "Red Hat Insights & Lightspeed",
      "description": "Red Hat Insights and Lightspeed integration via MCP",
      "version": "1.0.0",
      "packages": [{
        "registryType": "oci",
        "identifier": "ghcr.io/redhatinsights/red-hat-lightspeed-mcp:latest",
        "transport": { "type": "streamable-http" },
        "environmentVariables": [
          {
            "name": "LIGHTSPEED_CLIENT_ID",
            "description": "Service Account Client ID (create at console.redhat.com)",
            "isRequired": true,
            "isSecret": true
          },
          {
            "name": "LIGHTSPEED_CLIENT_SECRET",
            "description": "Service Account Client Secret",
            "isRequired": true,
            "isSecret": true
          }
        ],
        "packageArguments": [
          {
            "type": "positional",
            "value": "http"
          }
        ]
      }],
      "_meta": { "io.openshift/k8s": { "defaultPort": 8000 } }
    }
```

### deploy/catalog/kubernetes-mcp.yaml

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: catalog-kubernetes-mcp
  namespace: mcp-catalog
  labels:
    mcp.x-k8s.io/catalog-entry: "true"
data:
  server.json: |
    {
      "name": "kubernetes-mcp-server",
      "title": "Kubernetes",
      "description": "Query and manage Kubernetes resources via MCP",
      "version": "1.0.0",
      "packages": [{
        "registryType": "oci",
        "identifier": "quay.io/containers/kubernetes_mcp_server:latest",
        "transport": { "type": "streamable-http" },
        "packageArguments": [
          {
            "type": "named",
            "name": "--config",
            "value": "/etc/mcp-config/config.toml"
          }
        ]
      }],
      "_meta": { "io.openshift/k8s": {
        "defaultPort": 8080,
        "needsServiceAccount": true,
        "serviceAccountHint": "Needs a ServiceAccount bound to the 'view' ClusterRole for read-only access",
        "configMaps": [
          {
            "label": "Server Configuration (config.toml)",
            "description": "TOML config file mounted at /etc/mcp-config/config.toml",
            "defaultContent": "# Kubernetes MCP Server Configuration\nlog_level = 5\nport = \"8080\"\nread_only = true\ntoolsets = [\"core\", \"config\"]\n",
            "fileName": "config.toml"
          }
        ]
      } }
    }
```

### deploy/catalog/satellite-mcp.yaml

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: catalog-satellite-mcp
  namespace: mcp-catalog
  labels:
    mcp.x-k8s.io/catalog-entry: "true"
data:
  server.json: |
    {
      "name": "satellite-mcp-server",
      "title": "Red Hat Satellite",
      "description": "Interact with Red Hat Satellite for host and content management",
      "version": "1.0.0",
      "packages": [{
        "registryType": "oci",
        "identifier": "registry.redhat.io/satellite/foreman-mcp-server-rhel9:6.18",
        "transport": { "type": "streamable-http" },
        "packageArguments": [
          {
            "type": "named",
            "name": "--foreman-url",
            "description": "Satellite URL",
            "placeholder": "https://satellite.example.com",
            "isRequired": true
          }
        ]
      }],
      "_meta": { "io.openshift/k8s": {
        "defaultPort": 8080,
        "secretMounts": [
          {
            "secretKey": "ca.pem",
            "mountPath": "/app/ca.pem",
            "description": "Satellite CA Bundle (download from https://satellite.example.com/unattended/public/foreman_raw_ca)",
            "isRequired": true
          }
        ]
      } }
    }
```

## Build Verification

```
$ make build
go build -o bin/mcp-launcher .
# success - binary at bin/mcp-launcher (63M)
```

## References

- [MCP Registry - GitHub](https://github.com/modelcontextprotocol/registry)
- [server.json reference](https://raw.githubusercontent.com/modelcontextprotocol/registry/refs/heads/main/docs/reference/server-json/generic-server-json.md)
- [Kubeflow Model Registry MCP catalog](https://github.com/kubeflow/model-registry/tree/main/manifests/kustomize/options/catalog/overlays/demo)
