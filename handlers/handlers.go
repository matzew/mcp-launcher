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

	data := map[string]any{
		"ActivePage":      "configure",
		"Server":          entry,
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

	instanceName := r.FormValue("instance-name")
	namespace := r.FormValue("namespace")
	if namespace == "" {
		namespace = h.targetNamespace
	}
	image := r.FormValue("image")
	port := r.FormValue("port")

	ctx := r.Context()

	spec := map[string]any{
		"image": image,
		"port":  parsePort(port, entry.DefaultPort),
	}

	args := append([]string{}, entry.Args...)
	var envVars []any
	var secretCreated bool
	secretName := instanceName + "-credentials"
	secretData := map[string]string{}

	for _, cred := range entry.Credentials {
		switch cred.Type {
		case "env":
			value := r.FormValue("cred-" + cred.EnvName)
			if value == "" {
				continue
			}
			secretData[sanitizeKey(cred.EnvName)] = value
			envVars = append(envVars, map[string]any{
				"name": cred.EnvName,
				"valueFrom": map[string]any{
					"secretKeyRef": map[string]any{
						"name": secretName,
						"key":  sanitizeKey(cred.EnvName),
					},
				},
			})
			secretCreated = true

		case "file":
			fileContent := r.FormValue("cred-file-" + cred.SecretKey)
			if fileContent == "" {
				continue
			}
			fileSecretName := instanceName + "-" + strings.TrimSuffix(cred.SecretKey, ".pem") + "-secret"
			if err := h.createSecret(ctx, namespace, fileSecretName, map[string]string{
				cred.SecretKey: fileContent,
			}); err != nil {
				http.Error(w, fmt.Sprintf("failed to create secret: %v", err), http.StatusInternalServerError)
				return
			}
			spec["secretRef"] = map[string]any{"name": fileSecretName}
			spec["secretMountPath"] = cred.MountPath
			spec["secretKey"] = cred.SecretKey

		case "arg":
			value := r.FormValue("arg-" + cred.Flag)
			if value != "" {
				args = append(args, cred.Flag, value)
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
	} else if configMapContent != "" && len(entry.ConfigMaps) > 0 {
		// Create a new ConfigMap from the provided content
		cmName := instanceName + "-config"
		fileName := entry.ConfigMaps[0].FileName
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
	instanceName := r.FormValue("instance-name")
	if instanceName == "" {
		instanceName = entry.Name
	}
	ns := r.FormValue("namespace")
	if ns == "" {
		ns = namespace
	}
	image := r.FormValue("image")
	if image == "" {
		image = entry.Image
	}
	port := r.FormValue("port")
	if port == "" {
		port = fmt.Sprintf("%d", entry.DefaultPort)
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
	} else if configMapContent != "" && len(entry.ConfigMaps) > 0 {
		b.WriteString("  configMapRef:\n")
		fmt.Fprintf(&b, "    name: %s-config\n", instanceName)
	}

	// Args
	args := append([]string{}, entry.Args...)
	for _, cred := range entry.Credentials {
		if cred.Type == "arg" {
			value := r.FormValue("arg-" + cred.Flag)
			if value != "" {
				args = append(args, cred.Flag, value)
			}
		}
	}
	if len(args) > 0 {
		b.WriteString("  args:\n")
		for _, a := range args {
			fmt.Fprintf(&b, "    - %s\n", a)
		}
	}

	// File credentials
	for _, cred := range entry.Credentials {
		if cred.Type == "file" {
			content := r.FormValue("cred-file-" + cred.SecretKey)
			if content != "" {
				secretName := instanceName + "-" + strings.TrimSuffix(cred.SecretKey, ".pem") + "-secret"
				b.WriteString("  secretRef:\n")
				fmt.Fprintf(&b, "    name: %s\n", secretName)
				fmt.Fprintf(&b, "  secretMountPath: %s\n", cred.MountPath)
				fmt.Fprintf(&b, "  secretKey: %s\n", cred.SecretKey)
			}
		}
	}

	// Env credentials
	var hasEnvCreds bool
	for _, cred := range entry.Credentials {
		if cred.Type == "env" {
			value := r.FormValue("cred-" + cred.EnvName)
			if value != "" {
				if !hasEnvCreds {
					b.WriteString("  env:\n")
					hasEnvCreds = true
				}
				credSecretName := instanceName + "-credentials"
				fmt.Fprintf(&b, "    - name: %s\n", cred.EnvName)
				b.WriteString("      valueFrom:\n")
				b.WriteString("        secretKeyRef:\n")
				fmt.Fprintf(&b, "          name: %s\n", credSecretName)
				fmt.Fprintf(&b, "          key: %s\n", sanitizeKey(cred.EnvName))
			}
		}
	}

	return b.String()
}
