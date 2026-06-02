# Kubernetes RBAC Configuration

This directory contains the required Kubernetes RBAC manifests for running Wings
in Kubernetes mode. These resources grant Wings the minimum permissions needed to
manage game server workloads as Pods within a namespace.

## Quick Start

```bash
# Apply all RBAC resources
kubectl apply -f kubernetes/

# Verify
kubectl get serviceaccount pelican-wings -n pelican
kubectl get role pelican-wings -n pelican
kubectl get rolebinding pelican-wings -n pelican
kubectl get clusterrole pelican-wings-metrics
kubectl get clusterrolebinding pelican-wings-metrics
```

## Architecture

Wings requires two levels of RBAC:

1. **Namespace-scoped (Role + RoleBinding)** — Manages Pods, Services, Jobs, and
   PVCs within the `pelican` namespace.
2. **Cluster-scoped (ClusterRole + ClusterRoleBinding)** — Reads Pod metrics from
   the `metrics.k8s.io` API (which is cluster-scoped).

## Required Permissions

### Namespace-scoped (pelican namespace)

| Resource                  | Verbs                          | Purpose                                  |
|---------------------------|--------------------------------|------------------------------------------|
| pods                      | get, create, delete, list      | Game server Pod lifecycle                |
| pods/log                  | get                            | Stream server console output             |
| pods/attach               | create                         | Interactive console (SPDY attach)        |
| services                  | get, create, update, delete    | NodePort Service management              |
| jobs                      | get, create, delete            | Egg installation scripts                 |
| configmaps                | get, create, delete            | Install script storage (multi-node)      |
| resourcequotas            | get, create, update            | Namespace resource limits (if enabled)   |
| limitranges               | get, create, update            | Container default limits (if enabled)    |
| persistentvolumeclaims    | get, create, update, delete, list | PVC storage lifecycle (if enabled)    |

### Cluster-scoped

| Resource                              | Verbs | Purpose                         |
|---------------------------------------|-------|---------------------------------|
| pods (metrics.k8s.io/v1beta1)         | get   | CPU/memory usage polling        |

## Files

- `namespace.yaml` — Namespace definition
- `serviceaccount.yaml` — ServiceAccount for Wings Pods
- `role.yaml` — Namespace-scoped Role with required permissions
- `rolebinding.yaml` — Binds the Role to the ServiceAccount
- `clusterrole-metrics.yaml` — Cluster-scoped access to metrics API
- `clusterrolebinding-metrics.yaml` — Binds the ClusterRole to the ServiceAccount

## Configuration

After applying these manifests, configure Wings to use the ServiceAccount:

```yaml
# config.yml
kubernetes:
  enabled: true
  namespace: pelican
  service_account: pelican-wings
```

If Wings runs **inside** the cluster as a Pod, assign the ServiceAccount directly
to the Wings Deployment/Pod. If Wings runs **outside** the cluster, create a
kubeconfig that authenticates as the ServiceAccount (or use a token).

## Customization

### Different namespace

Replace `pelican` with your namespace in all manifests:

```bash
sed -i 's/namespace: pelican/namespace: my-namespace/g' kubernetes/*.yaml
```

### Disable PVC permissions

If you only use `storage_mode: hostpath`, you can remove the
`persistentvolumeclaims` resource from `role.yaml`.

### Image pulling

By default, game server Pods and installation Jobs use `imagePullPolicy: Always`
for remote images so updated tags are re-pulled instead of reusing a stale node
cache (matching the Docker backend); `~`-prefixed local images are never pulled.
For air-gapped clusters, pin the policy:

```yaml
# config.yml
kubernetes:
  image_pull_policy: IfNotPresent   # or "Never"
```

### Disable metrics

If you don't have metrics-server installed, you can skip the ClusterRole and
ClusterRoleBinding. Wings will gracefully degrade (no CPU/memory stats).

### Multiple Wings instances

If you run multiple Wings nodes targeting different namespaces, create a Role and
RoleBinding per namespace, but share the ClusterRole/ClusterRoleBinding (it's
namespace-independent).
