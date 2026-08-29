package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Building the binary under test means shelling out to `go build`, and that
// child inherits whatever CGO flags this process happens to carry. Under `make`
// they are exported; under a bare `go test ./...` they are not, and on macOS the
// child then dies at "'unicode/regex.h' file not found" — go-icu-regex binds
// ICU4C unconditionally and there is no ICU-free build tag. Deriving the flags
// here is what makes the suite runnable without the Makefile, and it is the only
// place in the tests that knows about ICU.

var (
	buildOnce  sync.Once
	binaryPath string
	buildErr   error
)

func bdcBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "bdc-e2e-")
		if err != nil {
			buildErr = err
			return
		}
		binaryPath = filepath.Join(dir, "bdc")
		cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/bdc")
		cmd.Dir = "../.."
		cmd.Env = buildEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("go build: %s", out)
		}
	})
	if buildErr != nil {
		t.Fatalf("building bdc: %v", buildErr)
	}
	return binaryPath
}

// buildEnv is this process's environment plus the ICU4C search paths, on the one
// platform that needs them and only when the caller has not already supplied
// them. Linux keeps system ICU on the default search path, so there is nothing
// to add there.
func buildEnv() []string {
	env := os.Environ()
	if runtime.GOOS != "darwin" {
		return env
	}
	if os.Getenv("CGO_CPPFLAGS") != "" || os.Getenv("CGO_LDFLAGS") != "" {
		return env
	}
	prefix := icu4cPrefix()
	if prefix == "" {
		// Nothing to add, and a guess would only replace the compiler's precise
		// error with a wrong path. Let the build report the missing header.
		return env
	}
	return append(env,
		"CGO_CPPFLAGS=-I"+filepath.Join(prefix, "include"),
		"CGO_LDFLAGS=-L"+filepath.Join(prefix, "lib"),
	)
}

// icu4cPrefix asks Homebrew the way the Makefile does, then falls back to the
// two standard Homebrew prefixes. A candidate counts only if it actually holds
// the header the build needs: `brew --prefix` answers for formulae that are not
// installed, and the keg-only versioned formula moves the real prefix.
func icu4cPrefix() string {
	candidates := []string{"/opt/homebrew/opt/icu4c", "/usr/local/opt/icu4c"}
	if out, err := exec.Command("brew", "--prefix", "icu4c").Output(); err == nil {
		candidates = append([]string{strings.TrimSpace(string(out))}, candidates...)
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "include", "unicode", "regex.h")); err == nil {
			return dir
		}
	}
	return ""
}
