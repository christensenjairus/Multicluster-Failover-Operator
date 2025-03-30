# Multicluster Failover Operator

A Kubernetes operator designed to manage failover between multiple Kubernetes clusters. This operator uses the multicluster-runtime framework to orchestrate resources across clusters for high availability.

![Multicluster Failover Operator excalidraw](https://github.com/user-attachments/assets/33b95c8e-bb5a-4a9f-965e-aa52aad1a9ca)

## Overview

The Multicluster Failover Operator watches `FailoverGroup` and `Failover` custom resources to manage the replication and failover of workloads between Kubernetes clusters. It's designed for scenarios where you need to maintain high availability across multiple distinct Kubernetes clusters.

## Architecture

The operator is built using the [Operator SDK](https://sdk.operatorframework.io/) and leverages the [multicluster-runtime](https://github.com/multicluster-runtime/multicluster-runtime) framework for managing resources across multiple clusters.

Key components:

- **FailoverGroup**: Defines a group of resources that should fail over together
- **Failover**: Represents a specific failover event or configuration
- **KubeconfigProvider**: Discovers and manages connections to remote clusters through Kubernetes Secrets containing kubeconfig files

## Installation

### Prerequisites

- Kubernetes cluster(s) running version v1.24.0 or later
- kubectl v1.24.0 or later
- Go 1.20 or later (for development only)

### Installing the Operator

1. Clone the repository:
```
git clone https://github.com/christensenjairus/Multicluster-Failover-Operator.git
cd Multicluster-Failover-Operator
```

2. Install the CRDs:
```
kubectl apply -f config/crd/bases
```

3. Install the operator:
```
kubectl apply -f config/samples/operator.yaml
```

## Usage

### Setting Up Cluster Access

The operator needs access to each cluster it will manage. This includes both the local cluster and any remote clusters. You can use the provided script to set up access for any cluster:

```bash
# For the local cluster
./scripts/create-kubeconfig-secret.sh -n local-cluster

# For a remote cluster (assuming your kubectl context is set to the target cluster)
kubectl config use-context remote-cluster
./scripts/create-kubeconfig-secret.sh -n remote-cluster
```

The script will:
1. Create necessary RBAC rules (ServiceAccount, ClusterRole, ClusterRoleBinding)
2. Create a kubeconfig secret
3. Verify the setup

Options:
- `-n, --name`: Name for the secret (will be used as cluster identifier)
- `-s, --namespace`: Namespace to create the secret in (default: multicluster-failover-operator-system)
- `-k, --kubeconfig`: Path to kubeconfig file (default: ~/.kube/config)
- `-c, --context`: Kubeconfig context to use (default: current-context)
- `-d, --dry-run`: Dry run, print YAML but don't apply
- `--no-verify`: Skip verification step

Example for a remote cluster:
```bash
./scripts/create-kubeconfig-secret.sh -n prod-cluster -c prod-context -k ~/.kube/prod-config
```

### Creating Failover Resources

1. Create a FailoverGroup resource:

```yaml
apiVersion: crd.hahomelabs.com/v1alpha1
kind: FailoverGroup
metadata:
  name: test-failovergroup
  namespace: test-failover
spec:
  suspended: false
  workload:
    kind: Deployment
    name: test-app
  network:
    kind: Ingress
    name: test-ingress
```

2. The operator will reconcile this resource across all registered clusters.

## Development

### Building the Operator

```
make generate
make manifests
make build
```

### Running the Operator Locally

```
make install
make run
```

### Running Tests

```
make test
```

## License

This project is licensed under the Apache License 2.0 - see the LICENSE file for details.
