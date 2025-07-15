package main

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/dagger/container-use/environment"
	"github.com/dagger/container-use/repository"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start [environment-id] [-- claude-args...]",
	Short: "Start Claude in an environment with automatic snapshots",
	Long: `Start Claude in a containerized environment with automatic snapshot capabilities.
	
This starts an interactive Claude session in the environment's container.
The proxy automatically creates snapshots when tools complete.
	
Examples:
  container-use start                    # Start Claude in default environment
  container-use start myenv              # Start Claude in myenv environment
  container-use start myenv -- -c        # Continue last conversation in myenv
  container-use start -- --print "Hi"    # Use --print with default environment
  container-use start myenv --from-snapshot abc123  # Resume from specific snapshot`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		// Parse arguments
		envID := "default"
		claudeArgs := []string{}

		dashDashIndex := -1
		for i, arg := range args {
			if arg == "--" {
				dashDashIndex = i
				break
			}
		}

		if dashDashIndex == 0 {
			// "start -- args" - all args are for Claude
			claudeArgs = args[1:]
		} else if dashDashIndex > 0 {
			// "start env -- args"
			envID = args[0]
			claudeArgs = args[dashDashIndex+1:]
		} else if len(args) > 0 {
			// "start env" - just environment ID
			envID = args[0]
		}

		slog.Info("Starting session", "env", envID, "claude_args", claudeArgs)

		// Open repository
		repo, err := repository.Open(ctx, ".")
		if err != nil {
			return fmt.Errorf("failed to open repository: %w", err)
		}

		// Get or create environment
		env, err := repo.Get(ctx, nil, envID)
		if err != nil {
			// Create new environment
			slog.Info("Creating new environment", "id", envID)
			env, err = repo.Create(ctx, nil, envID, "Interactive Claude session")
			if err != nil {
				return fmt.Errorf("failed to create environment: %w", err)
			}
		}

		// Get worktree path
		worktree, err := repo.WorktreePath(env.ID)
		if err != nil {
			return fmt.Errorf("failed to get worktree path: %w", err)
		}

		// Set up snapshot callback to commit worktree changes
		env.SetSnapshotCallback(func(e *environment.Environment, message string) error {
			slog.Info("Snapshot created, committing worktree", "message", message)
			return repo.Update(ctx, e, fmt.Sprintf("Snapshot: %s", message))
		})

		// Check if resuming from snapshot
		snapshotID, _ := cmd.Flags().GetString("from-snapshot")
		if snapshotID != "" {
			// Find the snapshot
			var snapshot *environment.Snapshot
			for _, s := range env.State.Snapshots {
				if strings.HasPrefix(s.ID, snapshotID) {
					snapshot = &s
					break
				}
			}
			if snapshot == nil {
				return fmt.Errorf("snapshot %s not found", snapshotID)
			}
			
			slog.Info("Resuming from snapshot", "id", snapshot.ID[:12], "message", snapshot.Message)
			
			// Start session with snapshot image
			if err := env.StartSessionFromSnapshot(ctx, worktree, snapshot.ID, claudeArgs); err != nil {
				return fmt.Errorf("failed to start session from snapshot: %w", err)
			}
		} else {
			// Start normal session
			if err := env.StartSession(ctx, worktree, claudeArgs); err != nil {
				return fmt.Errorf("failed to start session: %w", err)
			}
		}

		// Update environment state after session
		if err := repo.Update(ctx, env, "Session completed"); err != nil {
			slog.Error("Failed to update environment state", "error", err)
		}

		return nil
	},
}

func init() {
	startCmd.Flags().String("from-snapshot", "", "Resume from a specific snapshot ID")
	rootCmd.AddCommand(startCmd)
}
