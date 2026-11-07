package cmd

import (
	"github.com/airbuild/airbuild-cli/internal/agents"
	"github.com/airbuild/airbuild-cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	agentsDir   string
	agentsQuiet bool
)

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "Manage AI assistant files (AGENTS.md, CLAUDE.md, skills) in this project",
}

var agentsSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Refresh AirBuild-managed agent files written by an older CLI",
	Long: `Refresh the AirBuild-managed block in AGENTS.md / CLAUDE.md and the
airbuild-distribution / airbuild-codepush skills to match this CLI version.

Only files AirBuild already manages are touched — nothing is created, and
skill files you wrote yourself (no airbuild marker) are left alone. To create
the files in the first place, run ` + "`airbuild init --agents`" + `.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		updated, err := agents.Sync(agentsDir, serverBase(), cliVersion)
		if err != nil {
			return failf("Could not sync agent files: %v", err)
		}
		if jsonOutput {
			if updated == nil {
				updated = []string{}
			}
			ui.PrintJSON(map[string]any{"updated": updated, "version": cliVersion})
			return nil
		}
		found, _ := agents.Check(agentsDir, cliVersion)
		switch {
		case len(updated) > 0:
			ui.Success("Updated %d agent file(s) to %s:", len(updated), cliVersion)
			for _, p := range updated {
				ui.Muted("  %s", p)
			}
			ui.Info("Review and commit the changes.")
		case len(found) > 0:
			if !agentsQuiet {
				ui.Success("Agent files are already up to date (%s).", cliVersion)
			}
		case !agentsQuiet:
			ui.Info("No AirBuild agent files found here. Run `airbuild init --agents` to create them.")
		}
		return nil
	},
}

var agentsCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Exit non-zero if AirBuild agent files are older than this CLI (for CI)",
	RunE: func(cmd *cobra.Command, args []string) error {
		found, err := agents.Check(agentsDir, cliVersion)
		if err != nil {
			return failf("Could not check agent files: %v", err)
		}
		stale := 0
		for _, f := range found {
			if f.Stale {
				stale++
			}
		}
		if jsonOutput {
			if found == nil {
				found = []agents.Finding{}
			}
			ui.PrintJSON(map[string]any{"version": cliVersion, "files": found, "stale": stale})
		} else {
			if len(found) == 0 {
				ui.Info("No AirBuild agent files found here.")
			}
			for _, f := range found {
				state := "ok"
				if f.Stale {
					state = "STALE"
				}
				v := f.Version
				if v == "" {
					v = "unversioned"
				}
				ui.Info("%-6s %-5s %s (%s)", state, f.Kind, f.Path, v)
			}
		}
		if stale > 0 {
			ui.Warn("%d file(s) are out of date — run `airbuild agents sync`.", stale)
			return errSilent
		}
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{agentsSyncCmd, agentsCheckCmd} {
		c.Flags().StringVar(&agentsDir, "dir", ".", "Project directory")
	}
	agentsSyncCmd.Flags().BoolVar(&agentsQuiet, "quiet", false, "Print nothing unless files were updated")
	agentsCmd.AddCommand(agentsSyncCmd, agentsCheckCmd)
	rootCmd.AddCommand(agentsCmd)
}
