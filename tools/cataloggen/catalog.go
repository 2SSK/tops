package main

// catalog is the file internal/catalog embeds (backend schema "Settings catalog").
type catalog struct {
	Temporal      string    `json:"temporal"`
	GeneratedFrom string    `json:"generatedFrom"`
	Settings      []setting `json:"settings"`
}

// setting is one dynamic-config key.
type setting struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Precedence  string   `json:"precedence"`
	Constraints []string `json:"constraints"`
	Default     any      `json:"default"`
	DefaultExpr string   `json:"defaultExpr"`
	Description string   `json:"description"`
}
