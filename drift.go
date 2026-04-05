package drift

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// Run is the entry point for Drift Atomic functions. The handler receives
// the incoming HTTP request and must return a response.
//
// In WASM mode (DRIFT_WASM_RUNTIME is set): reads a JSON request from stdin,
// calls the handler, and writes the JSON response to stdout. The WASM runner
// manages the HTTP server, proxy registration, and log shipping.
//
// In local dev mode (no DRIFT_WASM_RUNTIME): starts a local HTTP server on
// the port specified by the PORT env var (default 8080) so developers can
// test their functions with `drift atomic run` without needing wazero.
func Run(handler func(Request) Response) {
	if os.Getenv("DRIFT_WASM_RUNTIME") != "" {
		runWASM(handler)
	} else {
		runLocal(handler)
	}
}

// runWASM implements the WASM-mode protocol: read request from stdin, call
// handler, write response to stdout.
func runWASM(handler func(Request) Response) {
	var req Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		resp := Response{
			Status:  http.StatusBadRequest,
			Message: "failed to decode request",
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		return
	}

	resp := handler(req)
	json.NewEncoder(os.Stdout).Encode(resp)
}

// runLocal starts a local HTTP server for development and testing.
func runLocal(handler func(Request) Response) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if r.Body != nil {
			json.NewDecoder(r.Body).Decode(&body)
		}

		headers := make(map[string]string)
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}

		req := Request{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: headers,
			Query:   r.URL.RawQuery,
			Body:    body,
		}

		resp := handler(req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.Status)
		json.NewEncoder(w).Encode(map[string]any{
			"status":  resp.Status,
			"message": resp.Message,
			"payload": resp.Payload,
		})
	})

	fmt.Fprintf(os.Stderr, "drift-sdk: local server starting on :%s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Fprintf(os.Stderr, "drift-sdk: server error: %v\n", err)
		os.Exit(1)
	}
}
