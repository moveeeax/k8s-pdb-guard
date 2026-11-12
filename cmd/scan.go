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
	var node string

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan PodDisruptionBudgets in a live cluster",
		Long: "Scan PodDisruptionBudgets in a live cluster. With --node, simulate a drain of\n" +
			"that node and report only the PDBs that would refuse evicting one of its pods;\n" +
			"the node scan covers every namespace unless --namespace is given.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ns := namespace
			if allNamespaces || (node != "" && !cmd.Flags().Changed("namespace")) {
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
			scope := "namespace " + namespace
			if ns == "" {
				scope = "all namespaces"
			}
			var findings []analyze.Finding
			if node != "" {
				findings = analyze.Drain(node, pdbs, workloads)
				scope = "node " + node + " (" + scope + ")"
			} else {
				findings = analyze.Analyze(pdbs, workloads)
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
	cmd.Flags().StringVar(&node, "node", "", "simulate a drain of this node and report the PDBs that would refuse its evictions")
	return cmd
}
