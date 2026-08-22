// Package analyze holds the pure PodDisruptionBudget analysis logic. It is
// deliberately free of any Kubernetes client or YAML dependency so the rules
// can be unit tested against plain fixtures. The cluster and manifest packages
// convert their inputs into these types before calling Analyze.
package analyze

import (
	"fmt"
	"sort"
)

// Rule identifiers reported on findings.
const (
	RuleMaxUnavailableZero  = "MaxUnavailableZero"
	RuleMinAvailableGERepl  = "MinAvailableGEReplicas"
	RuleZeroPodsMatched     = "ZeroPodsMatched"
	RuleOverlappingPDB      = "OverlappingPDB"
	RuleUnsupportedSelector = "UnsupportedSelector"
)

// IntOrPercent is a PDB minAvailable/maxUnavailable value. When IsPercent is
// true, Value is a percentage (0-100); otherwise it is an absolute count.
type IntOrPercent struct {
	Value     int
	IsPercent bool
}

func (v IntOrPercent) String() string {
	if v.IsPercent {
		return fmt.Sprintf("%d%%", v.Value)
	}
	return fmt.Sprintf("%d", v.Value)
}

// resolveMin resolves minAvailable against a replica count. Percentages round
// up, matching Kubernetes semantics for minAvailable.
func (v IntOrPercent) resolveMin(replicas int) int {
	if !v.IsPercent {
		return v.Value
	}
	// ceil(pct * replicas / 100)
	return (v.Value*replicas + 99) / 100
}

// resolveMax resolves maxUnavailable against a replica count. Percentages round
// down, matching Kubernetes semantics for maxUnavailable.
func (v IntOrPercent) resolveMax(replicas int) int {
	if !v.IsPercent {
		return v.Value
	}
	return (v.Value * replicas) / 100
}

// PDB is a namespaced PodDisruptionBudget reduced to the fields that matter for
// disruption analysis. Exactly one of MinAvailable / MaxUnavailable is normally
// set; if both are nil the PDB is inert.
type PDB struct {
	Namespace      string
	Name           string
	MinAvailable   *IntOrPercent
	MaxUnavailable *IntOrPercent
	// Selector is the matchLabels map. An empty (non-nil) map selects every
	// workload in the namespace, mirroring an empty LabelSelector in Kubernetes.
	Selector map[string]string
	// HasMatchExpressions records that the source selector used
	// matchExpressions, which this MVP does not evaluate.
	HasMatchExpressions bool
}

// Workload is a Deployment/StatefulSet/ReplicaSet (or a synthetic entry per live
// pod) that a PDB selector may match. Labels are the pod-template labels.
type Workload struct {
	Namespace string
	Name      string
	Kind      string
	Replicas  int
	Labels    map[string]string
}

// Finding is a single reported problem with a PDB.
type Finding struct {
	Namespace          string `json:"namespace"`
	Name               string `json:"name"`
	Rule               string `json:"rule"`
	Message            string `json:"message"`
	MatchedReplicas    int    `json:"matchedReplicas"`
	AllowedDisruptions int    `json:"allowedDisruptions"`
	Blocking           bool   `json:"blocking"`
}

