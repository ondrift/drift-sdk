//go:build wasip1

package drift

import "unsafe"

// driftAlloc is exported to the WASM host so it can allocate memory in
// the guest's linear memory for writing response data. The host calls this
// during a host function invocation, writes response bytes to the returned
// pointer, and the SDK reads from lastAlloc after the host function returns.
//
//go:wasmexport drift_alloc
func driftAlloc(size uint32) uint32 {
	lastAlloc = make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(lastAlloc))))
}
