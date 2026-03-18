package catalog

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const labelSelector = "mcp.x-k8s.io/catalog-entry=true"

// Store reads catalog entries from labeled ConfigMaps.
type Store struct {
	client    kubernetes.Interface
	namespace string
}

// NewStore creates a catalog store that reads from ConfigMaps in the given namespace.
func NewStore(client kubernetes.Interface, namespace string) *Store {
	return &Store{
		client:    client,
		namespace: namespace,
	}
}

// List returns all catalog entries from labeled ConfigMaps.
func (s *Store) List(ctx context.Context) ([]ServerEntry, error) {
	configMaps, err := s.client.CoreV1().ConfigMaps(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing catalog configmaps: %w", err)
	}

	var entries []ServerEntry
	for _, cm := range configMaps.Items {
		data, ok := cm.Data["server.json"]
		if !ok {
			continue
		}
		var entry ServerEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Get returns a single catalog entry by server name.
func (s *Store) Get(ctx context.Context, name string) (*ServerEntry, error) {
	entries, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Name == name {
			return &e, nil
		}
	}
	return nil, fmt.Errorf("server %q not found in catalog", name)
}
