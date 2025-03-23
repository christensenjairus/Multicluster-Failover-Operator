#!/bin/bash

# This script helps run the operator locally for development

# Default values
KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
NAMESPACE="${NAMESPACE:-multicluster-failover-operator-system}"

# Check if the namespace exists and create it if needed
echo "Checking if namespace $NAMESPACE exists..."
kubectl get namespace $NAMESPACE > /dev/null 2>&1 || kubectl create namespace $NAMESPACE

# Run the operator with explicit configs
echo "Running the operator locally, watching namespace $NAMESPACE..."
echo "Using kubeconfig from: $KUBECONFIG"

# Set KUBECONFIG environment variable so controller-runtime will use it
export KUBECONFIG

make run ARGS="--leader-elect=false" 