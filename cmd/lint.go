package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
	"github.com/moveeeax/k8s-pdb-guard/internal/manifest"
	"github.com/moveeeax/k8s-pdb-guard/internal/report"
)

func newLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint <path>",
		Short: "Lint PodDisruptionBudgets in a directory of YAML manifests (CI gate)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			pdbs, workloads, err := manifest.Load(path)
			if err != nil {
				return err
			}
			findings := analyze.Analyze(pdbs, workloads)
			if err := emit(cmd, findings, path); err != nil {
				return err
			}
			if analyze.HasBlocking(findings) {
				os.Exit(1)
			}
			return nil
		},
	}
}

// emit renders findings in the selected format and is shared by scan and lint.
func emit(cmd *cobra.Command, findings []analyze.Finding, scope string) error {
	switch outputFormat {
	case "json":
		return report.JSON(cmd.OutOrStdout(), findings)
	case "text":
		report.Text(cmd.OutOrStdout(), findings, scope)
		return nil
	default:
		return fmt.Errorf("unknown output format %q (want text|json)", outputFormat)
	}
}
