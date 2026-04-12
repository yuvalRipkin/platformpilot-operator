# PlatformPilot Operator

A Kubernetes operator that automates dev environment provisioning for engineering teams. When a `DevEnvironment` custom resource is applied, the operator reconciles a fully isolated namespace with scoped RBAC, resource quotas sized to the requested tier, and a network policy that restricts cross-namespace traffic — removing the manual toil of wiring these up per team and per environment type.

---

## Architecture

```mermaid
flowchart TD
    A[DevEnvironment CRD\nteam / envType / tier / services] --> B[Reconciler]
    B --> C[Namespace\n&lt;team&gt;-&lt;envType&gt;]
    B --> D[RBAC\nRole + RoleBinding]
    B --> E[ResourceQuota\ntier-based limits]
    B --> F[NetworkPolicy\ningress-scoped]
```

---

## CRD Spec Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `team` | `string` | Yes | Team name — used as the namespace prefix and RBAC group subject |
| `envType` | `string` | Yes | Environment type: `dev` or `staging` |
| `tier` | `string` | Yes | Resource tier: `small`, `medium`, or `large` |
| `services` | `[]string` | No | Optional list of services to provision, e.g. `postgres`, `redis` |

Namespace naming convention: `<team>-<envType>` (e.g. `payments-dev`).

---

## Tier Sizing

| Tier | CPU Request | CPU Limit | Memory Request | Memory Limit | Max Pods |
|------|-------------|-----------|----------------|--------------|----------|
| `small` | 2 | 4 | 2 Gi | 4 Gi | 10 |
| `medium` | 4 | 8 | 8 Gi | 16 Gi | 20 |
| `large` | 8 | 16 | 16 Gi | 32 Gi | 50 |

---

## Quick Start

**Prerequisites:** Go 1.24+, kubectl, and a running Kubernetes cluster (e.g. kind, k3d).

**Install the CRD:**

```sh
make install
```

**Run the operator locally (outside the cluster):**

```sh
make run
```

**Apply a sample DevEnvironment:**

```sh
kubectl apply -f config/samples/
```

**Uninstall:**

```sh
make uninstall
```

---

## Example DevEnvironment

```yaml
apiVersion: platform.platformpilot.io/v1alpha1
kind: DevEnvironment
metadata:
  name: payments-dev
spec:
  team: payments
  envType: dev
  tier: medium
  services:
    - postgres
```

This creates:
- Namespace `payments-dev`
- Role `payments-dev-role` with full access to pods, services, deployments, ingresses, PVCs, configmaps, and secrets
- RoleBinding `payments-dev-rolebinding` binding the `payments` group to that role
- ResourceQuota `payments-dev-resourcequota` with medium-tier limits
- NetworkPolicy `payments-dev-networkpolicy` allowing intra-namespace ingress only

---

## Status

**Implemented**
- `DevEnvironment` CRD with kubebuilder validation markers
- Namespace provisioning with PlatformPilot labels
- Scoped RBAC (Role + RoleBinding) per team
- Tier-based ResourceQuota (small / medium / large)
- NetworkPolicy restricting ingress to same-namespace pods

**Planned**
- Status subresource updates (`phase`, `conditions`) after each reconcile step
- Finalizer to clean up owned resources on deletion
- Services provisioning (deploy postgres/redis StatefulSets from `spec.services`)
- Webhook validation (reject unknown tier/envType values at admission time)
- Controller tests with envtest

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Language | Go 1.24+ |
| Operator framework | [Kubebuilder](https://book.kubebuilder.io) v4 |
| Controller runtime | [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) v0.23 |
| CRD API version | `platform.platformpilot.io/v1alpha1` |

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