// selectorMatches reports whether workload labels satisfy a matchLabels map. An
// empty selector matches everything (Kubernetes empty-selector semantics).
func selectorMatches(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// matchedWorkloads returns the workloads a PDB selects within its namespace and
// the total replica count across them.
func matchedWorkloads(p PDB, workloads []Workload) ([]Workload, int) {
	var matched []Workload
	total := 0
	for _, w := range workloads {
		if w.Namespace != p.Namespace {
			continue
		}
		if selectorMatches(p.Selector, w.Labels) {
			matched = append(matched, w)
			total += w.Replicas
		}
	}
	return matched, total
}

// Analyze evaluates every PDB against the known workloads and returns the
// findings sorted by namespace, name, then rule. A PDB may produce more than
// one finding.
func Analyze(pdbs []PDB, workloads []Workload) []Finding {
	var findings []Finding

	// Track which workloads each PDB covers, for overlap detection.
	covers := make([]map[string]struct{}, len(pdbs))

	for i, p := range pdbs {
		matched, replicas := matchedWorkloads(p, workloads)

		cover := make(map[string]struct{}, len(matched))
		for _, w := range matched {
			cover[w.Namespace+"/"+w.Kind+"/"+w.Name] = struct{}{}
		}
		covers[i] = cover

		if p.HasMatchExpressions {
			findings = append(findings, Finding{
				Namespace: p.Namespace, Name: p.Name,
				Rule:            RuleUnsupportedSelector,
				Message:         "selector uses matchExpressions; only matchLabels are evaluated, results may be incomplete",
				MatchedReplicas: replicas,
				Blocking:        false,
			})
		}

		// Zero-pod selector: a non-empty selector that matches nothing.
		if len(p.Selector) > 0 && len(matched) == 0 {
			findings = append(findings, Finding{
				Namespace: p.Namespace, Name: p.Name,
				Rule:            RuleZeroPodsMatched,
				Message:         "selector matches no workloads; the PDB protects nothing and hides a labelling mistake",
				MatchedReplicas: 0,
				Blocking:        true,
			})
			// No point computing disruption math with zero replicas.
			continue
		}

		switch {
		case p.MaxUnavailable != nil:
			allowed := p.MaxUnavailable.resolveMax(replicas)
			if allowed <= 0 {
				findings = append(findings, Finding{
					Namespace: p.Namespace, Name: p.Name,
					Rule:               RuleMaxUnavailableZero,
					Message:            fmt.Sprintf("maxUnavailable=%s resolves to %d disruptions allowed across %d replicas; drains will block forever", p.MaxUnavailable, allowed, replicas),
					MatchedReplicas:    replicas,
					AllowedDisruptions: allowed,
					Blocking:           true,
				})
			}
		case p.MinAvailable != nil:
			min := p.MinAvailable.resolveMin(replicas)
			allowed := replicas - min
			if replicas > 0 && allowed <= 0 {
				findings = append(findings, Finding{
					Namespace: p.Namespace, Name: p.Name,
					Rule:               RuleMinAvailableGERepl,
					Message:            fmt.Sprintf("minAvailable=%s equals or exceeds the %d matched replicas; zero disruptions allowed, drains will block forever", p.MinAvailable, replicas),
					MatchedReplicas:    replicas,
					AllowedDisruptions: allowed,
					Blocking:           true,
				})
			}
		}
	}

	// Overlap detection: two PDBs covering a common workload. Kubernetes denies
	// eviction of any pod covered by more than one PDB.
	for i := 0; i < len(pdbs); i++ {
		for j := i + 1; j < len(pdbs); j++ {
			if pdbs[i].Namespace != pdbs[j].Namespace {
				continue
			}
			shared := intersects(covers[i], covers[j])
			if shared == "" {
				continue
			}
			for _, k := range []int{i, j} {
				other := pdbs[j].Name
				if k == j {
					other = pdbs[i].Name
				}
				findings = append(findings, Finding{
					Namespace: pdbs[k].Namespace, Name: pdbs[k].Name,
					Rule:     RuleOverlappingPDB,
					Message:  fmt.Sprintf("shares workload %q with PDB %q; pods covered by two PDBs cannot be evicted", shared, other),
					Blocking: true,
				})
			}
		}
	}

	sort.SliceStable(findings, func(a, b int) bool {
		if findings[a].Namespace != findings[b].Namespace {
			return findings[a].Namespace < findings[b].Namespace
		}
		if findings[a].Name != findings[b].Name {
			return findings[a].Name < findings[b].Name
		}
		return findings[a].Rule < findings[b].Rule
	})
	return findings
}

func intersects(a, b map[string]struct{}) string {
	// Iterate the smaller set for stability and pick the lexicographically
	// smallest shared key so output is deterministic.
	best := ""
	for k := range a {
		if _, ok := b[k]; ok {
			if best == "" || k < best {
				best = k
			}
		}
	}
	return best
}

// HasBlocking reports whether any finding blocks voluntary disruptions.
func HasBlocking(findings []Finding) bool {
	for _, f := range findings {
		if f.Blocking {
			return true
		}
	}
	return false
}
