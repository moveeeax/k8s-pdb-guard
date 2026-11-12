// Package cmd wires the k8s-pdb-guard cobra commands.
package cmd

import (
	"github.com/spf13/cobra"
)

// Version is stamped at build time via -ldflags.
var Version = "dev"

var outputFormat string

// NewRootCmd builds the root command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "k8s-pdb-guard",
		Short:         "Flag PodDisruptionBudgets that will block node drains and autoscaler rotation",
		Long:          "k8s-pdb-guard scans PodDisruptionBudgets in a live cluster or a directory of\nmanifests and flags the ones that make voluntary disruptions impossible:\nmaxUnavailable: 0, minAvailable >= replicas, selectors matching zero pods,\noverlapping PDBs, and (in a live cluster) budgets whose status allows no\ndisruptions right now. scan --node simulates a drain of one node. It exits\nnon-zero when any blocking PDB is found.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", "text", "output format: text|json")
	root.AddCommand(newScanCmd())
	root.AddCommand(newLintCmd())
	return root
}
