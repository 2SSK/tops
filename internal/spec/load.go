package spec

import (
	"bytes"

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
