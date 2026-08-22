// Package manifest loads PodDisruptionBudgets and workloads from YAML files on
// disk and converts them into analyze types. It powers the `lint` command, which
// gates a manifest directory in CI without any cluster access.
package manifest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
)

type typeMeta struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

// Load walks a path (file or directory), parses every YAML document, and returns
// the PDBs and workloads found. Non-Kubernetes documents are ignored.
func Load(root string) ([]analyze.PDB, []analyze.Workload, error) {
	files, err := collect(root)
	if err != nil {
		return nil, nil, err
	}
	var pdbs []analyze.PDB
	var workloads []analyze.Workload
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		for _, doc := range splitDocs(raw) {
			if len(bytes.TrimSpace(doc)) == 0 {
				continue
			}
			var tm typeMeta
			if err := yaml.Unmarshal(doc, &tm); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", f, err)
			}
			switch tm.Kind {
			case "PodDisruptionBudget":
				var pdb policyv1.PodDisruptionBudget
				if err := yaml.Unmarshal(doc, &pdb); err != nil {
					return nil, nil, fmt.Errorf("%s: %w", f, err)
				}
				pdbs = append(pdbs, convertPDB(pdb))
			case "Deployment", "StatefulSet", "ReplicaSet":
				w, err := convertWorkload(tm.Kind, doc)
				if err != nil {
					return nil, nil, fmt.Errorf("%s: %w", f, err)
				}
				workloads = append(workloads, w)
			}
		}
	}
	return pdbs, workloads, nil
}

// splitDocs splits a YAML stream on `---` document separators.
func splitDocs(raw []byte) [][]byte {
	parts := bytes.Split(raw, []byte("\n---"))
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		out = append(out, bytes.TrimPrefix(p, []byte("---")))
	}
	return out
}

func collect(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func namespaceOrDefault(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}

func convertPDB(p policyv1.PodDisruptionBudget) analyze.PDB {
	out := analyze.PDB{
		Namespace: namespaceOrDefault(p.Namespace),
		Name:      p.Name,
	}
	if p.Spec.MinAvailable != nil {
		out.MinAvailable = fromIntStr(*p.Spec.MinAvailable)
	}
	if p.Spec.MaxUnavailable != nil {
		out.MaxUnavailable = fromIntStr(*p.Spec.MaxUnavailable)
	}
	out.Selector, out.HasMatchExpressions = fromSelector(p.Spec.Selector)
	return out
}

func convertWorkload(kind string, doc []byte) (analyze.Workload, error) {
	switch kind {
	case "Deployment":
		var d appsv1.Deployment
		if err := yaml.Unmarshal(doc, &d); err != nil {
			return analyze.Workload{}, err
		}
		return analyze.Workload{
			Namespace: namespaceOrDefault(d.Namespace), Name: d.Name, Kind: kind,
			Replicas: replicasOrOne(d.Spec.Replicas),
			Labels:   d.Spec.Template.Labels,
		}, nil
	case "StatefulSet":
		var s appsv1.StatefulSet
		if err := yaml.Unmarshal(doc, &s); err != nil {
			return analyze.Workload{}, err
		}
		return analyze.Workload{
			Namespace: namespaceOrDefault(s.Namespace), Name: s.Name, Kind: kind,
			Replicas: replicasOrOne(s.Spec.Replicas),
			Labels:   s.Spec.Template.Labels,
		}, nil
	default: // ReplicaSet
		var r appsv1.ReplicaSet
		if err := yaml.Unmarshal(doc, &r); err != nil {
			return analyze.Workload{}, err
		}
		return analyze.Workload{
			Namespace: namespaceOrDefault(r.Namespace), Name: r.Name, Kind: kind,
			Replicas: replicasOrOne(r.Spec.Replicas),
			Labels:   r.Spec.Template.Labels,
		}, nil
	}
}

func replicasOrOne(r *int32) int {
	if r == nil {
		return 1
	}
	return int(*r)
}

func fromIntStr(v intstr.IntOrString) *analyze.IntOrPercent {
	if v.Type == intstr.String {
		s := strings.TrimSuffix(v.StrVal, "%")
		n := 0
		fmt.Sscanf(s, "%d", &n)
		return &analyze.IntOrPercent{Value: n, IsPercent: true}
	}
	return &analyze.IntOrPercent{Value: int(v.IntVal)}
}

func fromSelector(sel *metav1.LabelSelector) (map[string]string, bool) {
	if sel == nil {
		return nil, false
	}
	m := map[string]string{}
	for k, v := range sel.MatchLabels {
		m[k] = v
	}
	return m, len(sel.MatchExpressions) > 0
}
