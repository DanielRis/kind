# KIND Configuration Extensions

This document describes the new configuration fields added to the KIND cluster config (`kind.x-k8s.io/v1alpha4`) for advanced networking, container runtime customization, and cluster bootstrapping.

## Table of Contents

- [Per-Node DNS Servers](#per-node-dns-servers)
- [Per-Node Extra Container Runtime Args](#per-node-extra-container-runtime-args)
- [Custom Docker Network](#custom-docker-network)
- [Per-Node Post-Create Commands](#per-node-post-create-commands)
- [Post-Ready Manifests](#post-ready-manifests)
- [Full Example](#full-example)

---

## Per-Node DNS Servers

**Field:** `nodes[].dns` (list of IP strings)

Configures custom DNS server IPs for individual node containers. Each entry is passed as a `--dns` flag to the container runtime (`docker run --dns <ip>`), overriding the default Docker embedded DNS (`127.0.0.11`).

This is useful when nodes need to resolve names against a custom DNS server (e.g., a mock cloud DNS) from the moment the container starts, including during `kubeadm init`/`join`.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  dns:
    - 172.18.0.100
    - 8.8.8.8
- role: worker
  dns:
    - 172.18.0.100
```

**Behavior:**
- When `dns` is set, Docker writes the specified IPs directly into the container's `/etc/resolv.conf` instead of `127.0.0.11`.
- The KIND entrypoint automatically detects this and skips its usual iptables-based DNS rewriting.
- If `dns` is not set, the default Docker embedded DNS behavior is preserved.

**Validation:** Each entry must be a valid IP address (IPv4 or IPv6).

---

## Per-Node Extra Container Runtime Args

**Field:** `nodes[].extraArgs` (list of strings)

A general-purpose escape hatch for passing arbitrary flags to the container runtime's `run`/`create` command. These are appended after all other KIND-managed arguments but before the image name.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraArgs:
    - "--add-host"
    - "myservice.local:172.18.0.50"
    - "--dns-opt"
    - "ndots:5"
```

**Use cases:**
- `--add-host` for custom `/etc/hosts` entries
- `--dns-search` for DNS search domains at the Docker level
- `--dns-opt` for custom DNS options
- `--ulimit` for resource limits
- Any other `docker run` flag that KIND does not natively support

**Note:** Use this when KIND does not have a dedicated config field for a particular Docker flag. For DNS servers specifically, prefer the `dns` field.

---

## Custom Docker Network

**Field:** `networking.dockerNetwork` (string)

Specifies the name of a pre-existing container runtime network to use instead of the default `kind` network. The network **must already exist** before creating the cluster.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  dockerNetwork: my-custom-network
```

**Behavior:**
- When set, KIND skips creating its default `kind` network and uses the specified network directly.
- If the network does not exist, cluster creation fails with a clear error message.
- This field takes precedence over the `KIND_EXPERIMENTAL_DOCKER_NETWORK` environment variable.
- KIND will **not** delete the custom network when the cluster is deleted.

**Validation:** The network name must match `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`.

**Creating a custom network:**

```bash
# Create a network with a specific subnet
docker network create \
  --driver bridge \
  --subnet 172.20.0.0/16 \
  --gateway 172.20.0.1 \
  my-custom-network

# Create the cluster using it
kind create cluster --config kind-config.yaml
```

This is useful when:
- You need a specific subnet range for your cluster nodes.
- Multiple KIND clusters need to share a network.
- External services (e.g., a mock DNS server) are already running on a specific Docker network.

---

## Per-Node Post-Create Commands

**Field:** `nodes[].postCreateCommands` (list of strings)

Shell commands executed inside the node container after it is created and running, but **before** `kubeadm init`/`join` runs. Each command is executed via `bash -c`.

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  postCreateCommands:
    - "echo 'nameserver 172.18.0.100' > /etc/resolv.conf"
    - "cp /custom-certs/ca.crt /usr/local/share/ca-certificates/ && update-ca-certificates"
    - "echo '172.18.0.50 registry.local' >> /etc/hosts"
```

**Execution timing:**

```
Container created → systemd ready → postCreateCommands → kubeadm config → kubeadm init/join
```

**Use cases:**
- Override `/etc/resolv.conf` before kubeadm starts
- Install custom CA certificates
- Add entries to `/etc/hosts`
- Configure containerd or other runtime settings
- Any pre-kubeadm container setup

**Note:** Commands run as root inside the container. If any command fails, cluster creation is aborted.

---

## Post-Ready Manifests

**Field:** `postReadyManifests` (list of YAML strings, on the Cluster object)

Kubernetes YAML manifests applied after the cluster reaches Ready state but **before** KIND returns control to the user. Each entry is an inline YAML string containing one or more Kubernetes resources (separated by `---`).

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
postReadyManifests:
- |
  apiVersion: v1
  kind: ConfigMap
  metadata:
    name: cluster-info
    namespace: default
  data:
    environment: "testing"
- |
  apiVersion: admissionregistration.k8s.io/v1
  kind: MutatingWebhookConfiguration
  metadata:
    name: my-webhook
  webhooks:
  - name: mutate.example.com
    clientConfig:
      url: https://webhook-service.default.svc:8443/mutate
      caBundle: <base64-encoded-ca>
    rules:
    - operations: ["CREATE"]
      apiGroups: [""]
      apiVersions: ["v1"]
      resources: ["pods"]
    admissionReviewVersions: ["v1"]
    sideEffects: None
```

**Execution timing:**

```
kubeadm init → CNI install → kubeadm join → wait for Ready → postReadyManifests → kind returns
```

**Guarantees:**
- The API server is fully operational when manifests are applied.
- All nodes have joined the cluster.
- Manifests are applied before KIND returns, so admission webhooks are active before any user workloads.
- This eliminates race conditions where pods could be created before webhooks are registered.

**Use cases:**
- Pre-baking MutatingWebhookConfigurations or ValidatingWebhookConfigurations
- Installing CRDs that must exist before any workloads
- Creating namespaces, RBAC rules, or ConfigMaps required by the test environment
- Any Kubernetes resources that must be present at cluster handoff

**Note:** Manifests are applied using `kubectl apply -f -` on the bootstrap control plane node. If any manifest fails to apply, cluster creation is aborted.

---

## Full Example

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  dockerNetwork: test-network
  podSubnet: "10.244.0.0/16"
  serviceSubnet: "10.96.0.0/16"
nodes:
- role: control-plane
  dns:
    - 172.18.0.100
  extraArgs:
    - "--add-host"
    - "cloudmock.local:172.18.0.100"
  postCreateCommands:
    - "update-ca-certificates"
- role: worker
  dns:
    - 172.18.0.100
  extraArgs:
    - "--add-host"
    - "cloudmock.local:172.18.0.100"
postReadyManifests:
- |
  apiVersion: admissionregistration.k8s.io/v1
  kind: MutatingWebhookConfiguration
  metadata:
    name: cloudmock-webhook
  webhooks:
  - name: mutate.cloudmock.local
    clientConfig:
      url: https://cloudmock.local:8443/mutate
    rules:
    - operations: ["CREATE"]
      apiGroups: [""]
      apiVersions: ["v1"]
      resources: ["pods"]
    admissionReviewVersions: ["v1"]
    sideEffects: None
```

This configuration:
1. Uses an existing Docker network `test-network` (where a CloudMock DNS server is running at `172.18.0.100`)
2. Points all nodes' DNS to the CloudMock DNS server
3. Adds a host entry so `cloudmock.local` resolves correctly
4. Installs CA certificates before kubeadm starts
5. Registers a mutating webhook after the cluster is ready, ensuring it is active before any user workloads
