# Testing Guide - Go Coordination Engine

## Quick Start Testing

### 1. Manual Testing with curl

**Start the server locally:**
```bash
export KUBECONFIG=~/.kube/config
export ML_SERVICE_URL=http://aiops-ml-service:8080
make run
```

**Test health endpoint:**
```bash
curl http://localhost:8080/api/v1/health | jq
```

**Trigger a remediation workflow:**
```bash
curl -X POST http://localhost:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "incident_id": "inc-test-001",
    "namespace": "default",
    "resource": {
      "kind": "Deployment",
      "name": "test-app"
    },
    "issue": {
      "type": "pod_crash_loop",
      "description": "Pods are crash looping",
      "severity": "high"
    }
  }' | jq
```

**Get workflow status:**
```bash
# Replace wf-xxxxx with the workflow_id from trigger response
curl http://localhost:8080/api/v1/workflows/wf-xxxxx | jq
```

**List all incidents:**
```bash
curl http://localhost:8080/api/v1/incidents | jq
```

### 2. Test with Real Cluster

**Prerequisites:**
- OpenShift/Kubernetes cluster access
- RBAC permissions configured (see ADR-006)
- Test deployment running in cluster

**Create a test deployment:**
```bash
kubectl create deployment nginx --image=nginx:latest -n default
kubectl wait --for=condition=available deployment/nginx -n default
```

**Simulate a CrashLoopBackOff:**
```bash
# Create a deployment that will crash
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: crash-test
  namespace: default
spec:
  replicas: 1
  selector:
    matchLabels:
      app: crash-test
  template:
    metadata:
      labels:
        app: crash-test
    spec:
      containers:
      - name: crash
        image: busybox
        command: ["/bin/sh", "-c", "exit 1"]
EOF
```

**Trigger remediation for the crashing pod:**
```bash
# Wait for pod to be in CrashLoopBackOff
kubectl get pods -n default | grep crash-test

# Get the pod name
POD_NAME=$(kubectl get pods -n default -l app=crash-test -o jsonpath='{.items[0].metadata.name}')

# Trigger remediation via API
curl -X POST http://localhost:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d "{
    \"incident_id\": \"inc-crash-001\",
    \"namespace\": \"default\",
    \"resource\": {
      \"kind\": \"Pod\",
      \"name\": \"$POD_NAME\"
    },
    \"issue\": {
      \"type\": \"CrashLoopBackOff\",
      \"description\": \"Pod is crash looping\",
      \"severity\": \"high\"
    }
  }" | jq

# Watch the pod get deleted and recreated
kubectl get pods -n default -w
```

### 3. Test Deployment Detection

**ArgoCD-managed deployment (if ArgoCD available):**
```bash
# Create deployment with ArgoCD annotation
kubectl create deployment argocd-test --image=nginx:latest -n default
kubectl annotate deployment argocd-test argocd.argoproj.io/tracking-id="test-app:Deployment:default/argocd-test" -n default

# Trigger remediation - should detect ArgoCD
curl -X POST http://localhost:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "incident_id": "inc-argocd-001",
    "namespace": "default",
    "resource": {
      "kind": "Deployment",
      "name": "argocd-test"
    },
    "issue": {
      "type": "pod_crash_loop",
      "description": "Test issue",
      "severity": "medium"
    }
  }' | jq

# Check workflow - deployment_method should be "argocd"
```

**Helm-managed deployment:**
```bash
# Install test helm chart
helm create test-chart
helm install test-release ./test-chart -n default

# Trigger remediation
curl -X POST http://localhost:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "incident_id": "inc-helm-001",
    "namespace": "default",
    "resource": {
      "kind": "Deployment",
      "name": "test-release-test-chart"
    },
    "issue": {
      "type": "pod_crash_loop",
      "description": "Test issue",
      "severity": "medium"
    }
  }' | jq

# Check workflow - deployment_method should be "helm"
```

### 4. Integration with MCP Server

The MCP server should be able to call these endpoints:

**From MCP server code:**
```python
import requests

# Trigger remediation
response = requests.post(
    "http://coordination-engine:8080/api/v1/remediation/trigger",
    json={
        "incident_id": "inc-mcp-001",
        "namespace": "production",
        "resource": {
            "kind": "Deployment",
            "name": "payment-service"
        },
        "issue": {
            "type": "pod_crash_loop",
            "description": "Payment service pods crashing",
            "severity": "critical"
        }
    }
)

workflow_id = response.json()["workflow_id"]

# Poll for workflow status
status_response = requests.get(
    f"http://coordination-engine:8080/api/v1/workflows/{workflow_id}"
)
print(status_response.json())
```

## Test Scenarios

### Scenario 1: Pod CrashLoopBackOff
1. Deploy crashing application
2. Trigger remediation via API
3. Verify pod is deleted
4. Verify deployment recreates pod
5. Check workflow status shows "completed"

### Scenario 2: ImagePullBackOff
1. Deploy with invalid image
2. Trigger remediation
3. Verify error message recommends manual intervention
4. Check workflow status shows "failed" with helpful message

### Scenario 3: Deployment Restart
1. Create healthy deployment
2. Trigger generic remediation
3. Verify deployment annotation updated
4. Verify pods are restarted
5. Check workflow completion

### Scenario 4: Multiple Workflows
1. Trigger 3 remediation workflows in parallel
2. Use /incidents endpoint to list all
3. Verify each workflow has unique ID
4. Check all workflows tracked correctly

## Metrics Testing

**Check Prometheus metrics:**
```bash
curl http://localhost:9090/metrics | grep coordination_engine
```

Expected metrics:
- `coordination_engine_remediation_total` - Counter of remediation attempts
- `coordination_engine_remediation_duration_seconds` - Histogram of remediation duration
- Standard Go metrics (goroutines, memory, etc.)

## Cleanup

```bash
# Delete test deployments
kubectl delete deployment nginx crash-test argocd-test -n default --ignore-not-found
helm uninstall test-release -n default --ignore-not-found
```

## Troubleshooting

**Server won't start:**
- Check KUBECONFIG is set and valid
- Verify RBAC permissions (run scripts/verify-rbac.sh)
- Check logs: `journalctl -u coordination-engine -f`

**Remediation fails:**
- Check pod exists: `kubectl get pod <name> -n <namespace>`
- Verify deployment detection: check logs for "Deployment method"
- Test Kubernetes API access: `kubectl auth can-i delete pods -n <namespace>`

**Workflow shows "failed":**
- Check workflow details via GET /api/v1/workflows/{id}
- Look at error_message field
- Review server logs for detailed error

## Next Steps

After manual testing succeeds:
1. Write integration tests (test/integration/)
2. Deploy to test OpenShift cluster
3. Test with real ArgoCD instance
4. Verify MCO integration (if available)
5. Load testing with multiple concurrent workflows
