package catalog

import (
	"testing"
)

func TestK8s(t *testing.T) {
	tests := []struct {
		name    string
		entry   ServerEntry
		wantNil bool
	}{
		{
			name:    "nil meta",
			entry:   ServerEntry{Name: "test"},
			wantNil: true,
		},
		{
			name: "meta without k8s",
			entry: ServerEntry{
				Name: "test",
				Meta: &Meta{},
			},
			wantNil: true,
		},
		{
			name: "meta with k8s",
			entry: ServerEntry{
				Name: "test",
				Meta: &Meta{
					K8s: &KubernetesExtensions{DefaultPort: 8080},
				},
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.entry.K8s()
			if tt.wantNil && got != nil {
				t.Errorf("K8s() = %v, want nil", got)
			}
			if !tt.wantNil && got == nil {
				t.Error("K8s() = nil, want non-nil")
			}
		})
	}
}

func TestIsOneClick(t *testing.T) {
	tests := []struct {
		name  string
		entry ServerEntry
		want  bool
	}{
		{
			name:  "nil meta",
			entry: ServerEntry{Name: "test"},
			want:  false,
		},
		{
			name: "no crTemplate",
			entry: ServerEntry{
				Name: "test",
				Meta: &Meta{
					K8s: &KubernetesExtensions{DefaultPort: 8080},
				},
			},
			want: false,
		},
		{
			name: "has crTemplate",
			entry: ServerEntry{
				Name: "test",
				Meta: &Meta{
					K8s: &KubernetesExtensions{
						CRTemplate: map[string]any{"image": "test:latest"},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.IsOneClick(); got != tt.want {
				t.Errorf("IsOneClick() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrimaryPackage(t *testing.T) {
	t.Run("has packages", func(t *testing.T) {
		entry := ServerEntry{
			Packages: []Package{
				{Identifier: "first"},
				{Identifier: "second"},
			},
		}
		pkg := entry.PrimaryPackage()
		if pkg == nil {
			t.Fatal("PrimaryPackage() = nil, want non-nil")
		}
		if pkg.Identifier != "first" {
			t.Errorf("PrimaryPackage().Identifier = %q, want %q", pkg.Identifier, "first")
		}
	})

	t.Run("empty packages", func(t *testing.T) {
		entry := ServerEntry{}
		if pkg := entry.PrimaryPackage(); pkg != nil {
			t.Errorf("PrimaryPackage() = %v, want nil", pkg)
		}
	})
}

func TestDisplayTitle(t *testing.T) {
	tests := []struct {
		name  string
		entry ServerEntry
		want  string
	}{
		{
			name:  "has title",
			entry: ServerEntry{Name: "my-server", Title: "My Server"},
			want:  "My Server",
		},
		{
			name:  "falls back to name",
			entry: ServerEntry{Name: "my-server"},
			want:  "my-server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.entry.DisplayTitle(); got != tt.want {
				t.Errorf("DisplayTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}
