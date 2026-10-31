package catalog

import (
	"bytes"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Class is how a change to a key reaches a running server, with the evidence for it
// (backend schema class-<minor>.yaml).
type Class struct {
	Apply    string `yaml:"apply" json:"apply"`
	Evidence string `yaml:"evidence" json:"evidence,omitempty"`
	Lab      string `yaml:"lab" json:"lab,omitempty"`
}

// unknown is the class of a key nobody has classified: plan restarts its roles.
var unknown = Class{Apply: "unknown"}

// Class is the class of the setting named key, or unknown.
func (c *Catalog) Class(key string) Class {
	if cl, ok := c.classes[key]; ok {
		return cl
	}
	return unknown
}

// parseClasses reads a class file; every entry must name a setting of c, say hot or
// restart, cite evidence, and a hot one must say what the lab showed (or pending).
func parseClasses(file string, data []byte, c *Catalog) (map[string]Class, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	classes := map[string]Class{}
	if err := dec.Decode(&classes); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	for key, cl := range classes {
		if err := checkClass(c, key, cl); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", file, key, err)
		}
	}
	return classes, nil
}

func checkClass(c *Catalog, key string, cl Class) error {
	if s, ok := c.Lookup(key); !ok || s.Key != key {
		return fmt.Errorf("not a setting of Temporal %s", c.Temporal)
	}

	switch {
	case cl.Apply != "hot" && cl.Apply != "restart":
		return fmt.Errorf("apply is %q, want hot or restart", cl.Apply)

	case cl.Evidence == "":
		return fmt.Errorf("no evidence: cite the call site that reads it")

	case cl.Apply == "hot" && cl.Lab == "":
		return fmt.Errorf("hot needs lab: pending until the lab check")
	}

	return nil
}
