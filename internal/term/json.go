package term

import (
	"encoding/json"
	"errors"
	"io"
)

// errorDocument is the --json shape of any failure
type errorDocument struct {
	Kind        string    `json:"kind"`
	TopsVersion string    `json:"topsVersion"`
	Error       errorBody `json:"error"`
}

type errorBody struct {
	Code string `json:"code"`
	What string `json:"what"`
	Why  string `json:"why,omitempty"`
	Fix  string `json:"fix,omitempty"`
}

// WriteErrorJSON writes err as one JSON document of kind Error.
// Errors that are not a UserError get the code "unexpected".
func WriteErrorJSON(w io.Writer, version string, err error) error {
	body := errorBody{Code: "unexpected", What: err.Error()}

	var ue *UserError
	if errors.As(err, &ue) {
		body = errorBody{Code: ue.Code, What: ue.What, Why: ue.Why, Fix: ue.Fix}
	}

	return WriteJSON(w, errorDocument{Kind: "Error", TopsVersion: version, Error: body})
}

// WriteJSON writes doc as one JSON document followed by a newline.
// <, > and & stay readable: tops output never goes into HTML.
func WriteJSON(w io.Writer, doc any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}
