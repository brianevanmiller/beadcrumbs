package linear

import (
	"strings"
)

// knownTools defines the detection order and binary names.
var knownTools = []struct {
	name   string
	binary string
	create func(binPath, apiKey string) Adapter
}{
	{"schpet", "linear", func(bp, ak string) Adapter { return NewSchpetAdapter(bp, ak) }},
	{"finesssee", "linear-cli", func(bp, ak string) Adapter { return NewFinessseeAdapter(bp, ak) }},
	{"linearis", "linearis", func(bp, ak string) Adapter { return NewLinearisAdapter(bp, ak) }},
}

// Detect finds the best available Linear CLI adapter.
// configTool overrides auto-detection if set (e.g., "schpet", "finesssee", "linearis").
// configPath overrides the binary path if set.
// apiKey is passed through to the adapter for LINEAR_API_KEY injection.
func Detect(configTool, configPath, apiKey string) (Adapter, error) {
	// If an explicit path is configured, use it with the configured (or default) tool type
	if configPath != "" {
		toolName := configTool
		if toolName == "" {
			toolName = "schpet" // default adapter for custom paths
		}
		return adapterForTool(toolName, configPath, apiKey)
	}

	// If a specific tool is configured, look for that tool only
	if configTool != "" {
		for _, t := range knownTools {
			if t.name == configTool {
				path := lookPath(t.binary)
				if path == "" {
					return nil, &NotInstalledError{Tool: t.name}
				}
				return t.create(path, apiKey), nil
			}
		}
		return nil, &NotInstalledError{Tool: configTool}
	}

	// Auto-detect: try each tool in preference order
	for _, t := range knownTools {
		path := lookPath(t.binary)
		if path == "" {
			continue
		}
		// For "linear" binary, verify it's schpet's tool (not some other "linear" binary)
		if t.name == "schpet" && !isSchpetLinear(path) {
			continue
		}
		return t.create(path, apiKey), nil
	}

	return nil, &NotInstalledError{Tool: "any"}
}

// DetectAll returns all installed Linear CLI adapters (for status display).
func DetectAll(apiKey string) []Adapter {
	var adapters []Adapter
	for _, t := range knownTools {
		path := lookPath(t.binary)
		if path == "" {
			continue
		}
		if t.name == "schpet" && !isSchpetLinear(path) {
			continue
		}
		adapters = append(adapters, t.create(path, apiKey))
	}
	return adapters
}

// adapterForTool creates an adapter by tool name with the given binary path.
func adapterForTool(name, binPath, apiKey string) (Adapter, error) {
	for _, t := range knownTools {
		if t.name == name {
			return t.create(binPath, apiKey), nil
		}
	}
	// Unknown tool name — treat as schpet-compatible
	return NewSchpetAdapter(binPath, apiKey), nil
}

// isSchpetLinear checks if the "linear" binary is @schpet/linear-cli
// by running `linear --version` and looking for identifying output.
func isSchpetLinear(binPath string) bool {
	out, err := runCmd(binPath, "", "--version")
	if err != nil {
		return false
	}
	version := strings.TrimSpace(string(out))
	// schpet's tool outputs something like "linear 1.8.1" or just a version
	// We accept it if it doesn't look like a completely different tool
	return version != "" && !strings.Contains(strings.ToLower(version), "linearis")
}
