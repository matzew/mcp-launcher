package catalog

// ServerEntry represents an MCP server in the catalog.
type ServerEntry struct {
	Name                string       `json:"name"`
	Description         string       `json:"description"`
	Image               string       `json:"image"`
	DefaultPort         int32        `json:"defaultPort"`
	Transport           string       `json:"transport,omitempty"`
	Credentials         []Credential `json:"credentials,omitempty"`
	Args                []string     `json:"args,omitempty"`
	NeedsServiceAccount bool         `json:"needsServiceAccount,omitempty"`
	ServiceAccountHint  string       `json:"serviceAccountHint,omitempty"`
	ConfigMaps          []ConfigMap  `json:"configMaps,omitempty"`
}

// Credential describes a piece of configuration the server needs.
type Credential struct {
	// Type is one of: "env", "file", "arg"
	Type string `json:"type"`

	// Common fields
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`

	// For type "env"
	EnvName   string `json:"envName,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`

	// For type "file"
	SecretKey string `json:"secretKey,omitempty"`
	MountPath string `json:"mountPath,omitempty"`

	// For type "arg"
	Flag string `json:"flag,omitempty"`
}

// ConfigMap describes a ConfigMap the server needs.
type ConfigMap struct {
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
	DefaultContent string `json:"defaultContent,omitempty"`
	FileName       string `json:"fileName,omitempty"`
}
