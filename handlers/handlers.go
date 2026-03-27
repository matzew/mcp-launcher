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

var httpRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

var mcpServerRegistrationGVR = schema.GroupVersionResource{
	Group:    "mcp.kagenti.com",
	Version:  "v1alpha1",
	Resource: "mcpserverregistrations",
}

// GatewayConfig holds mcp-gateway integration settings.
type GatewayConfig struct {
	Enabled          bool
	GatewayName      string
	GatewayNamespace string
}

// Handler holds dependencies for HTTP handlers.
type Handler struct {
	catalog         *catalog.Store
	clientset       kubernetes.Interface
	dynamicClient   dynamic.Interface
	targetNamespace string
	templateDir     string
	gateway         GatewayConfig
}

// New creates a new Handler.
func New(
	catalogStore *catalog.Store,
	clientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	targetNamespace string,
	gateway GatewayConfig,
) *Handler {
	return &Handler{
		catalog:         catalogStore,
		clientset:       clientset,
		dynamicClient:   dynamicClient,
		targetNamespace: targetNamespace,
		templateDir:     "templates",
		gateway:         gateway,
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

// Run creates the MCPServer CR, then the owned Secret(s) and ConfigMap(s).
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

	configSection := map[string]any{
		"port": parsePort(port, defaultPort),
		"path": "/mcp",
	}
	spec := map[string]any{
		"source": map[string]any{
			"type": "ContainerImage",
			"containerImage": map[string]any{
				"ref": image,
			},
		},
		"config": configSection,
	}
	var storageEntries []any

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

	// Track resources to create after the MCPServer CR (so we can set ownerReferences)
	type pendingSecret struct {
		name string
		data map[string]string
	}
	type pendingConfigMap struct {
		name string
		data map[string]string
	}
	var pendingSecrets []pendingSecret
	var pendingConfigMaps []pendingConfigMap

	var envVars []any
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
		}
	}
	if len(secretData) > 0 {
		pendingSecrets = append(pendingSecrets, pendingSecret{name: secretName, data: secretData})
	}

	// Secret file mounts
	if k8s := entry.K8s(); k8s != nil {
		for _, sm := range k8s.SecretMounts {
			fileContent := r.FormValue("file-" + sm.SecretKey)
			if fileContent == "" {
				continue
			}
			fileSecretName := instanceName + "-" + strings.TrimSuffix(sm.SecretKey, ".pem") + "-secret"
			pendingSecrets = append(pendingSecrets, pendingSecret{
				name: fileSecretName,
				data: map[string]string{sm.SecretKey: fileContent},
			})
			storageEntries = append(storageEntries, map[string]any{
				"path": sm.MountPath,
				"source": map[string]any{
					"type": "Secret",
					"secret": map[string]any{
						"secretName": fileSecretName,
						"items": []any{
							map[string]any{"key": sm.SecretKey, "path": sm.SecretKey},
						},
					},
				},
			})
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

	if len(args) > 0 {
		configSection["arguments"] = args
	}
	if len(envVars) > 0 {
		configSection["env"] = envVars
	}

	sa := r.FormValue("service-account")
	runAsRoot := r.FormValue("run-as-root")
	runtimeSecurity := map[string]any{}
	if sa != "" {
		runtimeSecurity["serviceAccountName"] = sa
	}
	if runAsRoot == "on" {
		runtimeSecurity["securityContext"] = map[string]any{
			"runAsNonRoot": false,
		}
	}
	if len(runtimeSecurity) > 0 {
		spec["runtime"] = map[string]any{"security": runtimeSecurity}
	}

	// ConfigMap support: create from content or reference existing
	configMapRef := r.FormValue("configmap-ref")
	configMapContent := r.FormValue("configmap-content")
	if configMapRef != "" {
		mountPath := "/etc/mcp-config"
		if entry.K8s() != nil && len(entry.K8s().ConfigMaps) > 0 && entry.K8s().ConfigMaps[0].MountPath != "" {
			mountPath = entry.K8s().ConfigMaps[0].MountPath
		}
		storageEntries = append(storageEntries, map[string]any{
			"path": mountPath,
			"source": map[string]any{
				"type":      "ConfigMap",
				"configMap": map[string]any{"name": configMapRef},
			},
		})
	} else if configMapContent != "" && entry.K8s() != nil && len(entry.K8s().ConfigMaps) > 0 {
		cmName := instanceName + "-config"
		fileName := entry.K8s().ConfigMaps[0].FileName
		if fileName == "" {
			fileName = "config"
		}
		mountPath := entry.K8s().ConfigMaps[0].MountPath
		if mountPath == "" {
			mountPath = "/etc/mcp-config"
		}
		pendingConfigMaps = append(pendingConfigMaps, pendingConfigMap{
			name: cmName,
			data: map[string]string{fileName: configMapContent},
		})
		storageEntries = append(storageEntries, map[string]any{
			"path": mountPath,
			"source": map[string]any{
				"type":      "ConfigMap",
				"configMap": map[string]any{"name": cmName},
			},
		})
	}

	if len(storageEntries) > 0 {
		configSection["storage"] = storageEntries
	}

	// Create the MCPServer CR first to get its UID for ownerReferences
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

	created, err := h.dynamicClient.Resource(mcpServerGVR).Namespace(namespace).Create(
		ctx, mcpServer, metav1.CreateOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create MCPServer: %v", err), http.StatusInternalServerError)
		return
	}

	ownerRef := ownerRefFrom(created)

	// Create owned Secrets
	for _, ps := range pendingSecrets {
		if err := h.createSecret(ctx, namespace, ps.name, ps.data, ownerRef); err != nil {
			http.Error(w, fmt.Sprintf("failed to create secret: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Create owned ConfigMaps
	for _, pcm := range pendingConfigMaps {
		if err := h.createConfigMap(ctx, namespace, pcm.name, pcm.data, ownerRef); err != nil {
			http.Error(w, fmt.Sprintf("failed to create ConfigMap: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Create gateway resources if mcp-gateway integration is enabled
	if h.gateway.Enabled {
		mcpPort, _, _ := unstructured.NestedInt64(created.Object, "spec", "config", "port")
		if mcpPort <= 0 {
			mcpPort = 8080
		}
		if err := h.createHTTPRoute(ctx, namespace, instanceName, mcpPort, ownerRef); err != nil {
			log.Printf("failed to create HTTPRoute: %v", err)
		}
		if err := h.createMCPServerRegistration(ctx, namespace, instanceName, ownerRef); err != nil {
			log.Printf("failed to create MCPServerRegistration: %v", err)
		}
	}

	http.Redirect(w, r, "/running", http.StatusSeeOther)
}

// QuickDeploy creates an MCPServer CR directly from a catalog entry's crTemplate.
func (h *Handler) QuickDeploy(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	entry, err := h.catalog.Get(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if !entry.IsOneClick() {
		http.Error(w, "this server requires configuration", http.StatusBadRequest)
		return
	}

	spec := make(map[string]any)
	for k, v := range entry.K8s().CRTemplate {
		spec[k] = v
	}

	namespace := h.targetNamespace
	if ns, ok := spec["namespace"].(string); ok && ns != "" {
		namespace = ns
		delete(spec, "namespace")
	}

	instanceName := entry.Name
	ctx := r.Context()

	// If the catalog entry requires root, inject securityContext override
	if k8s := entry.K8s(); k8s != nil && k8s.RunAsRoot {
		runtimeMap, _ := spec["runtime"].(map[string]any)
		if runtimeMap == nil {
			runtimeMap = map[string]any{}
			spec["runtime"] = runtimeMap
		}
		securityMap, _ := runtimeMap["security"].(map[string]any)
		if securityMap == nil {
			securityMap = map[string]any{}
			runtimeMap["security"] = securityMap
		}
		securityMap["securityContext"] = map[string]any{
			"runAsNonRoot": false,
		}
	}

	// Pre-wire configMapRef names (resources created after the CR for ownerReferences)
	type pendingConfigMap struct {
		name     string
		fileName string
		content  string
	}
	var pendingCMs []pendingConfigMap
	if k8s := entry.K8s(); k8s != nil {
		for _, cm := range k8s.ConfigMaps {
			if cm.DefaultContent == "" {
				continue
			}
			cmName := instanceName + "-config"
			fileName := cm.FileName
			if fileName == "" {
				fileName = "config"
			}
			mountPath := cm.MountPath
			if mountPath == "" {
				mountPath = "/etc/mcp-config"
			}
			pendingCMs = append(pendingCMs, pendingConfigMap{name: cmName, fileName: fileName, content: cm.DefaultContent})
			// Navigate into spec.config.storage to append the ConfigMap entry
			configMap, _ := spec["config"].(map[string]any)
			if configMap == nil {
				configMap = map[string]any{}
				spec["config"] = configMap
			}
			storageSlice, _ := configMap["storage"].([]any)
			storageSlice = append(storageSlice, map[string]any{
				"path": mountPath,
				"source": map[string]any{
					"type":      "ConfigMap",
					"configMap": map[string]any{"name": cmName},
				},
			})
			configMap["storage"] = storageSlice
		}
	}

	// Create MCPServer CR first to get its UID for ownerReferences
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

	created, err := h.dynamicClient.Resource(mcpServerGVR).Namespace(namespace).Create(
		ctx, mcpServer, metav1.CreateOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create MCPServer: %v", err), http.StatusInternalServerError)
		return
	}

	ownerRef := ownerRefFrom(created)

	// Create owned ConfigMaps
	for _, pcm := range pendingCMs {
		if err := h.createConfigMap(ctx, namespace, pcm.name, map[string]string{pcm.fileName: pcm.content}, ownerRef); err != nil {
			http.Error(w, fmt.Sprintf("failed to create ConfigMap: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Create gateway resources if mcp-gateway integration is enabled
	if h.gateway.Enabled {
		mcpPort, _, _ := unstructured.NestedInt64(created.Object, "spec", "config", "port")
		if mcpPort <= 0 {
			mcpPort = 8080
		}
		if err := h.createHTTPRoute(ctx, namespace, instanceName, mcpPort, ownerRef); err != nil {
			log.Printf("failed to create HTTPRoute: %v", err)
		}
		if err := h.createMCPServerRegistration(ctx, namespace, instanceName, ownerRef); err != nil {
			log.Printf("failed to create MCPServerRegistration: %v", err)
		}
	}

	http.Redirect(w, r, "/running", http.StatusSeeOther)
}

// Running renders the list of running MCPServer instances across all namespaces.
func (h *Handler) Running(w http.ResponseWriter, r *http.Request) {
	list, err := h.dynamicClient.Resource(mcpServerGVR).Namespace("").List(
		r.Context(), metav1.ListOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list servers: %v", err), http.StatusInternalServerError)
		return
	}

	type serverStatus struct {
		Name      string
		Namespace string
		Image     string
		Phase     string
		Port      int64
		Endpoint  string
	}

	var servers []serverStatus
	for _, item := range list.Items {
		name := item.GetName()
		ns := item.GetNamespace()
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		if phase == "" {
			phase = "Pending"
		}
		image, _, _ := unstructured.NestedString(item.Object, "spec", "source", "containerImage", "ref")
		port, _, _ := unstructured.NestedInt64(item.Object, "spec", "config", "port")

		endpoint, _, _ := unstructured.NestedString(item.Object, "status", "address", "url")

		servers = append(servers, serverStatus{
			Name:      name,
			Namespace: ns,
			Image:     image,
			Phase:     phase,
			Port:      port,
			Endpoint:  endpoint,
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
	}
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

// Delete removes an MCPServer CR and its managed artifacts (Secrets, ConfigMaps).
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	namespace := r.PathValue("namespace")
	name := r.PathValue("name")
	ctx := r.Context()

	// Clean up managed Secrets and ConfigMaps created by the launcher for this instance.
	// With ownerReferences this is a safety net — GC handles the common case.
	managedLabel := "app.kubernetes.io/managed-by=mcp-launcher"

	secrets, err := h.clientset.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedLabel,
	})
	if err == nil {
		for _, s := range secrets.Items {
			if strings.HasPrefix(s.Name, name+"-") {
				_ = h.clientset.CoreV1().Secrets(namespace).Delete(ctx, s.Name, metav1.DeleteOptions{})
			}
		}
	}

	configMaps, err := h.clientset.CoreV1().ConfigMaps(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: managedLabel,
	})
	if err == nil {
		for _, cm := range configMaps.Items {
			if strings.HasPrefix(cm.Name, name+"-") {
				_ = h.clientset.CoreV1().ConfigMaps(namespace).Delete(ctx, cm.Name, metav1.DeleteOptions{})
			}
		}
	}

	// Clean up gateway resources (safety net — ownerReferences handle the common case)
	if h.gateway.Enabled {
		routes, err := h.dynamicClient.Resource(httpRouteGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: managedLabel,
		})
		if err == nil {
			for _, r := range routes.Items {
				if r.GetName() == name {
					_ = h.dynamicClient.Resource(httpRouteGVR).Namespace(namespace).Delete(ctx, r.GetName(), metav1.DeleteOptions{})
				}
			}
		}

		regs, err := h.dynamicClient.Resource(mcpServerRegistrationGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: managedLabel,
		})
		if err == nil {
			for _, r := range regs.Items {
				if r.GetName() == name {
					_ = h.dynamicClient.Resource(mcpServerRegistrationGVR).Namespace(namespace).Delete(ctx, r.GetName(), metav1.DeleteOptions{})
				}
			}
		}
	}

	// Delete the MCPServer CR itself
	err = h.dynamicClient.Resource(mcpServerGVR).Namespace(namespace).Delete(
		ctx, name, metav1.DeleteOptions{},
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to delete: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ownerRefFrom builds an OwnerReference from a created MCPServer CR.
func ownerRefFrom(obj *unstructured.Unstructured) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		UID:        obj.GetUID(),
	}
}

func (h *Handler) createSecret(ctx context.Context, namespace, name string, data map[string]string, ownerRef metav1.OwnerReference) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-launcher",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		StringData: data,
	}
	_, err := h.clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

func (h *Handler) createConfigMap(ctx context.Context, namespace, name string, data map[string]string, ownerRef metav1.OwnerReference) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-launcher",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Data: data,
	}
	_, err := h.clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{})
	return err
}

func toolPrefix(name string) string {
	s := name
	for _, suffix := range []string{"-mcp-server", "-server", "-mcp"} {
		if strings.HasSuffix(s, suffix) {
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}
	return strings.ReplaceAll(s, "-", "_") + "_"
}

func (h *Handler) createHTTPRoute(ctx context.Context, namespace, name string, port int64, ownerRef metav1.OwnerReference) error {
	route := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "mcp-launcher",
				},
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": ownerRef.APIVersion,
						"kind":       ownerRef.Kind,
						"name":       ownerRef.Name,
						"uid":        string(ownerRef.UID),
					},
				},
			},
			"spec": map[string]any{
				"hostnames": []any{
					name + ".mcp.local",
				},
				"parentRefs": []any{
					map[string]any{
						"name":      h.gateway.GatewayName,
						"namespace": h.gateway.GatewayNamespace,
					},
				},
				"rules": []any{
					map[string]any{
						"backendRefs": []any{
							map[string]any{
								"name": name,
								"port": port,
							},
						},
					},
				},
			},
		},
	}
	_, err := h.dynamicClient.Resource(httpRouteGVR).Namespace(namespace).Create(ctx, route, metav1.CreateOptions{})
	return err
}

func (h *Handler) createMCPServerRegistration(ctx context.Context, namespace, name string, ownerRef metav1.OwnerReference) error {
	reg := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "mcp.kagenti.com/v1alpha1",
			"kind":       "MCPServerRegistration",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
				"labels": map[string]any{
					"app.kubernetes.io/managed-by": "mcp-launcher",
				},
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": ownerRef.APIVersion,
						"kind":       ownerRef.Kind,
						"name":       ownerRef.Name,
						"uid":        string(ownerRef.UID),
					},
				},
			},
			"spec": map[string]any{
				"toolPrefix": toolPrefix(name),
				"targetRef": map[string]any{
					"group": "gateway.networking.k8s.io",
					"kind":  "HTTPRoute",
					"name":  name,
				},
			},
		},
	}
	_, err := h.dynamicClient.Resource(mcpServerRegistrationGVR).Namespace(namespace).Create(ctx, reg, metav1.CreateOptions{})
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

	// Source
	b.WriteString("  source:\n")
	b.WriteString("    type: ContainerImage\n")
	b.WriteString("    containerImage:\n")
	fmt.Fprintf(&b, "      ref: %s\n", image)

	// Config
	b.WriteString("  config:\n")
	fmt.Fprintf(&b, "    port: %s\n", port)
	b.WriteString("    path: /mcp\n")

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
		b.WriteString("    arguments:\n")
		for _, a := range args {
			fmt.Fprintf(&b, "      - %s\n", a)
		}
	}

	// Env variables
	var hasEnvVars bool
	if pkg != nil {
		for _, ev := range pkg.EnvironmentVariables {
			value := r.FormValue("env-" + ev.Name)
			if value != "" {
				if !hasEnvVars {
					b.WriteString("    env:\n")
					hasEnvVars = true
				}
				credSecretName := instanceName + "-credentials"
				fmt.Fprintf(&b, "      - name: %s\n", ev.Name)
				b.WriteString("        valueFrom:\n")
				b.WriteString("          secretKeyRef:\n")
				fmt.Fprintf(&b, "            name: %s\n", credSecretName)
				fmt.Fprintf(&b, "            key: %s\n", sanitizeKey(ev.Name))
			}
		}
	}

	// Storage entries
	var storageEntries []string

	// Secret file mounts
	if k8s := entry.K8s(); k8s != nil {
		for _, sm := range k8s.SecretMounts {
			content := r.FormValue("file-" + sm.SecretKey)
			if content != "" {
				secretName := instanceName + "-" + strings.TrimSuffix(sm.SecretKey, ".pem") + "-secret"
				var se strings.Builder
				fmt.Fprintf(&se, "      - path: %s\n", sm.MountPath)
				se.WriteString("        source:\n")
				se.WriteString("          type: Secret\n")
				se.WriteString("          secret:\n")
				fmt.Fprintf(&se, "            secretName: %s\n", secretName)
				se.WriteString("            items:\n")
				fmt.Fprintf(&se, "              - key: %s\n", sm.SecretKey)
				fmt.Fprintf(&se, "                path: %s\n", sm.SecretKey)
				storageEntries = append(storageEntries, se.String())
			}
		}
	}

	// ConfigMap ref
	configMapRef := r.FormValue("configmap-ref")
	configMapContent := r.FormValue("configmap-content")
	if configMapRef != "" {
		mountPath := "/etc/mcp-config"
		if entry.K8s() != nil && len(entry.K8s().ConfigMaps) > 0 && entry.K8s().ConfigMaps[0].MountPath != "" {
			mountPath = entry.K8s().ConfigMaps[0].MountPath
		}
		var se strings.Builder
		fmt.Fprintf(&se, "      - path: %s\n", mountPath)
		se.WriteString("        source:\n")
		se.WriteString("          type: ConfigMap\n")
		se.WriteString("          configMap:\n")
		fmt.Fprintf(&se, "            name: %s\n", configMapRef)
		storageEntries = append(storageEntries, se.String())
	} else if configMapContent != "" && entry.K8s() != nil && len(entry.K8s().ConfigMaps) > 0 {
		mountPath := entry.K8s().ConfigMaps[0].MountPath
		if mountPath == "" {
			mountPath = "/etc/mcp-config"
		}
		var se strings.Builder
		fmt.Fprintf(&se, "      - path: %s\n", mountPath)
		se.WriteString("        source:\n")
		se.WriteString("          type: ConfigMap\n")
		se.WriteString("          configMap:\n")
		fmt.Fprintf(&se, "            name: %s-config\n", instanceName)
		storageEntries = append(storageEntries, se.String())
	}

	if len(storageEntries) > 0 {
		b.WriteString("    storage:\n")
		for _, se := range storageEntries {
			b.WriteString(se)
		}
	}

	// Runtime
	sa := r.FormValue("service-account")
	runAsRoot := r.FormValue("run-as-root")
	if sa != "" || runAsRoot == "on" {
		b.WriteString("  runtime:\n")
		b.WriteString("    security:\n")
		if sa != "" {
			fmt.Fprintf(&b, "      serviceAccountName: %s\n", sa)
		}
		if runAsRoot == "on" {
			b.WriteString("      securityContext:\n")
			b.WriteString("        runAsNonRoot: false\n")
		}
	}

	return b.String()
}
