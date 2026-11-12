# k8s-pdb-guard

[![CI](https://github.com/moveeeax/k8s-pdb-guard/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/k8s-pdb-guard/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

Catch `PodDisruptionBudget`s that will silently block a node drain — before a
2 a.m. cordon hangs forever.

A misconfigured PDB makes `kubectl drain` and cluster-autoscaler node rotation
block **indefinitely**, and you usually find out mid-incident. `k8s-pdb-guard`
scans every PDB in a cluster (or a directory of manifests) and flags the ones
that make voluntary disruptions impossible, exiting non-zero so it can gate CI.

## What it flags

| Rule | Why it blocks drains |
| --- | --- |
| `MaxUnavailableZero` | `maxUnavailable: 0` (or a percentage that rounds down to 0) permits zero evictions. |
| `MinAvailableGEReplicas` | `minAvailable` ≥ the matched replica count leaves zero disruptions allowed. |
| `ZeroPodsMatched` | The selector matches no workloads — a labelling typo that protects nothing. |
| `OverlappingPDB` | A pod covered by two PDBs can never be evicted by the eviction API. |
| `NoDisruptionsAllowedNow` | `scan` only: the spec allows disruptions, but the live `status.disruptionsAllowed` is 0, usually because some matched pods are not Ready. The message names them. |
| `UnsupportedSelector` | Warning: `matchExpressions` are not evaluated by this MVP. |
| `UnhealthyPodEvictionPolicyUnset` | Warning: some matched pods are running but not Ready and `unhealthyPodEvictionPolicy` is not `AlwaysAllow`, so those pods cannot be evicted while the budget is unhealthy. |

Percentages follow Kubernetes rounding: `minAvailable` rounds **up**,
`maxUnavailable` rounds **down**.

## Install

Requires Go 1.26 or newer.

```shell
go install github.com/moveeeax/k8s-pdb-guard@latest
```

Or build from source:

```shell
git clone https://github.com/moveeeax/k8s-pdb-guard.git
cd k8s-pdb-guard
go build -o k8s-pdb-guard .
```

## Usage

Scan a live cluster (uses your current kubeconfig context):

```shell
# every namespace
k8s-pdb-guard scan --all-namespaces

# one namespace
k8s-pdb-guard scan -n prod
```

Simulate a drain of one node before you start it. Only PDBs that would refuse
evicting a pod on that node are reported, and each blocking finding lists the
refused pods. The node scan covers every namespace unless `-n` is given:

```shell
k8s-pdb-guard scan --node worker-03
```

```text
BLOCK shop/web-pdb [NoDisruptionsAllowedNow] status.disruptionsAllowed=0 (currentHealthy=2, desiredHealthy=2, expectedPods=3) although the spec allows 1; pod "web-7c9f-x2k" is not ready; drains block until the pods recover; refuses eviction of "web-7c9f-abc" on node worker-03
WARN  shop/web-pdb [UnhealthyPodEvictionPolicyUnset] unhealthyPodEvictionPolicy is unset (IfHealthyBudget) and pod "web-7c9f-x2k" is not ready; unready pods cannot be evicted while the budget is unhealthy, set unhealthyPodEvictionPolicy: AlwaysAllow

1 blocking, 1 warning(s) in node worker-03 (all namespaces)
```

Gate a directory of manifests in CI — no cluster needed:

```shell
k8s-pdb-guard lint ./manifests --output json
```

Example output:

```text
BLOCK shop/checkout-pdb [MaxUnavailableZero] maxUnavailable=0 resolves to 0 disruptions allowed across 3 replicas; drains will block forever
BLOCK shop/cache-pdb [MinAvailableGEReplicas] minAvailable=3 equals or exceeds the 3 matched replicas; zero disruptions allowed, drains will block forever

2 blocking, 0 warning(s) in ./manifests
```

The process exits `1` when any blocking PDB is found and `0` when clean, so it
drops straight into a CI step:

```yaml
- run: k8s-pdb-guard lint ./manifests
```

## How it works

The rule engine in `internal/analyze` is pure Go — no Kubernetes imports — so it
is trivially unit-tested against fixtures. For each PDB it resolves the selector
against the known workloads, sums the matched replicas, resolves
`minAvailable`/`maxUnavailable` (including percentages), and computes how many
voluntary disruptions are actually allowed. Anything that lands at zero is
blocking.

- `lint` parses PDBs plus `Deployment`/`StatefulSet`/`ReplicaSet` manifests off
  disk (`internal/manifest`) and derives replica counts from their specs.
- `scan` lists PDBs and live pods with `client-go` (`internal/cluster`),
  treating each pod as one replica so identical rules apply to a real cluster.
  Pods in `Succeeded`/`Failed` phase are skipped. It also reads the PDB status
  (`disruptionsAllowed`, `currentHealthy`, `desiredHealthy`, `expectedPods`)
  and pod readiness, which separates a temporarily unhealthy workload
  (`NoDisruptionsAllowedNow`) from a budget that can never allow an eviction.
  Status is ignored while `status.observedGeneration` is behind the PDB's
  generation, since the controller has not caught up yet.
- `scan --node` follows the eviction API rules: a pod covered by two PDBs is
  always refused; a Ready pod needs a disruption the budget allows; a running
  but not-Ready pod passes when `unhealthyPodEvictionPolicy` is `AlwaysAllow`,
  or under the default `IfHealthyBudget` while `currentHealthy >= desiredHealthy`.
  DaemonSet and mirror pods are skipped, as `kubectl drain` does not evict them.

## Development

```shell
go test ./...
go build ./...
```

## License

MIT — see [LICENSE](LICENSE).
