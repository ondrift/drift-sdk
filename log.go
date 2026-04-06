package drift

import (
	"fmt"
	"os"
)

// Log sends a log message at "info" level. The runner captures stderr
// and stores per-function logs.
func Log(msg string) {
	fmt.Fprintf(os.Stderr, "[info] %s\n", msg)
}

// LogError sends a log message at "error" level.
func LogError(msg string) {
	fmt.Fprintf(os.Stderr, "[error] %s\n", msg)
}

// LogWarn sends a log message at "warn" level.
func LogWarn(msg string) {
	fmt.Fprintf(os.Stderr, "[warn] %s\n", msg)
}
