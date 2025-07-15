package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/dagger/container-use/repository"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list [environment-id]",
	Short: "List all environments or snapshots for a specific environment",
	Long: `Display all active environments with their IDs, titles, and timestamps.
If an environment ID is provided, list its snapshots instead.
Use -q for environment IDs only, useful for scripting.`,
	RunE: func(app *cobra.Command, args []string) error {
		ctx := app.Context()
		repo, err := repository.Open(ctx, ".")
		if err != nil {
			return err
		}
		
		// If environment ID provided, list its snapshots
		if len(args) > 0 {
			return listSnapshots(app, repo, args[0])
		}
		
		envInfos, err := repo.List(ctx)
		if err != nil {
			return err
		}
		if quiet, _ := app.Flags().GetBool("quiet"); quiet {
			for _, envInfo := range envInfos {
				fmt.Println(envInfo.ID)
			}
			return nil
		}

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTITLE\tCREATED\tUPDATED")

		defer tw.Flush()
		for _, envInfo := range envInfos {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", envInfo.ID, truncate(app, envInfo.State.Title, 40), humanize.Time(envInfo.State.CreatedAt), humanize.Time(envInfo.State.UpdatedAt))
		}
		return nil
	},
}

func truncate(app *cobra.Command, s string, max int) string {
	if noTrunc, _ := app.Flags().GetBool("no-trunc"); noTrunc {
		return s
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func listSnapshots(app *cobra.Command, repo *repository.Repository, envID string) error {
	ctx := app.Context()
	env, err := repo.Get(ctx, nil, envID)
	if err != nil {
		return fmt.Errorf("failed to get environment: %w", err)
	}
	
	if quiet, _ := app.Flags().GetBool("quiet"); quiet {
		for _, snapshot := range env.State.Snapshots {
			fmt.Println(snapshot.ID)
		}
		return nil
	}
	
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SNAPSHOT ID\tMESSAGE\tTOOLS\tTIME\tGIT COMMIT")
	
	defer tw.Flush()
	for _, snapshot := range env.State.Snapshots {
		toolCount := fmt.Sprintf("%d tools", len(snapshot.Tools))
		if len(snapshot.Tools) == 1 {
			toolCount = "1 tool"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			snapshot.ID[:12],
			truncate(app, snapshot.Message, 40),
			toolCount,
			humanize.Time(snapshot.Timestamp),
			snapshot.GitCommit[:7])
	}
	return nil
}

func init() {
	listCmd.Flags().BoolP("quiet", "q", false, "Display only environment/snapshot IDs")
	listCmd.Flags().BoolP("no-trunc", "", false, "Don't truncate output")
	rootCmd.AddCommand(listCmd)
}
