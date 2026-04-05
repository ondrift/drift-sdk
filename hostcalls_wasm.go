//go:build wasip1

package drift

// Host function imports from the "drift" WASM module namespace.
// These are implemented by the wasm-runner and provide I/O capabilities
// that WASI P1 cannot (networking, backbone access, etc.).
//
// Memory protocol:
//   - Guest→Host: pass (ptr, len) pairs pointing to data in guest linear memory.
//   - Host→Guest: host calls drift_alloc to allocate a buffer, writes response
//     there, and returns the response length as uint32. The guest reads from lastAlloc.

// hostBackboneRequest calls the backbone service.
// requestPtr/requestLen point to a JSON-encoded backboneRequest in guest memory.
// Returns the response length; response bytes are in lastAlloc.
//
//go:wasmimport drift backbone_request
func hostBackboneRequest(requestPtr, requestLen uint32) uint32

// hostHTTPRequest makes an outbound HTTP request.
// requestPtr/requestLen point to a JSON-encoded httpRequest in guest memory.
// Returns the response length; response bytes are in lastAlloc.
//
//go:wasmimport drift http_request
func hostHTTPRequest(requestPtr, requestLen uint32) uint32

// hostLogWrite sends a log message to the runner's log shipper.
// levelPtr/levelLen: log level string (e.g. "info", "error")
// msgPtr/msgLen: the log message
//
//go:wasmimport drift log_write
func hostLogWrite(levelPtr, levelLen, msgPtr, msgLen uint32)

// hostEnvGet reads an environment variable.
// keyPtr/keyLen point to the variable name in guest memory.
// Returns the value length; value bytes are in lastAlloc.
//
//go:wasmimport drift env_get
func hostEnvGet(keyPtr, keyLen uint32) uint32
