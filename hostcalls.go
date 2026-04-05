package drift

import "unsafe"

// bytesToPtr returns a (pointer, length) pair for a byte slice suitable
// for passing to host functions.
func bytesToPtr(b []byte) (uint32, uint32) {
	if len(b) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b)))), uint32(len(b))
}

// stringToPtr returns a (pointer, length) pair for a string suitable
// for passing to host functions.
func stringToPtr(s string) (uint32, uint32) {
	if len(s) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.StringData(s)))), uint32(len(s))
}
