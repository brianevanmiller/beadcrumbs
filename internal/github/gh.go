// Package github provides a wrapper around the gh CLI for PR operations.
// It is the only place in the codebase that calls out to the gh binary.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 15 * time.Second

// CLIError represents a failure from the gh CLI subprocess.
type CLIError struct {
	Command  string
	ExitCode int
	Stderr   string
}

func (e *CLIError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("gh cli error (exit %d): %s", e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("gh cli error (exit %d) running: %s", e.ExitCode, e.Command)
}

// NotInstalledError indicates gh CLI was not found on PATH.
type NotInstalledError struct{}

func (e *NotInstalledError) Error() string {
	return "gh cli not installed (https://cli.github.com)"
}

// NotAuthenticatedError indicates gh CLI is not logged in.
type NotAuthenticatedError struct{}

func (e *NotAuthenticatedError) Error() string {
	return "gh cli not authenticated (run: gh auth login)"
}

// PRInfo holds parsed PR data from gh pr view.
type PRInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Repo   string // "owner/repo" — derived from headRepositoryOwner + headRepository
}

// GH wraps the GitHub CLI for PR operations.
type GH struct {
	binPath string
}

// Detect finds the gh binary on PATH.
// Returns a ready-to-use GH instance or a NotInstalledError.
func Detect() (*GH, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, &NotInstalledError{}
	}
	return &GH{binPath: path}, nil
}

// BinPath returns the resolved path to the gh binary.
func (g *GH) BinPath() string { return g.binPath }

// CheckAuth verifies gh is authenticated.
func (g *GH) CheckAuth() error {
	_, err := g.run("auth", "status")
	if err != nil {
		return &NotAuthenticatedError{}
	}
	return nil
}

// CurrentBranchPR detects the open PR for the current git branch.
// Returns nil, nil if no PR exists for the current branch.
func (g *GH) CurrentBranchPR() (*PRInfo, error) {
	out, err := g.run("pr", "view", "--json", "number,title,state,url,headRepositoryOwner,headRepository")
	if err != nil {
		// gh pr view with no args returns exit 1 if no PR exists
		if cliErr, ok := err.(*CLIError); ok && cliErr.ExitCode == 1 {
			return nil, nil
		}
		return nil, err
	}
	return parsePRJSON(out)
}

// ViewPR fetches PR details. prRef can be a number, URL, or branch name.
func (g *GH) ViewPR(prRef string) (*PRInfo, error) {
	out, err := g.run("pr", "view", prRef, "--json", "number,title,state,url,headRepositoryOwner,headRepository")
	if err != nil {
		return nil, err
	}
	return parsePRJSON(out)
}

// AddComment posts a comment to a PR.
func (g *GH) AddComment(repo string, prNumber int, body string) error {
	args := []string{"pr", "comment", fmt.Sprintf("%d", prNumber), "--body", body}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	_, err := g.run(args...)
	return err
}

// run executes gh with the given args and a timeout.
func (g *GH) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, g.binPath, args...)
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
			Command:  fmt.Sprintf("gh %s", strings.Join(args, " ")),
			ExitCode: exitCode,
			Stderr:   strings.TrimSpace(stderr.String()),
		}
	}
	return stdout.Bytes(), nil
}

// parsePRJSON parses gh pr view --json output.
func parsePRJSON(data []byte) (*PRInfo, error) {
	var raw struct {
		Number              int    `json:"number"`
		Title               string `json:"title"`
		State               string `json:"state"`
		URL                 string `json:"url"`
		HeadRepositoryOwner struct {
			Login string `json:"login"`
		} `json:"headRepositoryOwner"`
		HeadRepository struct {
			Name string `json:"name"`
		} `json:"headRepository"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse gh output: %w", err)
	}

	pr := &PRInfo{
		Number: raw.Number,
		Title:  raw.Title,
		State:  raw.State,
		URL:    raw.URL,
	}
	if raw.HeadRepositoryOwner.Login != "" && raw.HeadRepository.Name != "" {
		pr.Repo = raw.HeadRepositoryOwner.Login + "/" + raw.HeadRepository.Name
	}
	return pr, nil
}
