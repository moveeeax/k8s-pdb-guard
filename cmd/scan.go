package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
	"github.com/moveeeax/k8s-pdb-guard/internal/cluster"
)

func newScanCmd() *cobra.Command {
	var allNamespaces bool
	var namespace string
	var kubeContext string

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan PodDisruptionBudgets in a live cluster",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ns := namespace
			if allNamespaces {
				ns = ""
			}
			c, err := cluster.New(kubeContext)
			if err != nil {
				return err
			}
			pdbs, workloads, err := c.Fetch(context.Background(), ns)
			if err != nil {
				return err
			}
			findings := analyze.Analyze(pdbs, workloads)
			scope := "namespace " + namespace
			if allNamespaces {
				scope = "all namespaces"
			}
			if err := emit(cmd, findings, scope); err != nil {
				return err
			}
			if analyze.HasBlocking(findings) {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "scan every namespace")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to scan when --all-namespaces is not set")
	cmd.Flags().StringVar(&kubeContext, "context", "", "kubeconfig context to use")
	return cmd
}
