package drift

// Log sends a log message at "info" level to the WASM runner's log shipper,
// which batches and forwards it to the atomic proxy.
func Log(msg string) {
	levelPtr, levelLen := stringToPtr("info")
	msgPtr, msgLen := stringToPtr(msg)
	hostLogWrite(levelPtr, levelLen, msgPtr, msgLen)
}

// LogError sends a log message at "error" level.
func LogError(msg string) {
	levelPtr, levelLen := stringToPtr("error")
	msgPtr, msgLen := stringToPtr(msg)
	hostLogWrite(levelPtr, levelLen, msgPtr, msgLen)
}

// LogWarn sends a log message at "warn" level.
func LogWarn(msg string) {
	levelPtr, levelLen := stringToPtr("warn")
	msgPtr, msgLen := stringToPtr(msg)
	hostLogWrite(levelPtr, levelLen, msgPtr, msgLen)
}
