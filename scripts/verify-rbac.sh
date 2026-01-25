#!/bin/bash
# verify-rbac.sh - Verify RBAC permissions for the coordination engine ServiceAccount
#
# Usage: ./scripts/verify-rbac.sh [namespace]

set -e

NAMESPACE="${1:-self-healing-platform}"
SERVICEACCOUNT="self-healing-operator"

echo "======================================"
echo "RBAC Verification Script"
echo "======================================"
echo "Namespace: $NAMESPACE"
echo "ServiceAccount: $SERVICEACCOUNT"
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" &>/dev/null; then
    echo -e "${RED}❌ Namespace '$NAMESPACE' does not exist${NC}"
    exit 1
fi

# Check if ServiceAccount exists
if ! kubectl get serviceaccount "$SERVICEACCOUNT" -n "$NAMESPACE" &>/dev/null; then
    echo -e "${YELLOW}⚠️  ServiceAccount '$SERVICEACCOUNT' does not exist in namespace '$NAMESPACE'${NC}"
    echo "ServiceAccount will be created during Helm deployment"
    echo ""
fi

echo "Checking RBAC permissions..."
echo ""

# Counter for results
TOTAL=0
ALLOWED=0
DENIED=0

# Function to check a permission
check_permission() {
    local RESOURCE=$1
    local VERB=$2
    local API_GROUP=$3

    TOTAL=$((TOTAL + 1))

    local SA_ARG="--as=system:serviceaccount:${NAMESPACE}:${SERVICEACCOUNT}"

    if [ -z "$API_GROUP" ] || [ "$API_GROUP" == "core" ]; then
        # Core API group
        if kubectl auth can-i "$VERB" "$RESOURCE" -n "$NAMESPACE" $SA_ARG 2>/dev/null; then
            echo -e "${GREEN}✓${NC} $VERB $RESOURCE"
            ALLOWED=$((ALLOWED + 1))
        else
            echo -e "${RED}✗${NC} $VERB $RESOURCE"
            DENIED=$((DENIED + 1))
        fi
    else
        # Named API group
        if kubectl auth can-i "$VERB" "${RESOURCE}.${API_GROUP}" -n "$NAMESPACE" $SA_ARG 2>/dev/null; then
            echo -e "${GREEN}✓${NC} $VERB $RESOURCE ($API_GROUP)"
            ALLOWED=$((ALLOWED + 1))
        else
            echo -e "${RED}✗${NC} $VERB $RESOURCE ($API_GROUP)"
            DENIED=$((DENIED + 1))
        fi
    fi
}

echo "Core API Resources:"
echo "-------------------"
check_permission "pods" "get" "core"
check_permission "pods" "list" "core"
check_permission "pods" "watch" "core"
check_permission "pods" "create" "core"
check_permission "pods" "update" "core"
check_permission "pods" "patch" "core"
check_permission "pods" "delete" "core"
check_permission "services" "get" "core"
check_permission "services" "list" "core"
check_permission "configmaps" "get" "core"
check_permission "secrets" "get" "core"
check_permission "events" "create" "core"
check_permission "namespaces" "get" "core"
check_permission "namespaces" "list" "core"

echo ""
echo "Apps API Resources:"
echo "-------------------"
check_permission "deployments" "get" "apps"
check_permission "deployments" "list" "apps"
check_permission "deployments" "watch" "apps"
check_permission "deployments" "patch" "apps"
check_permission "replicasets" "get" "apps"
check_permission "replicasets" "list" "apps"
check_permission "statefulsets" "get" "apps"
check_permission "statefulsets" "list" "apps"
check_permission "daemonsets" "get" "apps"
check_permission "daemonsets" "list" "apps"

echo ""
echo "Batch API Resources:"
echo "--------------------"
check_permission "jobs" "get" "batch"
check_permission "jobs" "list" "batch"
check_permission "cronjobs" "get" "batch"
check_permission "cronjobs" "list" "batch"

echo ""
echo "ArgoCD Resources:"
echo "-----------------"
check_permission "applications" "get" "argoproj.io"
check_permission "applications" "list" "argoproj.io"
check_permission "applications" "watch" "argoproj.io"

echo ""
echo "Machine Configuration Resources:"
echo "---------------------------------"
check_permission "machineconfigs" "get" "machineconfiguration.openshift.io"
check_permission "machineconfigs" "list" "machineconfiguration.openshift.io"
check_permission "machineconfigpools" "get" "machineconfiguration.openshift.io"
check_permission "machineconfigpools" "list" "machineconfiguration.openshift.io"

echo ""
echo "Monitoring Resources:"
echo "---------------------"
check_permission "servicemonitors" "get" "monitoring.coreos.com"
check_permission "servicemonitors" "list" "monitoring.coreos.com"

echo ""
echo "======================================"
echo "Summary"
echo "======================================"
echo "Total Checks: $TOTAL"
echo -e "${GREEN}Allowed: $ALLOWED${NC}"
if [ $DENIED -gt 0 ]; then
    echo -e "${RED}Denied: $DENIED${NC}"
else
    echo -e "Denied: $DENIED"
fi
echo ""

if [ $DENIED -eq 0 ]; then
    echo -e "${GREEN}✅ All RBAC permissions verified successfully!${NC}"
    exit 0
else
    echo -e "${RED}❌ Some RBAC permissions are missing${NC}"
    echo "Please ensure the RBAC Role is properly configured and bound to the ServiceAccount"
    echo "You can deploy the Helm chart to create the required RBAC resources:"
    echo "  helm install coordination-engine ./charts/coordination-engine -n $NAMESPACE"
    exit 1
fi
