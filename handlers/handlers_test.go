package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/matzew/mcp-launcher/catalog"
)

// --- test helpers ---

func makeConfigMap(name, namespace, serverJSON string, labeled bool) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{},
	}
	if labeled {
		cm.Labels = map[string]string{
			"mcp.x-k8s.io/catalog-entry": "true",
		}
	}
	if serverJSON != "" {
		cm.Data["server.json"] = serverJSON
	}
	return cm
}

func entryJSON(e *catalog.ServerEntry) string {
	b, _ := json.Marshal(e)
	return string(b)
}

func newDynamicClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			mcpServerGVR: "MCPServerList",
		},
		objects...,
	)
}

func setupHandler(t *testing.T, catalogEntries []*corev1.ConfigMap, dynObjects ...runtime.Object) *Handler {
	t.Helper()
	client := fake.NewSimpleClientset() //nolint:staticcheck // NewClientset requires generated apply configs
	for _, cm := range catalogEntries {
		_, err := client.CoreV1().ConfigMaps(cm.Namespace).Create(
			context.Background(), cm, metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("failed to create configmap: %v", err)
		}
	}

	dynClient := newDynamicClient(dynObjects...)
	store := catalog.NewStore(client, "catalog-ns")
	h := New(store, client, dynClient, "default")
	h.templateDir = "../templates"
	return h
}

func simpleEntry(name string) *catalog.ServerEntry {
	return &catalog.ServerEntry{
		Name:        name,
		Description: "Test server " + name,
		Packages: []catalog.Package{
			{
				Identifier:   "quay.io/test/" + name + ":latest",
				RegistryType: "oci",
				Transport:    catalog.Transport{Type: "sse"},
			},
		},
		Meta: &catalog.Meta{
			K8s: &catalog.KubernetesExtensions{
				DefaultPort: 3001,
			},
		},
	}
}

func oneClickEntry(name string) *catalog.ServerEntry {
	e := simpleEntry(name)
	e.Meta.K8s.CRTemplate = map[string]any{
		"source": map[string]any{
			"type": "ContainerImage",
			"containerImage": map[string]any{
				"ref": "quay.io/test/" + name + ":latest",
			},
		},
		"config": map[string]any{
			"port": float64(3001),
			"path": "/mcp",
		},
	}
	return e
}

// --- parsePort tests ---

func TestParsePort(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		defaultPort int32
		want        int64
	}{
		{"default on empty", "", 8080, 8080},
		{"valid port", "3000", 8080, 3000},
		{"invalid port", "abc", 8080, 8080},
		{"zero returns default", "0", 8080, 8080},
		{"negative returns default", "-1", 8080, 8080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePort(tt.input, tt.defaultPort)
			if got != tt.want {
				t.Errorf("parsePort(%q, %d) = %d, want %d", tt.input, tt.defaultPort, got, tt.want)
			}
		})
	}
}

// --- sanitizeKey tests ---

func TestSanitizeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"UPPER_CASE", "upper-case"},
		{"already-lower", "already-lower"},
		{"MiXeD_CaSe", "mixed-case"},
		{"no_change", "no-change"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeKey(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- buildYAMLPreview tests ---

func TestBuildYAMLPreview(t *testing.T) {
	entry := &catalog.ServerEntry{
		Name: "test-server",
		Packages: []catalog.Package{
			{
				Identifier: "quay.io/test/server:latest",
				EnvironmentVariables: []catalog.EnvironmentVariable{
					{Name: "API_KEY", IsSecret: true},
				},
			},
		},
		Meta: &catalog.Meta{
			K8s: &catalog.KubernetesExtensions{
				DefaultPort: 8080,
			},
		},
	}

	form := url.Values{}
	form.Set("instance-name", "my-instance")
	form.Set("namespace", "test-ns")
	form.Set("image", "quay.io/custom:v1")
	form.Set("port", "9090")
	form.Set("env-API_KEY", "secret123")

	r := httptest.NewRequest("GET", "/preview/test-server?"+form.Encode(), nil)

	yaml := buildYAMLPreview(r, entry, "default")

	checks := []string{
		"apiVersion: mcp.x-k8s.io/v1alpha1",
		"kind: MCPServer",
		"name: my-instance",
		"namespace: test-ns",
		"type: ContainerImage",
		"ref: quay.io/custom:v1",
		"port: 9090",
		"path: /mcp",
		"name: API_KEY",
		"key: api-key",
	}
	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("YAML preview missing %q\nGot:\n%s", check, yaml)
		}
	}
}

