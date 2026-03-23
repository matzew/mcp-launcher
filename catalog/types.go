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
	DefaultPort         int32          `json:"defaultPort,omitempty"`
	NeedsServiceAccount bool           `json:"needsServiceAccount,omitempty"`
	ServiceAccountHint  string         `json:"serviceAccountHint,omitempty"`
	RunAsRoot           bool           `json:"runAsRoot,omitempty"`
	ConfigMaps          []ConfigMap    `json:"configMaps,omitempty"`
	SecretMounts        []SecretMount  `json:"secretMounts,omitempty"`
	CRTemplate          map[string]any `json:"crTemplate,omitempty"`
}

// ConfigMap describes a ConfigMap the server needs.
type ConfigMap struct {
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
	DefaultContent string `json:"defaultContent,omitempty"`
	FileName       string `json:"fileName,omitempty"`
	MountPath      string `json:"mountPath,omitempty"`
	IsRequired     bool   `json:"isRequired,omitempty"`
}

// SecretMount describes a file that should be mounted from a Secret.
type SecretMount struct {
	SecretKey   string `json:"secretKey"`
	MountPath   string `json:"mountPath"`
	Description string `json:"description,omitempty"`
	IsRequired  bool   `json:"isRequired,omitempty"`
}

// IsOneClick returns true if this entry has a crTemplate and can be deployed without configuration.
func (e *ServerEntry) IsOneClick() bool {
	return e.K8s() != nil && e.K8s().CRTemplate != nil
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
