package spec

import (
	"bytes"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/term"
)

// Parse decodes cluster spec; file names it in errors. Unknown keys are an error
// with their line number.
func Parse(file string, data []byte) (*Cluster, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, invalidFile(file, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	c := Cluster{doc: &doc}
	if err := dec.Decode(&c); err != nil {
		return nil, invalidFile(file, err)
	}

	return &c, nil
}

// invalidFile is the error for a file that does not decode: bad YAML, an unknown key
// or a wrong type.
func invalidFile(file string, err error) error {
	return &term.UserError{
		Code: "spec.invalid",
		What: file + " is invalid",
		Why:  err.Error(),
		Fix:  "edit " + file,
	}
}
