package drift

// Env reads an environment variable from the WASM runner host.
// These are the user's secrets injected via the Kubernetes envSecretName.
// Returns an empty string if the variable is not set.
func Env(key string) string {
	keyPtr, keyLen := stringToPtr(key)
	respLen := hostEnvGet(keyPtr, keyLen)
	if respLen == 0 {
		return ""
	}
	return string(readHostResponse(respLen))
}
