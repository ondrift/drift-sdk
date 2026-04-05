package drift

// lastAlloc holds a reference to the most recently host-allocated buffer,
// preventing the Go GC from collecting it before the host writes to it.
var lastAlloc []byte

// readHostResponse reads the response written by the host into lastAlloc.
// length is the number of bytes the host wrote.
func readHostResponse(length uint32) []byte {
	if length == 0 || int(length) > len(lastAlloc) {
		return nil
	}
	result := make([]byte, length)
	copy(result, lastAlloc[:length])
	return result
}
