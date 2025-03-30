#!/bin/bash

# Script to create a kubeconfig secret for the Multicluster Failover Operator

set -e

# Default values
NAMESPACE="multicluster-failover-operator-system"
KUBECONFIG_PATH="${HOME}/.kube/config"
KUBECONFIG_CONTEXT=""
SECRET_NAME=""
DRY_RUN="false"
VERIFY="true"
OUTPUT_FILE=""

# Function to display usage information
function show_help {
  echo "Usage: $0 [options]"
  echo "  -c, --context CONTEXT   Kubeconfig context to use (required)"
  echo "  -n, --name NAME         Name for the secret (defaults to context name)"
  echo "  -s, --namespace NS      Namespace to create the secret in (default: ${NAMESPACE})"
  echo "  -k, --kubeconfig PATH   Path to kubeconfig file (default: ${KUBECONFIG_PATH})"
  echo "  -d, --dry-run           Dry run, print YAML but don't apply"
  echo "  --no-verify             Skip verification step"
  echo "  -o, --output FILE       Save the generated kubeconfig to a file"
  echo "  -h, --help              Show this help message"
  echo ""
  echo "Example: $0 -c prod-cluster -k ~/.kube/config"
}

# Parse command line options
while [[ $# -gt 0 ]]; do
  key="$1"
  case $key in
    -n|--name)
      SECRET_NAME="$2"
      shift 2
      ;;
    -s|--namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    -k|--kubeconfig)
      KUBECONFIG_PATH="$2"
      shift 2
      ;;
    -c|--context)
      KUBECONFIG_CONTEXT="$2"
      shift 2
      ;;
    -d|--dry-run)
      DRY_RUN="true"
      shift 1
      ;;
    --no-verify)
      VERIFY="false"
      shift 1
      ;;
    -o|--output)
      OUTPUT_FILE="$2"
      shift 2
      ;;
    -h|--help)
      show_help
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      show_help
      exit 1
      ;;
  esac
done

# Validate required arguments
if [ -z "$KUBECONFIG_CONTEXT" ]; then
  echo "ERROR: Kubeconfig context is required (-c, --context)"
  show_help
  exit 1
fi

# Set secret name to context if not specified
if [ -z "$SECRET_NAME" ]; then
  SECRET_NAME="$KUBECONFIG_CONTEXT"
fi

if [ ! -f "$KUBECONFIG_PATH" ]; then
  echo "ERROR: Kubeconfig file not found at: $KUBECONFIG_PATH"
  exit 1
fi

# Create the namespace if it doesn't exist
if [ "$DRY_RUN" != "true" ]; then
  kubectl get namespace "$NAMESPACE" &>/dev/null || kubectl create namespace "$NAMESPACE"
fi

if [ "$DRY_RUN" != "true" ]; then
  # Get the cluster CA certificate from the remote cluster
  CLUSTER_CA=$(kubectl --context=${KUBECONFIG_CONTEXT} config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.certificate-authority-data}')
  if [ -z "$CLUSTER_CA" ]; then
    echo "ERROR: Could not get cluster CA certificate"
    exit 1
  fi
  
  # Get the cluster server URL from the remote cluster
  CLUSTER_SERVER=$(kubectl --context=${KUBECONFIG_CONTEXT} config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.server}')
  if [ -z "$CLUSTER_SERVER" ]; then
    echo "ERROR: Could not get cluster server URL"
    exit 1
  fi
  
  # Get the service account token from the remote cluster
  SA_TOKEN=$(kubectl --context=${KUBECONFIG_CONTEXT} -n ${NAMESPACE} create token multicluster-failover-operator-controller-manager --duration=8760h)
  if [ -z "$SA_TOKEN" ]; then
    echo "ERROR: Could not create service account token"
    exit 1
  fi
  
  # Create a new kubeconfig using the service account token
  NEW_KUBECONFIG=$(cat <<EOF
apiVersion: v1
kind: Config
clusters:
- name: ${SECRET_NAME}
  cluster:
    server: ${CLUSTER_SERVER}
    certificate-authority-data: ${CLUSTER_CA}
contexts:
- name: ${SECRET_NAME}
  context:
    cluster: ${SECRET_NAME}
    user: multicluster-failover-operator-controller-manager
current-context: ${SECRET_NAME}
users:
- name: multicluster-failover-operator-controller-manager
  user:
    token: ${SA_TOKEN}
EOF
)
  
  # Save kubeconfig to file if requested
  if [ -n "$OUTPUT_FILE" ]; then
    echo "Saving kubeconfig to ${OUTPUT_FILE}..."
    echo "$NEW_KUBECONFIG" > "$OUTPUT_FILE"
    echo "Kubeconfig saved to ${OUTPUT_FILE}"
  fi
  
  # Encode the new kubeconfig
  KUBECONFIG_B64=$(echo "$NEW_KUBECONFIG" | base64 -w0)
  
  # Generate the secret YAML
  SECRET_YAML=$(cat <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${SECRET_NAME}
  namespace: ${NAMESPACE}
  labels:
    sigs.k8s.io/multicluster-runtime-kubeconfig: "true"
type: Opaque
data:
  kubeconfig: ${KUBECONFIG_B64}
EOF
)
  
  echo "Creating kubeconfig secret..."
  echo "$SECRET_YAML" | kubectl apply -f -
  
  echo "Secret '${SECRET_NAME}' created in namespace '${NAMESPACE}'"
  
  if [ "$VERIFY" == "true" ]; then
    echo "Verifying setup..."
    
    # Check if secret exists
    if ! kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" &>/dev/null; then
      echo "ERROR: Secret ${SECRET_NAME} not found in namespace ${NAMESPACE}"
      exit 1
    fi
    
    echo "Setup verified successfully!"
  fi
else
  echo "# Example of the kubeconfig that would be created (with redacted values):"
  echo "apiVersion: v1"
  echo "kind: Config"
  echo "clusters:"
  echo "- name: ${SECRET_NAME}"
  echo "  cluster:"
  echo "    server: <cluster-server-url>"
  echo "    certificate-authority-data: <cluster-ca>"
  echo "contexts:"
  echo "- name: ${SECRET_NAME}"
  echo "  context:"
  echo "    cluster: ${SECRET_NAME}"
  echo "    user: multicluster-failover-operator-controller-manager"
  echo "current-context: ${SECRET_NAME}"
  echo "users:"
  echo "- name: multicluster-failover-operator-controller-manager"
  echo "  user:"
  echo "    token: <service-account-token>"
fi

echo "The operator should now be able to discover and connect to this cluster" 