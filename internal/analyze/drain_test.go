package analyze

import (
	"strings"
	"testing"
)

func TestDrainReportsOnlyPDBsOnTheNode(t *testing.T) {
	pdbs := []PDB{
		{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(0), Selector: map[string]string{"app": "web"}},
		{Namespace: "default", Name: "db-pdb", MaxUnavailable: abs(0), Selector: map[string]string{"app": "db"}},
		{Namespace: "default", Name: "ghost-pdb", MinAvailable: abs(1), Selector: map[string]string{"app": "ghost"}},
	}
	db := Workload{Namespace: "default", Name: "db-0", Kind: "Pod", Replicas: 1, Labels: map[string]string{"app": "db"}, Node: "worker-2"}
	pods := []Workload{livePod("web-a", "worker-1", false), livePod("web-b", "worker-2", false), db}

	fs := Drain("worker-1", pdbs, pods)
	if len(fs) != 1 || fs[0].Name != "web-pdb" || fs[0].Rule != RuleMaxUnavailableZero {
		t.Fatalf("expected only web-pdb MaxUnavailableZero, got %+v", fs)
	}
	if !strings.Contains(fs[0].Message, `refuses eviction of "web-a" on node worker-1`) {
		t.Fatalf("expected refused pod in message, got %q", fs[0].Message)
	}
	if strings.Contains(fs[0].Message, "web-b") {
		t.Fatalf("pod on another node should not be listed, got %q", fs[0].Message)
	}
}

func TestDrainIgnoresExemptPods(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(0), Selector: map[string]string{"app": "web"}}}
	ds := livePod("web-ds", "worker-1", false)
	ds.DrainExempt = true
	fs := Drain("worker-1", pdbs, []Workload{ds, livePod("web-b", "worker-2", false)})
	if len(fs) != 0 {
		t.Fatalf("DaemonSet/mirror pods are not evicted by drain, got %+v", fs)
	}
}

func TestDrainUnhealthyPodWithAlwaysAllowIsEvictable(t *testing.T) {
	// Status says 0 allowed because web-c is unready; with AlwaysAllow the
	// unready pod on the node can still be evicted, so the drain does not stall.
	pdbs := []PDB{{
		Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"},
		UnhealthyPodEvictionPolicy: PolicyAlwaysAllow,
		Status:                     &PDBStatus{DisruptionsAllowed: 0, CurrentHealthy: 2, DesiredHealthy: 2, ExpectedPods: 3},
	}}
	pods := []Workload{livePod("web-a", "worker-2", false), livePod("web-b", "worker-2", false), livePod("web-c", "worker-1", true)}
	if fs := Drain("worker-1", pdbs, pods); HasBlocking(fs) {
		t.Fatalf("expected no blocking finding, got %+v", fs)
	}
	// The Ready pods on worker-2 are still refused.
	fs := Drain("worker-2", pdbs, pods)
	f := findRule(fs, "web-pdb", RuleNoDisruptionsNow)
	if f == nil || !strings.Contains(f.Message, `refuses eviction of "web-a", "web-b" on node worker-2`) {
		t.Fatalf("expected Ready pods on worker-2 refused, got %+v", fs)
	}
}

func TestDrainUnhealthyPodIfHealthyBudget(t *testing.T) {
	// IfHealthyBudget: an unready pod is evictable only while currentHealthy >=
	// desiredHealthy. Here 1 < 2, so the eviction is refused.
	pdbs := []PDB{{
		Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"},
		Status: &PDBStatus{DisruptionsAllowed: 0, CurrentHealthy: 1, DesiredHealthy: 2, ExpectedPods: 3},
	}}
	pods := []Workload{livePod("web-a", "worker-2", false), livePod("web-b", "worker-1", true), livePod("web-c", "worker-2", true)}
	fs := Drain("worker-1", pdbs, pods)
	if f := findRule(fs, "web-pdb", RuleNoDisruptionsNow); f == nil || !f.Blocking {
		t.Fatalf("expected blocking finding, got %+v", fs)
	}
	if f := findRule(fs, "web-pdb", RuleUnhealthyPolicy); f == nil {
		t.Fatalf("expected policy warning to be kept, got %+v", fs)
	}
}

func TestDrainOverlapListsSharedPods(t *testing.T) {
	pdbs := []PDB{
		{Namespace: "default", Name: "a", MinAvailable: abs(1), Selector: map[string]string{"app": "web"}},
		{Namespace: "default", Name: "b", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"}},
	}
	pods := []Workload{livePod("web-a", "worker-1", false), livePod("web-b", "worker-2", false), livePod("web-c", "worker-2", false)}
	fs := Drain("worker-1", pdbs, pods)
	for _, name := range []string{"a", "b"} {
		f := findRule(fs, name, RuleOverlappingPDB)
		if f == nil || !strings.Contains(f.Message, `refuses eviction of "web-a"`) {
			t.Fatalf("expected overlap finding on %s naming web-a, got %+v", name, fs)
		}
	}
}

func TestDrainHealthyNodeIsClean(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"},
		Status: &PDBStatus{DisruptionsAllowed: 1, CurrentHealthy: 3, DesiredHealthy: 2, ExpectedPods: 3}}}
	pods := []Workload{livePod("web-a", "worker-1", false), livePod("web-b", "worker-2", false), livePod("web-c", "worker-3", false)}
	if fs := Drain("worker-1", pdbs, pods); len(fs) != 0 {
		t.Fatalf("expected clean drain, got %+v", fs)
	}
}
