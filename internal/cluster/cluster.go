// Package cluster reads PodDisruptionBudgets and pods from a live cluster with
// client-go and converts them into analyze types. Each live pod becomes a
// single-replica workload so the same rule engine applies to real clusters, and
// each PDB carries its live status so drains stuck on unready pods show up.
package cluster

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
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

	pdbs, workloads := convert(pdbList.Items, podList.Items)
	return pdbs, workloads, nil
}

// convert turns listed PDBs and pods into analyze inputs. Pods in a terminal
// phase (Succeeded/Failed) are dropped: they are neither healthy nor evicted.
func convert(pdbItems []policyv1.PodDisruptionBudget, podItems []corev1.Pod) ([]analyze.PDB, []analyze.Workload) {
	var pdbs []analyze.PDB
	for _, p := range pdbItems {
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
		if p.Spec.UnhealthyPodEvictionPolicy != nil {
			out.UnhealthyPodEvictionPolicy = string(*p.Spec.UnhealthyPodEvictionPolicy)
		}
		// Status is only trustworthy once the controller has observed the
		// current generation; otherwise leave it out and rely on the spec.
		if p.Status.ObservedGeneration >= p.Generation {
			out.Status = &analyze.PDBStatus{
				DisruptionsAllowed: int(p.Status.DisruptionsAllowed),
				CurrentHealthy:     int(p.Status.CurrentHealthy),
				DesiredHealthy:     int(p.Status.DesiredHealthy),
				ExpectedPods:       int(p.Status.ExpectedPods),
			}
		}
		pdbs = append(pdbs, out)
	}

	// Each pod is one replica. This lets the selector/replica math treat a live
	// cluster identically to parsed manifests.
	var workloads []analyze.Workload
	for _, pod := range podItems {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		w := analyze.Workload{
			Namespace:   pod.Namespace,
			Name:        pod.Name,
			Kind:        "Pod",
			Replicas:    1,
			Labels:      pod.Labels,
			Node:        pod.Spec.NodeName,
			DrainExempt: drainExempt(pod),
		}
		if pod.Status.Phase == corev1.PodRunning && !podReady(pod) {
			w.Unready = 1
		}
		workloads = append(workloads, w)
	}
	return pdbs, workloads
}

// podReady reports whether the pod has condition Ready=True, the definition of
// "healthy" the disruption controller uses.
func podReady(pod corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// drainExempt reports pods that `kubectl drain` skips instead of evicting:
// DaemonSet-owned pods and mirror (static) pods.
func drainExempt(pod corev1.Pod) bool {
	if _, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]; ok {
		return true
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller && ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
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
