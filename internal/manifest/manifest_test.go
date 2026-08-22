package manifest

import (
	"path/filepath"
	"testing"

	"github.com/moveeeax/k8s-pdb-guard/internal/analyze"
)

const fixtures = "../../testdata"

func TestLoadBlockingManifest(t *testing.T) {
	pdbs, workloads, err := Load(filepath.Join(fixtures, "blocking.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pdbs) != 1 || len(workloads) != 1 {
		t.Fatalf("expected 1 pdb and 1 workload, got %d/%d", len(pdbs), len(workloads))
	}
	if pdbs[0].MaxUnavailable == nil || pdbs[0].MaxUnavailable.Value != 0 {
		t.Fatalf("expected maxUnavailable 0, got %+v", pdbs[0].MaxUnavailable)
	}
	if workloads[0].Replicas != 3 {
		t.Fatalf("expected 3 replicas, got %d", workloads[0].Replicas)
	}
	fs := analyze.Analyze(pdbs, workloads)
	if !analyze.HasBlocking(fs) {
		t.Fatalf("expected blocking finding from fixture, got %+v", fs)
	}
}

func TestLoadHealthyManifestIsClean(t *testing.T) {
	pdbs, workloads, err := Load(filepath.Join(fixtures, "healthy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if analyze.HasBlocking(analyze.Analyze(pdbs, workloads)) {
		t.Fatal("expected healthy fixture to be clean")
	}
}

func TestLoadZeroPodsManifest(t *testing.T) {
	pdbs, workloads, err := Load(filepath.Join(fixtures, "zeropods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fs := analyze.Analyze(pdbs, workloads)
	found := false
	for _, f := range fs {
		if f.Rule == analyze.RuleZeroPodsMatched && f.Blocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected ZeroPodsMatched finding, got %+v", fs)
	}
}

func TestLoadDirectoryScansAll(t *testing.T) {
	pdbs, _, err := Load(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	if len(pdbs) < 3 {
		t.Fatalf("expected to load PDBs from every fixture file, got %d", len(pdbs))
	}
}

func TestPercentSelectorParsing(t *testing.T) {
	pdbs, _, err := Load(filepath.Join(fixtures, "percent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pdbs) != 1 || pdbs[0].MinAvailable == nil {
		t.Fatalf("expected 1 pdb with minAvailable, got %+v", pdbs)
	}
	if !pdbs[0].MinAvailable.IsPercent || pdbs[0].MinAvailable.Value != 100 {
		t.Fatalf("expected 100%% minAvailable, got %+v", pdbs[0].MinAvailable)
	}
}
