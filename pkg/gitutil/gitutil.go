package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// CurrentBranch returns the current git branch name.
func CurrentBranch() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository or git not available")
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteURL returns the URL of the given remote (default "origin").
func RemoteURL(remote string) (string, error) {
	if remote == "" {
		remote = "origin"
	}
	out, err := exec.Command("git", "config", "--get", fmt.Sprintf("remote.%s.url", remote)).Output()
	if err != nil {
		return "", fmt.Errorf("no remote %q found", remote)
	}
	return strings.TrimSpace(string(out)), nil
}

// RepoName extracts the repo name from a git remote URL.
// Handles both SSH (git@github.com:org/repo.git) and HTTPS (https://github.com/org/repo.git).
func RepoName(remoteURL string) string {
	// Remove trailing .git
	url := strings.TrimSuffix(remoteURL, ".git")

	// SSH format: git@github.com:org/repo
	if strings.Contains(url, ":") && strings.HasPrefix(url, "git@") {
		parts := strings.Split(url, ":")
		if len(parts) == 2 {
			pathParts := strings.Split(parts[1], "/")
			if len(pathParts) >= 1 {
				return pathParts[len(pathParts)-1]
			}
		}
	}

	// HTTPS format: https://github.com/org/repo
	parts := strings.Split(url, "/")
	if len(parts) >= 1 {
		return parts[len(parts)-1]
	}

	return ""
}

// RepoOwnerAndName extracts "owner/repo" from a remote URL.
func RepoOwnerAndName(remoteURL string) string {
	url := strings.TrimSuffix(remoteURL, ".git")

	// SSH format
	if strings.Contains(url, ":") && strings.HasPrefix(url, "git@") {
		parts := strings.Split(url, ":")
		if len(parts) == 2 {
			return parts[1]
		}
	}

	// HTTPS format
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}

	return ""
}

// CurrentCommitSHA returns the current HEAD commit SHA.
func CurrentCommitSHA() (string, error) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	return strings.TrimSpace(string(out)), nil
}
