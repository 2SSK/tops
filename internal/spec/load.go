package spec

import (
	"bytes"
	"errors"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/term"
)

// Parse decodes a cluster spec; file names it in errors. YAML that does not parse is an
// error; unknown keys and wrong types are kept for Validate to report as problems.
func Parse(file string, data []byte) (*Cluster, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, invalidFile(file, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	c := Cluster{doc: &doc}
	var te *yaml.TypeError
	if err := dec.Decode(&c); errors.As(err, &te) {
		c.decoded = decodeProblems(&doc, te)
	} else if err != nil {
		return nil, invalidFile(file, err)
	}

	return &c, nil
}

// invalidFile is the error for a file that is not YAML, or is empty.
func invalidFile(file string, err error) error {
	return &term.UserError{
		Code: "spec.invalid",
		What: file + " is invalid",
		Why:  err.Error(),
		Fix:  "edit " + file,
	}
}
