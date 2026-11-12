package analyze

import (
	"fmt"
	"sort"
	"strings"
)

// Drain simulates `kubectl drain <node>`: it runs Analyze over the whole input
// and keeps only the findings for PDBs that would refuse the eviction of at
// least one pod on the node. Blocking findings get the refused pods appended to
// their message. Workloads must be live pods (Kind "Pod") with Node set.
//
// Eviction rules follow the policy/v1 API: a pod covered by more than one PDB
// is always refused; a running but not-ready pod passes a budget with
// unhealthyPodEvictionPolicy AlwaysAllow, or with IfHealthyBudget while
// status.currentHealthy >= status.desiredHealthy; a Ready pod needs a
// disruption the budget allows.
func Drain(node string, pdbs []PDB, workloads []Workload) []Finding {
	var candidates []Workload
	for _, w := range workloads {
		if w.Node == node && !w.DrainExempt {
			candidates = append(candidates, w)
		}
	}

	// covering maps each PDB (namespace/name) to the candidate pods it selects;
	// coverCount counts how many PDBs select each candidate.
	covering := make(map[string][]Workload, len(pdbs))
	coverCount := make(map[string]int)
	byKey := make(map[string]PDB, len(pdbs))
	for _, p := range pdbs {
		key := p.Namespace + "/" + p.Name
		byKey[key] = p
		for _, w := range candidates {
			if w.Namespace == p.Namespace && selectorMatches(p.Selector, w.Labels) {
				covering[key] = append(covering[key], w)
				coverCount[w.Namespace+"/"+w.Name]++
			}
		}
	}

	var out []Finding
	for _, f := range Analyze(pdbs, workloads) {
		key := f.Namespace + "/" + f.Name
		pods := covering[key]
		if len(pods) == 0 {
			continue
		}
		if !f.Blocking {
			out = append(out, f)
			continue
		}
		p := byKey[key]
		var refused []string
		for _, w := range pods {
			if f.Rule == RuleOverlappingPDB {
				if coverCount[w.Namespace+"/"+w.Name] > 1 {
					refused = append(refused, w.Name)
				}
				continue
			}
			if w.Unready > 0 && unhealthyEvictable(p) {
				continue
			}
			refused = append(refused, w.Name)
		}
		if len(refused) == 0 {
			continue
		}
		sort.Strings(refused)
		f.Message += fmt.Sprintf("; refuses eviction of %s on node %s", strings.Join(quoteAll(refused), ", "), node)
		out = append(out, f)
	}
	return out
}

// unhealthyEvictable reports whether the API server lets a running but
// not-ready pod covered by p be evicted.
func unhealthyEvictable(p PDB) bool {
	switch p.UnhealthyPodEvictionPolicy {
	case PolicyAlwaysAllow:
		return true
	case "", "IfHealthyBudget":
		return p.Status != nil && p.Status.CurrentHealthy >= p.Status.DesiredHealthy
	default:
		// Unknown policies must be treated as disallowing unhealthy eviction.
		return false
	}
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}
