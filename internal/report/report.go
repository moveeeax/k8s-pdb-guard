// Package report renders analyze.Finding lists as human-readable text or JSON.
package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
)

// Result is the JSON envelope emitted with --output json.
type Result struct {
	Blocking int               `json:"blocking"`
	Warnings int               `json:"warnings"`
	Findings []analyze.Finding `json:"findings"`
}

func summarize(findings []analyze.Finding) Result {
	r := Result{Findings: findings}
	if r.Findings == nil {
		r.Findings = []analyze.Finding{}
	}
	for _, f := range findings {
		if f.Blocking {
			r.Blocking++
		} else {
			r.Warnings++
		}
	}
	return r
}

// JSON writes the findings as an indented JSON document.
func JSON(w io.Writer, findings []analyze.Finding) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(summarize(findings))
}

// Text writes a compact human-readable report. scope names what was scanned
// (e.g. a namespace label or a directory) for the header line.
func Text(w io.Writer, findings []analyze.Finding, scope string) {
	res := summarize(findings)
	if len(findings) == 0 {
		fmt.Fprintf(w, "OK: no blocking PodDisruptionBudgets found in %s\n", scope)
		return
	}
	for _, f := range findings {
		marker := "WARN "
		if f.Blocking {
			marker = "BLOCK"
		}
		fmt.Fprintf(w, "%s %s/%s [%s] %s\n", marker, f.Namespace, f.Name, f.Rule, f.Message)
	}
	fmt.Fprintf(w, "\n%d blocking, %d warning(s) in %s\n", res.Blocking, res.Warnings, scope)
}
