package drift

import "os"

// Env reads an environment variable. In deployed mode, the runner injects
// the user's secrets as environment variables on the subprocess.
// Returns an empty string if the variable is not set.
func Env(key string) string {
	return os.Getenv(key)
}
