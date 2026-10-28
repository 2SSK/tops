package secret

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const password = "hunter2"

func TestValueNeverPrints(t *testing.T) {
	v := New(password)
	inStruct := struct{ Password Value }{v}

	tests := []struct {
		name string
		got  string
	}{
		{"String", v.String()},
		{"GoString", v.GoString()},
		{"%s", fmt.Sprintf("%s", v)},
		{"%v", fmt.Sprintf("%v", v)},
		{"%+v", fmt.Sprintf("%+v", v)},
		{"%#v", fmt.Sprintf("%#v", v)},
		{"%q", fmt.Sprintf("%q", v)},
		{"%x", fmt.Sprintf("%x", v)},
		{"%d", fmt.Sprintf("%d", v)},
		{"Sprint", fmt.Sprint(v)},
		{"in a struct", fmt.Sprintf("%+v", inStruct)},
		{"in a struct %#v", fmt.Sprintf("%#v", inStruct)},
		{"in a slice", fmt.Sprint([]Value{v})},
		{"in an error", fmt.Errorf("connect: %v", v).Error()},
		{"slog", logged(v)},
		{"json", marshal(t, inStruct)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.got, password) {
				t.Errorf("printed the secret: %s", tt.got)
			}
		})
	}
}

func TestLogValue(t *testing.T) {
	if got := logged(New(password)); !strings.Contains(got, "password=***") || !strings.Contains(got, `"password":"***"`) {
		t.Errorf("slog wrote %s, want password=*** and \"password\":\"***\"", got)
	}
}

func TestReveal(t *testing.T) {
	if got := New(password).Reveal(); got != password {
		t.Errorf("Reveal = %q, want %q", got, password)
	}
}

// logged returns what slog's text and JSON handlers write for one record with v.
func logged(v Value) string {
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("connect", "password", v)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("connect", "password", v)
	return buf.String()
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
