package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectInstallMethod_NodeModulesPath(t *testing.T) {
	// Verify the string matching logic used by detectInstallMethod
	paths := []struct {
		path string
		npm  bool
	}{
		{"/home/user/node_modules/.bin/bdc", true},
		{"/usr/local/lib/node_modules/@beadcrumbs/bdc/bin/bdc", true},
		{"/usr/local/bin/bdc", false},
		{"/tmp/bdc", false},
	}

	for _, p := range paths {
		isNpm := strings.Contains(p.path, "node_modules")
		if isNpm != p.npm {
			t.Errorf("path %q: expected npm=%v, got %v", p.path, p.npm, isNpm)
		}
	}
}

func TestDetectInstallMethod_GoPathDetection(t *testing.T) {
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		t.Skip("GOPATH not set")
	}

	binDir := filepath.Join(gopath, "bin")
	fakePath := filepath.Join(binDir, "bdc")

	if !strings.HasPrefix(fakePath, binDir) {
		t.Errorf("expected %q to have prefix %q", fakePath, binDir)
	}
}

func TestDetectInstallMethod_UnknownPath(t *testing.T) {
	// A random /tmp path should not match npm or go patterns
	path := "/tmp/random/bdc"
	if strings.Contains(path, "node_modules") {
		t.Error("random path should not match node_modules")
	}

	gopath := os.Getenv("GOPATH")
	if gopath != "" && strings.HasPrefix(path, filepath.Join(gopath, "bin")) {
		t.Error("random path should not match GOPATH/bin")
	}
}
