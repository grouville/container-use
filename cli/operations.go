// Package cli provides shared operations used by both CLI commands and tests
// This eliminates duplication and ensures consistent behavior
package cli

import (
	"context"
	"fmt"

	"github.com/dagger/container-use/environment"
	"github.com/dagger/container-use/repository"
)

// DeleteEnvironments performs the core delete operation
// Used by both 'cu delete' command and integration tests
func DeleteEnvironments(ctx context.Context, repoPath string, envIDs []string) error {
	for _, envID := range envIDs {
		repo, err := repository.Open(ctx, repoPath)
		if err != nil {
			return fmt.Errorf("failed to open repository: %w", err)
		}
		if err := repo.Delete(ctx, envID); err != nil {
			return fmt.Errorf("failed to delete environment: %w", err)
		}
		// Note: Printf is used here to match CLI output behavior
		// Tests can capture this via stdout redirection if needed
		fmt.Printf("Environment '%s' deleted successfully.\n", envID)
	}
	return nil
}

// ListEnvironments performs the core list operation
// Used by both 'cu list' command and integration tests
func ListEnvironments(ctx context.Context, repoPath string) ([]*environment.EnvironmentInfo, error) {
	repo, err := repository.Open(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return repo.List(ctx)
}

// CheckoutEnvironment performs the core checkout operation
// Used by both 'cu checkout' command and integration tests
func CheckoutEnvironment(ctx context.Context, repoPath string, envID, branch string) (string, error) {
	repo, err := repository.Open(ctx, repoPath)
	if err != nil {
		return "", err
	}
	return repo.Checkout(ctx, envID, branch)
}

// GetEnvironmentLog returns git log for an environment
// For tests only - CLI uses interactive git log with pagination
func GetEnvironmentLog(ctx context.Context, repoPath string, envID string) (string, error) {
	// Verify repository is valid
	if _, err := repository.Open(ctx, repoPath); err != nil {
		return "", err
	}

	// Get log using non-interactive format suitable for tests
	ref := fmt.Sprintf("container-use/%s", envID)
	return repository.RunGitCommand(ctx, repoPath, "log", "--oneline", "-10", ref)
}

// MergeEnvironment performs a merge operation
// For tests only - CLI uses interactive git merge that may require conflict resolution
func MergeEnvironment(ctx context.Context, repoPath string, envID string) error {
	// Verify environment exists
	repo, err := repository.Open(ctx, repoPath)
	if err != nil {
		return err
	}

	// Check if environment exists by attempting to list
	envs, err := repo.List(ctx)
	if err != nil {
		return err
	}

	found := false
	for _, env := range envs {
		if env.ID == envID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("environment %s not found", envID)
	}

	// Perform non-interactive merge
	ref := fmt.Sprintf("container-use/%s", envID)
	_, err = repository.RunGitCommand(ctx, repoPath, "merge", "--no-edit", ref)
	return err
}
