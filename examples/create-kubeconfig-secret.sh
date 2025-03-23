#!/usr/bin/env bash

# Exit on error
set -e

# Default values
CLUSTER_NAME=""
NAMESPACE="multicluster-failover-operator-system"
SOURCE_CONTEXT=""
TARGET_CONTEXT=""
KUBECONFIG_PATH=""
LABEL_KEY="sigs.k8s.io/multicluster-runtime-kubeconfig"

# Function to print usage
function print_usage() {
  echo "Usage: $0 -n <cluster_name> [-f <kubeconfig_file>] [-c <source_context>] [-t <target_context>] [-s <namespace>]"
  echo ""
  echo "Options:"
  echo "  -n, --name           Name for the cluster (required, used as secret name)"
  echo "  -f, --file           Path to kubeconfig file containing the SOURCE cluster credentials"
  echo "                       Defaults to ~/.kube/<cluster_name>.yaml if not specified"
  echo "  -c, --context        Context within the SOURCE kubeconfig to use"
  echo "                       Optional, uses current-context from the source kubeconfig if not specified"
  echo "  -t, --target-context Context to use for the TARGET cluster where the secret will be created"
  echo "                       Optional, uses your current kubectl context if not specified"
  echo "  -s, --namespace      Namespace to create the secret in (default: multicluster-failover-operator-system)"
  echo "  -h, --help           Show this help message"
  echo ""
  echo "Example: Create a secret for dev-central cluster in your current context:"
  echo "  $0 -n dev-central -f ~/.kube/dev-central.yaml"
  echo ""
  echo "Example: Create a secret for dev-central cluster in the dev-west context:"
  echo "  $0 -n dev-central -f ~/.kube/dev-central.yaml -t dev-west"
  exit 1
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--name)
      CLUSTER_NAME="$2"
      shift 2
      ;;
    -f|--file)
      KUBECONFIG_PATH="$2"
      shift 2
      ;;
    -c|--context)
      SOURCE_CONTEXT="$2"
      shift 2
      ;;
    -t|--target-context)
      TARGET_CONTEXT="$2"
      shift 2
      ;;
    -s|--namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    -h|--help)
      print_usage
      ;;
    *)
      echo "Unknown option: $1"
      print_usage
      ;;
  esac
done

# Check required parameters
if [[ -z "$CLUSTER_NAME" ]]; then
  echo "Error: Cluster name is required"
  print_usage
fi

# If no kubeconfig path specified, use the default path based on cluster name
if [[ -z "$KUBECONFIG_PATH" ]]; then
  KUBECONFIG_PATH="$HOME/.kube/${CLUSTER_NAME}.yaml"
  echo "Using default SOURCE kubeconfig path: $KUBECONFIG_PATH"
fi

if [[ ! -f "$KUBECONFIG_PATH" ]]; then
  echo "Error: SOURCE kubeconfig file not found at $KUBECONFIG_PATH"
  exit 1
fi

# If source context is not specified, use the current context from the specified kubeconfig
if [[ -z "$SOURCE_CONTEXT" ]]; then
  # Temporarily set KUBECONFIG to use the specified file for 'current-context'
  ORIG_KUBECONFIG="$KUBECONFIG"
  export KUBECONFIG="$KUBECONFIG_PATH"
  SOURCE_CONTEXT=$(kubectl config current-context)
  echo "Using current context from SOURCE kubeconfig: $SOURCE_CONTEXT"
  # Restore original KUBECONFIG
  export KUBECONFIG="$ORIG_KUBECONFIG"
fi

# Create temporary kubeconfig with only the specified context from the source kubeconfig
echo "Extracting context $SOURCE_CONTEXT from SOURCE kubeconfig..."
TEMP_KUBECONFIG=$(mktemp)
trap "rm -f $TEMP_KUBECONFIG" EXIT

# Export the specified kubeconfig to use with kubectl
export KUBECONFIG="$KUBECONFIG_PATH"
kubectl config view --minify --flatten --context="$SOURCE_CONTEXT" > "$TEMP_KUBECONFIG"

# Switch to target context if specified
if [[ -n "$TARGET_CONTEXT" ]]; then
  echo "Switching to TARGET context: $TARGET_CONTEXT"
  export KUBECONFIG="$ORIG_KUBECONFIG"
  kubectl config use-context "$TARGET_CONTEXT"
else
  echo "Using current kubectl context for TARGET cluster"
  export KUBECONFIG="$ORIG_KUBECONFIG"
fi

# Create namespace if it doesn't exist
kubectl get namespace "$NAMESPACE" > /dev/null 2>&1 || kubectl create namespace "$NAMESPACE"

# Create the secret
echo "Creating kubeconfig secret '$CLUSTER_NAME' in namespace '$NAMESPACE'..."
kubectl create secret generic "$CLUSTER_NAME" \
  --namespace="$NAMESPACE" \
  --from-file=kubeconfig="$TEMP_KUBECONFIG" \
  --dry-run=client -o yaml | \
kubectl label --local -f - \
  "$LABEL_KEY=true" \
  --dry-run=client -o yaml | \
kubectl apply -f -

echo "Successfully created kubeconfig secret for cluster $CLUSTER_NAME in namespace $NAMESPACE"
echo ""
echo "To verify:"
echo "  kubectl get secret $CLUSTER_NAME -n $NAMESPACE -o jsonpath='{.metadata.labels}'"
echo ""
echo "To remove the cluster:"
echo "  kubectl delete secret $CLUSTER_NAME -n $NAMESPACE" 