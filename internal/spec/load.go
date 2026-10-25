package spec

import (
	"bytes"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/term"
)

// Parse decodes cluster spec; file names it in errors. Unknown keys are an error
// with their line number.
func Parse(file string, data []byte) (*Cluster, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var c Cluster
	if err := dec.Decode(&c); err != nil {
		return nil, &term.UserError{
			Code: "spec.invalid",
			What: file + " is invalid",
			Why:  err.Error(),
			Fix:  "edit " + file,
		}
	}
	return &c, nil
}

// Load reads the spec file at path, decodes it and applies Defaults.
func Load(path string) (*Cluster, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, &term.UserError{
			Code: "spec.unreadable",
			What: "cannot read " + path,
			Why:  err.Error(),
			Fix:  "run tops next to cluster.yaml, or pass -f <file>",
		}
	}

	c, err := Parse(path, data)
	if err != nil {
		return nil, err
	}
	Defaults(c)
	return c, nil
}
