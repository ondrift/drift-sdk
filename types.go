package drift

import "encoding/json"

// Request is the incoming HTTP request passed to the function handler.
// The runner serializes the original HTTP request into this struct
// and writes it to the subprocess's stdin as JSON.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	Body    json.RawMessage   `json:"body"`
}

// Response is what the function handler returns. The runner reads
// this from the subprocess's stdout and converts it back into an HTTP response.
type Response struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Payload json.RawMessage `json:"payload"`
}

// backboneRequest is the internal envelope for backbone calls (local dev only).
type backboneRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}
