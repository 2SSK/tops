package spec

import (
	"bytes"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/term"
)

// Parse decodes cluster.yaml. Unknown keys are an error with their line number.
func Parse(data []byte) (*Cluster, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var c Cluster
	if err := dec.Decode(&c); err != nil {
		return nil, &term.UserError{
			Code: "spec.invalid",
			What: "cluster.yaml cannot be read",
			Why:  err.Error(),
			Fix:  "edit cluster.yaml, then tops validate",
		}
	}
	return &c, nil
}
