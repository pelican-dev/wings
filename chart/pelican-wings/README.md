# Pelican Wings Helm Chart

Deploys [Pelican Wings](https://github.com/pelican-dev/wings) — the game server
management daemon — into a Kubernetes cluster with full RBAC, storage, and
networking support.

## Prerequisites

- Kubernetes 1.34+
- Helm 3.x
- A running [Pelican Panel](https://github.com/pelican-dev/panel) instance

## Quick Start

```bash
# Add your node credentials from the Panel
helm install wings ./chart/pelican-wings \
  --set wings.panelUrl=https://panel.example.com \
  --set wings.token=YOUR_TOKEN \
  --set wings.tokenId=YOUR_TOKEN_ID \
  --set wings.uuid=YOUR_NODE_UUID
```

## Configuration

See [values.yaml](values.yaml) for the full list of configurable values.

### Key Values

| Parameter | Description | Default |
|-----------|-------------|---------|
| `wings.panelUrl` | Panel URL | `https://panel.example.com` |
| `wings.token` | Panel authentication token | `""` |
| `wings.tokenId` | Panel token ID | `""` |
| `wings.uuid` | Node UUID from Panel | `""` |
| `wings.kubernetes.networkMode` | Port exposure: `hostport` or `nodeport` | `nodeport` |
| `wings.kubernetes.storageMode` | Storage: `hostpath` or `pvc` | `pvc` |
| `wings.kubernetes.storageClass` | StorageClass for PVCs | `""` (cluster default) |
| `wings.kubernetes.storageSize` | Default PVC size | `10Gi` |
| `wings.kubernetes.imagePullPolicy` | Pull policy for game server Pods/install Jobs: `Always`, `IfNotPresent`, `Never` | `""` (smart default) |
| `gameNamespace` | Namespace for game server resources | `pelican` |
| `rbac.create` | Create RBAC resources | `true` |
| `serviceAccount.create` | Create ServiceAccount | `true` |

### Storage

By default, the chart uses PVC-based storage (`storageMode: pvc`). This creates
a PersistentVolumeClaim per game server, enabling proper data lifecycle
management.

For single-node setups or testing, you can use HostPath:

```yaml
wings:
  kubernetes:
    storageMode: hostpath
```

### Networking

NodePort mode (default) creates a Kubernetes Service per game server, exposing
ports via cluster-assigned NodePorts:

```yaml
wings:
  kubernetes:
    networkMode: nodeport
    nodeportPreserve: true  # Try to use game port as NodePort
```

HostPort mode binds game server ports directly to the node:

```yaml
wings:
  kubernetes:
    networkMode: hostport
```

### Image pulling

Game server Pods and installation Jobs default to `imagePullPolicy: Always`
for remote images, so updated tags are re-pulled rather than reusing a stale
copy cached on the node (matching the Docker backend). `~`-prefixed local
images are never pulled. Override this for air-gapped clusters:

```yaml
wings:
  kubernetes:
    imagePullPolicy: IfNotPresent  # or "Never"
```

This is independent of `image.pullPolicy`, which applies to the Wings daemon
image itself.

## What Gets Created

- **Namespace** — `pelican` (configurable)
- **ServiceAccount** — For Wings and game server Pods
- **Role + RoleBinding** — Namespace-scoped permissions (Pods, Services, Jobs, PVCs)
- **ClusterRole + ClusterRoleBinding** — Metrics API access
- **ConfigMap** — Wings configuration file
- **Deployment** — Wings daemon with health probes
- **Service** — Exposes Wings API within the cluster

## Uninstalling

```bash
helm uninstall wings
```

Note: PVCs created for game servers are NOT automatically deleted when
uninstalling the chart. Delete them manually if you want to remove all data:

```bash
kubectl delete pvc -n pelican -l app.kubernetes.io/managed-by=pelican-wings
```
