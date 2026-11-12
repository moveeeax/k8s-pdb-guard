package analyze

import (
	"strings"
	"testing"
)

func abs(v int) *IntOrPercent { return &IntOrPercent{Value: v} }
func pct(v int) *IntOrPercent { return &IntOrPercent{Value: v, IsPercent: true} }

func web(replicas int) Workload {
	return Workload{Namespace: "default", Name: "web", Kind: "Deployment", Replicas: replicas, Labels: map[string]string{"app": "web"}}
}

func findRule(fs []Finding, name, rule string) *Finding {
	for i := range fs {
		if fs[i].Name == name && fs[i].Rule == rule {
			return &fs[i]
		}
	}
	return nil
}

func TestMaxUnavailableZeroIsBlocking(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(0), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	f := findRule(fs, "web-pdb", RuleMaxUnavailableZero)
	if f == nil || !f.Blocking {
		t.Fatalf("expected blocking MaxUnavailableZero finding, got %+v", fs)
	}
	if f.AllowedDisruptions != 0 {
		t.Fatalf("expected 0 allowed disruptions, got %d", f.AllowedDisruptions)
	}
}

func TestMinAvailableEqualsReplicasIsBlocking(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MinAvailable: abs(3), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "web-pdb", RuleMinAvailableGERepl); f == nil || !f.Blocking {
		t.Fatalf("expected blocking MinAvailableGEReplicas finding, got %+v", fs)
	}
}

func TestMinAvailableGreaterThanReplicasIsBlocking(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MinAvailable: abs(5), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "web-pdb", RuleMinAvailableGERepl); f == nil || !f.Blocking {
		t.Fatalf("expected blocking finding for minAvailable > replicas, got %+v", fs)
	}
}

func TestHealthyMinAvailableIsClean(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MinAvailable: abs(2), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if HasBlocking(fs) {
		t.Fatalf("expected clean result, got %+v", fs)
	}
}

func TestHealthyMaxUnavailableIsClean(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if HasBlocking(fs) {
		t.Fatalf("expected clean result, got %+v", fs)
	}
}

func TestZeroPodsMatchedIsBlocking(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "ghost-pdb", MinAvailable: abs(1), Selector: map[string]string{"app": "does-not-exist"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "ghost-pdb", RuleZeroPodsMatched); f == nil || !f.Blocking {
		t.Fatalf("expected blocking ZeroPodsMatched finding, got %+v", fs)
	}
}

func TestMaxUnavailablePercentRoundsDownToZero(t *testing.T) {
	// 10% of 3 replicas rounds down to 0 -> blocking.
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: pct(10), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "web-pdb", RuleMaxUnavailableZero); f == nil || !f.Blocking {
		t.Fatalf("expected blocking finding for maxUnavailable 10%% of 3, got %+v", fs)
	}
}

func TestMinAvailablePercentRoundsUp(t *testing.T) {
	// 100% of 3 rounds up to 3 -> zero disruptions -> blocking.
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MinAvailable: pct(100), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "web-pdb", RuleMinAvailableGERepl); f == nil || !f.Blocking {
		t.Fatalf("expected blocking finding for minAvailable 100%%, got %+v", fs)
	}
}

func TestMinAvailablePercentHealthy(t *testing.T) {
	// 50% of 4 -> 2 required, 2 disruptions allowed -> clean.
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MinAvailable: pct(50), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{web(4)})
	if HasBlocking(fs) {
		t.Fatalf("expected clean result for minAvailable 50%% of 4, got %+v", fs)
	}
}

func TestOverlappingPDBsAreBlocking(t *testing.T) {
	pdbs := []PDB{
		{Namespace: "default", Name: "a", MinAvailable: abs(1), Selector: map[string]string{"app": "web"}},
		{Namespace: "default", Name: "b", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"}},
	}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "a", RuleOverlappingPDB); f == nil || !f.Blocking {
		t.Fatalf("expected overlap finding on a, got %+v", fs)
	}
	if f := findRule(fs, "b", RuleOverlappingPDB); f == nil || !f.Blocking {
		t.Fatalf("expected overlap finding on b, got %+v", fs)
	}
}

