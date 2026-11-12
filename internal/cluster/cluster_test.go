package cluster

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
)

func pod(name, node string, phase corev1.PodPhase, ready bool) *corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name, Labels: map[string]string{"app": "web"}},
		Spec:       corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}},
		},
	}
}

func TestFetchConvertsStatusAndReadiness(t *testing.T) {
	policy := policyv1.IfHealthyBudget
	maxUnavailable := intstr.FromInt32(1)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-pdb", Generation: 2},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable:             &maxUnavailable,
			Selector:                   &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			UnhealthyPodEvictionPolicy: &policy,
		},
		Status: policyv1.PodDisruptionBudgetStatus{
			ObservedGeneration: 2,
			DisruptionsAllowed: 0,
			CurrentHealthy:     2,
			DesiredHealthy:     2,
			ExpectedPods:       3,
		},
	}
	isController := true
	ds := pod("agent-x", "worker-1", corev1.PodRunning, true)
	ds.OwnerReferences = []metav1.OwnerReference{{Kind: "DaemonSet", Name: "agent", Controller: &isController}}
	mirror := pod("etcd-worker-1", "worker-1", corev1.PodRunning, true)
	mirror.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "abc"}

	c := &Client{cs: fake.NewClientset(
		pdb,
		pod("web-a", "worker-1", corev1.PodRunning, true),
		pod("web-b", "worker-2", corev1.PodRunning, true),
		pod("web-c", "worker-1", corev1.PodRunning, false),
		pod("job-done", "worker-1", corev1.PodSucceeded, false),
		ds,
		mirror,
	)}

	pdbs, workloads, err := c.Fetch(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(pdbs) != 1 {
		t.Fatalf("expected 1 PDB, got %d", len(pdbs))
	}
	got := pdbs[0]
	if got.UnhealthyPodEvictionPolicy != "IfHealthyBudget" {
		t.Fatalf("expected IfHealthyBudget policy, got %q", got.UnhealthyPodEvictionPolicy)
	}
	want := analyze.PDBStatus{DisruptionsAllowed: 0, CurrentHealthy: 2, DesiredHealthy: 2, ExpectedPods: 3}
	if got.Status == nil || *got.Status != want {
		t.Fatalf("expected status %+v, got %+v", want, got.Status)
	}

	byName := map[string]analyze.Workload{}
	for _, w := range workloads {
		byName[w.Name] = w
	}
	if _, ok := byName["job-done"]; ok {
		t.Fatalf("terminal pod should be dropped, got %+v", byName["job-done"])
	}
	if byName["web-c"].Unready != 1 || byName["web-a"].Unready != 0 {
		t.Fatalf("expected only web-c unready, got web-a=%d web-c=%d", byName["web-a"].Unready, byName["web-c"].Unready)
	}
	if byName["web-a"].Node != "worker-1" {
		t.Fatalf("expected node worker-1, got %q", byName["web-a"].Node)
	}
	if !byName["agent-x"].DrainExempt || !byName["etcd-worker-1"].DrainExempt || byName["web-a"].DrainExempt {
		t.Fatalf("expected DaemonSet and mirror pods drain-exempt only, got %+v", byName)
	}
}

func TestFetchDropsStaleStatus(t *testing.T) {
	minAvailable := intstr.FromInt32(1)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-pdb", Generation: 3},
		Spec:       policyv1.PodDisruptionBudgetSpec{MinAvailable: &minAvailable},
		Status:     policyv1.PodDisruptionBudgetStatus{ObservedGeneration: 2},
	}
	c := &Client{cs: fake.NewClientset(pdb)}
	pdbs, _, err := c.Fetch(context.Background(), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if pdbs[0].Status != nil {
		t.Fatalf("expected status dropped for unobserved generation, got %+v", pdbs[0].Status)
	}
}
