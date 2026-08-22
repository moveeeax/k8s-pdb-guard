// Package cluster reads PodDisruptionBudgets and pods from a live cluster with
// client-go and converts them into analyze types. Each live pod becomes a
// single-replica workload so the same rule engine applies to real clusters.
package cluster

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
)

// Client wraps a Kubernetes clientset.
type Client struct {
	cs kubernetes.Interface
}

// New builds a Client from the default kubeconfig loading rules, honouring
// KUBECONFIG and the supplied --context override.
func New(kubeContext string) (*Client, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build client: %w", err)
	}
	return &Client{cs: cs}, nil
}

// Fetch lists PDBs and pods from the given namespace ("" for all namespaces) and
// returns them as analyze inputs.
func (c *Client) Fetch(ctx context.Context, namespace string) ([]analyze.PDB, []analyze.Workload, error) {
	pdbList, err := c.cs.PolicyV1().PodDisruptionBudgets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list PDBs: %w", err)
	}
	podList, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list pods: %w", err)
	}

	var pdbs []analyze.PDB
	for _, p := range pdbList.Items {
		out := analyze.PDB{Namespace: p.Namespace, Name: p.Name}
		if p.Spec.MinAvailable != nil {
			out.MinAvailable = fromIntStr(*p.Spec.MinAvailable)
		}
		if p.Spec.MaxUnavailable != nil {
			out.MaxUnavailable = fromIntStr(*p.Spec.MaxUnavailable)
		}
		if p.Spec.Selector != nil {
			out.Selector = map[string]string{}
			for k, v := range p.Spec.Selector.MatchLabels {
				out.Selector[k] = v
			}
			out.HasMatchExpressions = len(p.Spec.Selector.MatchExpressions) > 0
		}
		pdbs = append(pdbs, out)
	}

	// Each pod is one replica. This lets the selector/replica math treat a live
	// cluster identically to parsed manifests.
	var workloads []analyze.Workload
	for _, pod := range podList.Items {
		workloads = append(workloads, analyze.Workload{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			Kind:      "Pod",
			Replicas:  1,
			Labels:    pod.Labels,
		})
	}
	return pdbs, workloads, nil
}

func fromIntStr(v intstr.IntOrString) *analyze.IntOrPercent {
	if v.Type == intstr.String {
		s := v.StrVal
		n := 0
		fmt.Sscanf(s, "%d%%", &n)
		return &analyze.IntOrPercent{Value: n, IsPercent: true}
	}
	return &analyze.IntOrPercent{Value: int(v.IntVal)}
}
