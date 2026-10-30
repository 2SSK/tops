// Command cataloggen writes the settings catalog of one Temporal version from a source
// checkout: every dynamic-config key with its type, precedence, default, description,
// the roles that read it and a static hot/restart hint.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	src := flag.String("src", "", "Temporal source checkout, named v<version>")
	out := flag.String("out", "", "catalog file to write")
	flag.Parse()

	if err := run(*src, *out); err != nil {
		fmt.Fprintln(os.Stderr, "cataloggen:", err)
		os.Exit(1)
	}
}

func run(src, out string) error {
	if src == "" || out == "" {
		return fmt.Errorf("usage: cataloggen -src .cache/temporal/v<version> -out catalog.json")
	}
	version := strings.TrimPrefix(filepath.Base(src), "v")

	settings, err := load(src)
	if err != nil {
		return err
	}

	data, err := encode(catalog{
		Temporal:      version,
		GeneratedFrom: "github.com/temporalio/temporal@v" + version + " " + constantsFile,
		Settings:      settings,
	})
	if err != nil {
		return err
	}

	return os.WriteFile(out, data, 0o600)
}

// encode is the catalog as indented JSON witha final newline; <,> and & stay as they
// are so descriptions read as written.
func encode(c catalog) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
