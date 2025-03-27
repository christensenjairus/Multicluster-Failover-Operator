# Multicluster Failover Operator

A Kubernetes operator designed to manage failover between multiple Kubernetes clusters. This operator uses the multicluster-runtime framework to orchestrate resources across clusters for high availability.

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

1. Create a Secret with a kubeconfig for each cluster you want to manage:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: cluster-1
  namespace: multicluster-failover-operator-system
  labels:
    sigs.k8s.io/multicluster-runtime-kubeconfig: "true"
data:
  kubeconfig: BASE64_ENCODED_KUBECONFIG_HERE
```

2. Create a FailoverGroup resource:

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

3. The operator will reconcile this resource across all registered clusters.

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
