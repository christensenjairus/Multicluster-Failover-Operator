# multicluster-failover-operator
// TODO(user): Add simple overview of use/purpose

## Description
// TODO(user): An in-depth paragraph about your project and overview of use

## Multicluster Functionality

This operator provides a multicluster management system that allows dynamically adding and removing clusters without restarting the operator.

### Adding Remote Clusters

Remote clusters are added by creating Kubernetes secrets containing kubeconfig data in the operator's namespace:

1. Use the provided script to create a kubeconfig secret:

```sh
./examples/create-kubeconfig-secret.sh -n cluster1 -c my-context -k ~/.kube/config
```

2. Alternatively, create a secret manually using the provided example YAML:

```sh
kubectl apply -f examples/kubeconfig-secret-example.yaml
```

The secret should have:
- A name that will be used as the cluster identifier
- The label `sigs.k8s.io/multicluster-runtime-kubeconfig: "true"`
- The kubeconfig data stored under the key `kubeconfig`

### Accessing Remote Clusters in Controllers

Once a kubeconfig secret is created, the operator will automatically:
1. Detect the new secret
2. Create a controller-runtime cluster client for it
3. Log the list of managed clusters
4. Make the cluster available through the MulticlusterManager

Example of accessing a remote cluster in your reconciler:

```go
// Get a remote cluster client
remoteCluster, err := r.MCManager.Get(ctx, "cluster1")
if err != nil {
    // Handle error - cluster might not be available
    return ctrl.Result{}, err
}

// Use the remote cluster client
pods := &corev1.PodList{}
err = remoteCluster.GetClient().List(ctx, pods, client.InNamespace("default"))
```

### Monitoring Managed Clusters

The operator provides real-time detection of kubeconfig secrets:
- When a kubeconfig secret is created, the operator immediately detects it and engages the cluster
- When a kubeconfig secret is updated, the operator immediately detects it and updates the cluster connection
- When a kubeconfig secret is deleted, the operator immediately detects it and disengages the cluster

The operator logs the list of all managed clusters after each change. Look for log entries with the message "Currently managing the following clusters".

## Getting Started

### Prerequisites
- go version v1.22.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/multicluster-failover-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don't work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/multicluster-failover-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/multicluster-failover-operator:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/multicluster-failover-operator/<tag or branch>/dist/install.yaml
```

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
