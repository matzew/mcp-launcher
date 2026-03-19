package catalog

import (
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

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

func entryJSON(name, description string) string {
	e := ServerEntry{Name: name, Description: description}
	b, _ := json.Marshal(e)
	return string(b)
}

func TestList(t *testing.T) {
	tests := []struct {
		name       string
		configMaps []*corev1.ConfigMap
		wantCount  int
	}{
		{
			name:       "empty catalog",
			configMaps: nil,
			wantCount:  0,
		},
		{
			name: "single entry",
			configMaps: []*corev1.ConfigMap{
				makeConfigMap("server-a", "catalog-ns", entryJSON("server-a", "Server A"), true),
			},
			wantCount: 1,
		},
		{
			name: "multiple entries",
			configMaps: []*corev1.ConfigMap{
				makeConfigMap("server-a", "catalog-ns", entryJSON("server-a", "A"), true),
				makeConfigMap("server-b", "catalog-ns", entryJSON("server-b", "B"), true),
				makeConfigMap("server-c", "catalog-ns", entryJSON("server-c", "C"), true),
			},
			wantCount: 3,
		},
		{
			name: "skips unlabeled ConfigMaps",
			configMaps: []*corev1.ConfigMap{
				makeConfigMap("labeled", "catalog-ns", entryJSON("labeled", "L"), true),
				makeConfigMap("unlabeled", "catalog-ns", entryJSON("unlabeled", "U"), false),
			},
			wantCount: 1,
		},
		{
			name: "skips ConfigMaps missing server.json",
			configMaps: []*corev1.ConfigMap{
				makeConfigMap("no-data", "catalog-ns", "", true),
				makeConfigMap("has-data", "catalog-ns", entryJSON("has-data", "H"), true),
			},
			wantCount: 1,
		},
		{
			name: "skips ConfigMaps with invalid JSON",
			configMaps: []*corev1.ConfigMap{
				makeConfigMap("bad-json", "catalog-ns", "{invalid", true),
				makeConfigMap("good", "catalog-ns", entryJSON("good", "G"), true),
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset() //nolint:staticcheck // NewClientset requires generated apply configs
			for _, cm := range tt.configMaps {
				_, err := client.CoreV1().ConfigMaps("catalog-ns").Create(
					context.Background(), cm, metav1.CreateOptions{},
				)
				if err != nil {
					t.Fatalf("failed to create configmap: %v", err)
				}
			}

			store := NewStore(client, "catalog-ns")
			entries, err := store.List(context.Background())
			if err != nil {
				t.Fatalf("List() error: %v", err)
			}
			if len(entries) != tt.wantCount {
				t.Errorf("List() returned %d entries, want %d", len(entries), tt.wantCount)
			}
		})
	}
}

func TestGet(t *testing.T) {
	client := fake.NewSimpleClientset() //nolint:staticcheck // NewClientset requires generated apply configs
	cm := makeConfigMap("my-server", "catalog-ns", entryJSON("my-server", "My Server"), true)
	_, err := client.CoreV1().ConfigMaps("catalog-ns").Create(
		context.Background(), cm, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("failed to create configmap: %v", err)
	}

	store := NewStore(client, "catalog-ns")

	t.Run("found", func(t *testing.T) {
		entry, err := store.Get(context.Background(), "my-server")
		if err != nil {
			t.Fatalf("Get() error: %v", err)
		}
		if entry.Name != "my-server" {
			t.Errorf("Get() name = %q, want %q", entry.Name, "my-server")
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.Get(context.Background(), "nonexistent")
		if err == nil {
			t.Fatal("Get() expected error for nonexistent server, got nil")
		}
	})
}