func TestEmptySelectorMatchesAll(t *testing.T) {
	// An empty (non-nil) selector matches every workload; maxUnavailable:0 blocks.
	pdbs := []PDB{{Namespace: "default", Name: "catch-all", MaxUnavailable: abs(0), Selector: map[string]string{}}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if f := findRule(fs, "catch-all", RuleZeroPodsMatched); f != nil {
		t.Fatalf("empty selector should not be flagged as zero-pods, got %+v", fs)
	}
	if f := findRule(fs, "catch-all", RuleMaxUnavailableZero); f == nil || !f.Blocking {
		t.Fatalf("expected MaxUnavailableZero for empty-selector catch-all, got %+v", fs)
	}
}

// livePod is a single-replica workload as built by the cluster package.
func livePod(name, node string, unready bool) Workload {
	w := Workload{Namespace: "default", Name: name, Kind: "Pod", Replicas: 1, Labels: map[string]string{"app": "web"}, Node: node}
	if unready {
		w.Unready = 1
	}
	return w
}

func TestNoDisruptionsAllowedNowNamesUnreadyPods(t *testing.T) {
	// Spec allows 1 of 3, but one pod is unready so the controller says 0.
	pdbs := []PDB{{
		Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"},
		Status: &PDBStatus{DisruptionsAllowed: 0, CurrentHealthy: 2, DesiredHealthy: 2, ExpectedPods: 3},
	}}
	pods := []Workload{livePod("web-a", "n1", false), livePod("web-b", "n2", false), livePod("web-c", "n3", true)}
	fs := Analyze(pdbs, pods)
	f := findRule(fs, "web-pdb", RuleNoDisruptionsNow)
	if f == nil || !f.Blocking {
		t.Fatalf("expected blocking NoDisruptionsAllowedNow finding, got %+v", fs)
	}
	if !strings.Contains(f.Message, `pod "web-c" is not ready`) {
		t.Fatalf("expected message to name web-c, got %q", f.Message)
	}
	if findRule(fs, "web-pdb", RuleMaxUnavailableZero) != nil {
		t.Fatalf("spec rule should not fire when the spec allows disruptions, got %+v", fs)
	}
}

func TestNoDisruptionsAllowedNowSkippedWhenStatusAllows(t *testing.T) {
	pdbs := []PDB{{
		Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"},
		Status: &PDBStatus{DisruptionsAllowed: 1, CurrentHealthy: 3, DesiredHealthy: 2, ExpectedPods: 3},
	}}
	fs := Analyze(pdbs, []Workload{livePod("web-a", "n1", false), livePod("web-b", "n2", false), livePod("web-c", "n3", false)})
	if len(fs) != 0 {
		t.Fatalf("expected no findings, got %+v", fs)
	}
}

func TestNoDisruptionsAllowedNowNotDuplicatedForSpecBlock(t *testing.T) {
	// maxUnavailable: 0 is already a permanent spec block; status 0 adds nothing.
	pdbs := []PDB{{
		Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(0), Selector: map[string]string{"app": "web"},
		Status: &PDBStatus{DisruptionsAllowed: 0, CurrentHealthy: 3, DesiredHealthy: 3, ExpectedPods: 3},
	}}
	fs := Analyze(pdbs, []Workload{web(3)})
	if findRule(fs, "web-pdb", RuleNoDisruptionsNow) != nil {
		t.Fatalf("expected only the spec finding, got %+v", fs)
	}
}

func TestNoDisruptionsAllowedNowWithoutUnreadyPods(t *testing.T) {
	// Status 0 while every matched pod is Ready: a replacement is missing.
	pdbs := []PDB{{
		Namespace: "default", Name: "web-pdb", MinAvailable: abs(1), Selector: map[string]string{"app": "web"},
		Status: &PDBStatus{DisruptionsAllowed: 0, CurrentHealthy: 2, DesiredHealthy: 2, ExpectedPods: 3},
	}}
	fs := Analyze(pdbs, []Workload{livePod("web-a", "n1", false), livePod("web-b", "n2", false)})
	f := findRule(fs, "web-pdb", RuleNoDisruptionsNow)
	if f == nil || !strings.Contains(f.Message, "missing or pending") {
		t.Fatalf("expected missing-pods hint, got %+v", fs)
	}
}

func TestUnhealthyPodEvictionPolicyUnsetWarns(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{livePod("web-a", "n1", false), livePod("web-b", "n1", true)})
	f := findRule(fs, "web-pdb", RuleUnhealthyPolicy)
	if f == nil || f.Blocking {
		t.Fatalf("expected non-blocking UnhealthyPodEvictionPolicyUnset warning, got %+v", fs)
	}
	if !strings.Contains(f.Message, "unset (IfHealthyBudget)") {
		t.Fatalf("expected unset policy in message, got %q", f.Message)
	}
}

func TestUnhealthyPodEvictionPolicyAlwaysAllowIsClean(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"}, UnhealthyPodEvictionPolicy: PolicyAlwaysAllow}}
	fs := Analyze(pdbs, []Workload{livePod("web-a", "n1", false), livePod("web-b", "n1", true)})
	if findRule(fs, "web-pdb", RuleUnhealthyPolicy) != nil {
		t.Fatalf("AlwaysAllow should not warn, got %+v", fs)
	}
}

func TestUnhealthyPodEvictionPolicyNoWarningWhenAllReady(t *testing.T) {
	pdbs := []PDB{{Namespace: "default", Name: "web-pdb", MaxUnavailable: abs(1), Selector: map[string]string{"app": "web"}}}
	fs := Analyze(pdbs, []Workload{livePod("web-a", "n1", false), livePod("web-b", "n1", false)})
	if len(fs) != 0 {
		t.Fatalf("expected no findings, got %+v", fs)
	}
}
