package indexer

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// runGit executes the git binary with the given arguments. It returns
// trimmed stdout on success and an error wrapping stderr on failure.
func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CheckGitVersion verifies that git is installed and at least version 2.25.0.
func CheckGitVersion() error {
	out, err := runGit("--version")
	if err != nil {
		return fmt.Errorf("git not found: %w", err)
	}

	// Expected format: "git version X.Y.Z" (may have extra suffix like "(Apple Git-143)")
	parts := strings.Fields(out)
	if len(parts) < 3 {
		return fmt.Errorf("unexpected git version output: %s", out)
	}

	versionStr := parts[2] // third token, e.g. "2.39.2"
	vParts := strings.SplitN(versionStr, ".", 3)
	if len(vParts) < 2 {
		return fmt.Errorf("cannot parse git version: %s", versionStr)
	}

	major, err := strconv.Atoi(vParts[0])
	if err != nil {
		return fmt.Errorf("cannot parse git major version: %s", vParts[0])
	}
	minor, err := strconv.Atoi(vParts[1])
	if err != nil {
		return fmt.Errorf("cannot parse git minor version: %s", vParts[1])
	}

	if major < 2 || (major == 2 && minor < 25) {
		return fmt.Errorf("git version %s is too old, need at least 2.25.0", versionStr)
	}
	return nil
}

// CloneNoCheckout performs a shallow, blobless clone without checking out files.
// This allows inspecting the commit SHA before materializing any file content.
func CloneNoCheckout(repoURL, destDir string) error {
	_, err := runGit("clone", "--no-checkout", "--depth", "1", "--filter=blob:none", "--", repoURL, destDir)
	if err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	return nil
}

// SparseCheckoutAndCheckout sets the sparse-checkout paths and then checks out
// the working tree. Call this after CloneNoCheckout.
func SparseCheckoutAndCheckout(repoDir string, paths []string) error {
	args := append([]string{"-C", repoDir, "sparse-checkout", "set"}, paths...)
	if _, err := runGit(args...); err != nil {
		return fmt.Errorf("sparse-checkout: %w", err)
	}

	if _, err := runGit("-C", repoDir, "checkout"); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}
	return nil
}

// CloneDocFolders clones a repo and checks out only the specified paths.
// It is a convenience wrapper around CloneNoCheckout + SparseCheckoutAndCheckout.
func CloneDocFolders(repoURL, destDir string, paths []string) error {
	if err := CloneNoCheckout(repoURL, destDir); err != nil {
		return err
	}
	return SparseCheckoutAndCheckout(destDir, paths)
}

// GetCommitSHA returns the 40-character hex SHA of HEAD for the repo at repoDir.
func GetCommitSHA(repoDir string) (string, error) {
	out, err := runGit("-C", repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("rev-parse: %w", err)
	}
	return out, nil
}
