package linear

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const defaultTimeout = 15 * time.Second

// CLIError represents a failure from a Linear CLI subprocess.
type CLIError struct {
	Command  string
	ExitCode int
	Stderr   string
}

func (e *CLIError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("linear cli error (exit %d): %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("linear cli error (exit %d) running: %s", e.ExitCode, e.Command)
}

// NotInstalledError indicates the CLI binary was not found.
type NotInstalledError struct {
	Tool string
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("linear cli not installed: %s", e.Tool)
}

// NotAuthenticatedError indicates the CLI is not authenticated.
type NotAuthenticatedError struct {
	Tool string
}

func (e *NotAuthenticatedError) Error() string {
	return fmt.Sprintf("linear cli not authenticated: %s", e.Tool)
}

// runCmd executes a CLI binary with the given args, optional API key, and a timeout.
// Returns stdout bytes on success or a typed error on failure.
func runCmd(binPath string, apiKey string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, args...)

	if apiKey != "" {
		cmd.Env = append(os.Environ(), "LINEAR_API_KEY="+apiKey)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return nil, &CLIError{
			Command:  fmt.Sprintf("%s %v", binPath, args),
			ExitCode: exitCode,
			Stderr:   stderr.String(),
		}
	}

	return stdout.Bytes(), nil
}

// lookPath finds a binary on PATH, returning its full path or empty string.
func lookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}