func TestBuildYAMLPreviewRunAsRoot(t *testing.T) {
	entry := &catalog.ServerEntry{
		Name: "root-server",
		Packages: []catalog.Package{
			{Identifier: "quay.io/test/root:latest"},
		},
		Meta: &catalog.Meta{
			K8s: &catalog.KubernetesExtensions{
				DefaultPort: 8080,
			},
		},
	}

	form := url.Values{}
	form.Set("instance-name", "root-instance")
	form.Set("namespace", "test-ns")
	form.Set("image", "quay.io/test/root:latest")
	form.Set("port", "8080")
	form.Set("run-as-root", "on")

	r := httptest.NewRequest("GET", "/preview/root-server?"+form.Encode(), nil)

	yaml := buildYAMLPreview(r, entry, "default")

	checks := []string{
		"runtime:",
		"security:",
		"securityContext:",
		"runAsNonRoot: false",
	}
	for _, check := range checks {
		if !strings.Contains(yaml, check) {
			t.Errorf("YAML preview missing %q\nGot:\n%s", check, yaml)
		}
	}
}

func TestBuildYAMLPreviewDefaults(t *testing.T) {
	entry := &catalog.ServerEntry{
		Name: "test-server",
		Packages: []catalog.Package{
			{Identifier: "quay.io/test/server:latest"},
		},
		Meta: &catalog.Meta{
			K8s: &catalog.KubernetesExtensions{DefaultPort: 3001},
		},
	}

	r := httptest.NewRequest("GET", "/preview/test-server", nil)
	yaml := buildYAMLPreview(r, entry, "default")

	if !strings.Contains(yaml, "name: test-server") {
		t.Errorf("expected default instance name, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "namespace: default") {
		t.Errorf("expected default namespace, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "ref: quay.io/test/server:latest") {
		t.Errorf("expected default image from package, got:\n%s", yaml)
	}
}

// --- handler tests ---

func TestCatalog(t *testing.T) {
	t.Run("renders list", func(t *testing.T) {
		entry := simpleEntry("server-a")
		h := setupHandler(t, []*corev1.ConfigMap{
			makeConfigMap("server-a", "catalog-ns", entryJSON(entry), true),
		})

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		h.Catalog(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("Catalog() status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "server-a") {
			t.Error("Catalog() response does not contain server name")
		}
	})

	t.Run("empty catalog", func(t *testing.T) {
		h := setupHandler(t, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		h.Catalog(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("Catalog() status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestConfigure(t *testing.T) {
	entry := simpleEntry("my-server")

	t.Run("found", func(t *testing.T) {
		h := setupHandler(t, []*corev1.ConfigMap{
			makeConfigMap("my-server", "catalog-ns", entryJSON(entry), true),
		})

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/configure/my-server", nil)
		r.SetPathValue("name", "my-server")
		h.Configure(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("Configure() status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "my-server") {
			t.Error("Configure() response does not contain server name")
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := setupHandler(t, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/configure/nonexistent", nil)
		r.SetPathValue("name", "nonexistent")
		h.Configure(w, r)

		if w.Code != http.StatusNotFound {
			t.Errorf("Configure() status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})
}

func TestPreview(t *testing.T) {
	entry := simpleEntry("my-server")
	h := setupHandler(t, []*corev1.ConfigMap{
		makeConfigMap("my-server", "catalog-ns", entryJSON(entry), true),
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/preview/my-server", nil)
	r.SetPathValue("name", "my-server")
	h.Preview(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Preview() status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "MCPServer") {
		t.Error("Preview() response does not contain MCPServer YAML")
	}
}

func TestQuickDeploy(t *testing.T) {
	t.Run("creates resource", func(t *testing.T) {
		entry := oneClickEntry("quick-server")
		h := setupHandler(t, []*corev1.ConfigMap{
			makeConfigMap("quick-server", "catalog-ns", entryJSON(entry), true),
		})

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/deploy/quick-server", nil)
		r.SetPathValue("name", "quick-server")
		h.QuickDeploy(w, r)

		if w.Code != http.StatusSeeOther {
			t.Errorf("QuickDeploy() status = %d, want %d\nbody: %s", w.Code, http.StatusSeeOther, w.Body.String())
		}

		// Verify the MCPServer was created
		created, err := h.dynamicClient.Resource(mcpServerGVR).Namespace("default").Get(
			context.Background(), "quick-server", metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("MCPServer not created: %v", err)
		}
		image, _, _ := unstructured.NestedString(created.Object, "spec", "source", "containerImage", "ref")
		if image != "quay.io/test/quick-server:latest" {
			t.Errorf("MCPServer image = %q, want %q", image, "quay.io/test/quick-server:latest")
		}
	})

	t.Run("rejects non-one-click", func(t *testing.T) {
		entry := simpleEntry("config-server")
		h := setupHandler(t, []*corev1.ConfigMap{
			makeConfigMap("config-server", "catalog-ns", entryJSON(entry), true),
		})

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/deploy/config-server", nil)
		r.SetPathValue("name", "config-server")
		h.QuickDeploy(w, r)

		if w.Code != http.StatusBadRequest {
			t.Errorf("QuickDeploy() status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

func TestRunning(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		h := setupHandler(t, nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/running", nil)
		h.Running(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("Running() status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("lists servers", func(t *testing.T) {
		mcpServer := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "mcp.x-k8s.io/v1alpha1",
				"kind":       "MCPServer",
				"metadata": map[string]any{
					"name":      "running-server",
					"namespace": "default",
				},
				"spec": map[string]any{
					"source": map[string]any{
						"type": "ContainerImage",
						"containerImage": map[string]any{
							"ref": "quay.io/test/server:latest",
						},
					},
					"config": map[string]any{
						"port": int64(3001),
					},
				},
				"status": map[string]any{
					"phase": "Running",
				},
			},
		}

		h := setupHandler(t, nil, mcpServer)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/running", nil)
		h.Running(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("Running() status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "running-server") {
			t.Error("Running() response does not contain server name")
		}
	})

	t.Run("htmx partial", func(t *testing.T) {
		mcpServer := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "mcp.x-k8s.io/v1alpha1",
				"kind":       "MCPServer",
				"metadata": map[string]any{
					"name":      "htmx-server",
					"namespace": "default",
				},
				"spec": map[string]any{
					"source": map[string]any{
						"type": "ContainerImage",
						"containerImage": map[string]any{
							"ref": "quay.io/test/server:latest",
						},
					},
				},
			},
		}

		h := setupHandler(t, nil, mcpServer)

		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/running", nil)
		r.Header.Set("HX-Request", "true")
		h.Running(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("Running() htmx status = %d, want %d", w.Code, http.StatusOK)
		}
		if !strings.Contains(w.Body.String(), "htmx-server") {
			t.Error("Running() htmx response does not contain server name")
		}
	})
}

func TestDelete(t *testing.T) {
	mcpServer := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "mcp.x-k8s.io/v1alpha1",
			"kind":       "MCPServer",
			"metadata": map[string]any{
				"name":      "delete-me",
				"namespace": "default",
			},
			"spec": map[string]any{
				"source": map[string]any{
					"type": "ContainerImage",
					"containerImage": map[string]any{
						"ref": "quay.io/test/server:latest",
					},
				},
			},
		},
	}

	h := setupHandler(t, nil, mcpServer)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("DELETE", "/server/default/delete-me", nil)
	r.SetPathValue("namespace", "default")
	r.SetPathValue("name", "delete-me")
	h.Delete(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("Delete() status = %d, want %d\nbody: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Verify the MCPServer was deleted
	_, err := h.dynamicClient.Resource(mcpServerGVR).Namespace("default").Get(
		context.Background(), "delete-me", metav1.GetOptions{},
	)
	if err == nil {
		t.Error("MCPServer still exists after Delete()")
	}
}

func TestRun(t *testing.T) {
	t.Run("creates resource with env vars", func(t *testing.T) {
		entry := &catalog.ServerEntry{
			Name:        "full-server",
			Description: "Full test server",
			Packages: []catalog.Package{
				{
					Identifier:   "quay.io/test/full:latest",
					RegistryType: "oci",
					Transport:    catalog.Transport{Type: "sse"},
					EnvironmentVariables: []catalog.EnvironmentVariable{
						{Name: "API_KEY", IsSecret: true, IsRequired: true},
					},
				},
			},
			Meta: &catalog.Meta{
				K8s: &catalog.KubernetesExtensions{
					DefaultPort: 8080,
				},
			},
		}

		h := setupHandler(t, []*corev1.ConfigMap{
			makeConfigMap("full-server", "catalog-ns", entryJSON(entry), true),
		})

		form := url.Values{}
		form.Set("catalog-name", "full-server")
		form.Set("instance-name", "my-instance")
		form.Set("namespace", "default")
		form.Set("image", "quay.io/test/full:latest")
		form.Set("port", "8080")
		form.Set("env-API_KEY", "my-secret-key")

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/run", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.Run(w, r)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("Run() status = %d, want %d\nbody: %s", w.Code, http.StatusSeeOther, w.Body.String())
		}

		// Verify the MCPServer CR was created
		created, err := h.dynamicClient.Resource(mcpServerGVR).Namespace("default").Get(
			context.Background(), "my-instance", metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("MCPServer not created: %v", err)
		}
		image, _, _ := unstructured.NestedString(created.Object, "spec", "source", "containerImage", "ref")
		if image != "quay.io/test/full:latest" {
			t.Errorf("MCPServer image = %q, want %q", image, "quay.io/test/full:latest")
		}

		// Verify the Secret was created with env var
		secret, err := h.clientset.CoreV1().Secrets("default").Get(
			context.Background(), "my-instance-credentials", metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("Secret not created: %v", err)
		}
		if secret.StringData["api-key"] != "my-secret-key" {
			t.Errorf("Secret key api-key = %q, want %q", secret.StringData["api-key"], "my-secret-key")
		}
	})

	t.Run("run-as-root sets securityContext", func(t *testing.T) {
		entry := &catalog.ServerEntry{
			Name:        "root-server",
			Description: "Root test server",
			Packages: []catalog.Package{
				{
					Identifier:   "quay.io/test/root:latest",
					RegistryType: "oci",
					Transport:    catalog.Transport{Type: "sse"},
				},
			},
			Meta: &catalog.Meta{
				K8s: &catalog.KubernetesExtensions{
					DefaultPort: 8080,
				},
			},
		}

		h := setupHandler(t, []*corev1.ConfigMap{
			makeConfigMap("root-server", "catalog-ns", entryJSON(entry), true),
		})

		form := url.Values{}
		form.Set("catalog-name", "root-server")
		form.Set("instance-name", "root-instance")
		form.Set("namespace", "default")
		form.Set("image", "quay.io/test/root:latest")
		form.Set("port", "8080")
		form.Set("run-as-root", "on")

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/run", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		h.Run(w, r)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("Run() status = %d, want %d\nbody: %s", w.Code, http.StatusSeeOther, w.Body.String())
		}

		created, err := h.dynamicClient.Resource(mcpServerGVR).Namespace("default").Get(
			context.Background(), "root-instance", metav1.GetOptions{},
		)
		if err != nil {
			t.Fatalf("MCPServer not created: %v", err)
		}

		runAsNonRoot, found, err := unstructured.NestedBool(created.Object, "spec", "runtime", "security", "securityContext", "runAsNonRoot")
		if err != nil {
			t.Fatalf("failed to read runAsNonRoot: %v", err)
		}
		if !found {
			t.Fatal("spec.runtime.security.securityContext.runAsNonRoot not found")
		}
		if runAsNonRoot != false {
			t.Errorf("runAsNonRoot = %v, want false", runAsNonRoot)
		}
	})
}
