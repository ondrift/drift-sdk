package drift

import "encoding/json"

// Request is the incoming HTTP request passed to the function handler.
// The WASM runner serializes the original HTTP request into this struct
// and writes it to the module's stdin as JSON.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	Body    json.RawMessage   `json:"body"`
}

// Response is what the function handler returns. The WASM runner reads
// this from the module's stdout and converts it back into an HTTP response.
type Response struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Payload json.RawMessage `json:"payload"`
}

// backboneRequest is the internal envelope for backbone host calls.
type backboneRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// httpRequest is the internal envelope for outbound HTTP host calls.
type httpRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body,omitempty"`
}

// httpResponse is the response from the outbound HTTP host function.
type httpResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}
