# Deployment Checklist - Go Coordination Engine

## Build Verification

```bash
# Build the binary
make build

# Run all tests
make test

# Check binary
ls -lh bin/coordination-engine
file bin/coordination-engine

# Expected output:
# bin/coordination-engine: ELF 64-bit LSB executable, x86-64
```

## Environment Setup

### Minimal Configuration (Manual Remediation Only)

```bash
export KUBECONFIG=~/.kube/config
export ML_SERVICE_URL=http://aiops-ml-service:8080
export LOG_LEVEL=info

./bin/coordination-engine
```

### Full Configuration (with ArgoCD)

```bash
export KUBECONFIG=~/.kube/config
export ML_SERVICE_URL=http://aiops-ml-service:8080
export ARGOCD_API_URL=https://argocd-server.openshift-gitops.svc.cluster.local
export ARGOCD_TOKEN=<your-token>
export LOG_LEVEL=debug
export NAMESPACE=self-healing-platform

./bin/coordination-engine
```

## Pre-Deployment Validation

### 1. Check Kubernetes Access

```bash
# Verify cluster access
kubectl cluster-info

# Check namespace exists
kubectl get namespace self-healing-platform

# Verify RBAC permissions
./scripts/verify-rbac.sh self-healing-platform
```

### 2. Verify Dependencies

```bash
# Check ML service is reachable
curl -f http://aiops-ml-service:8080/health || echo "ML service not available"

# Check ArgoCD is available (if using)
kubectl get pods -n openshift-gitops
```

### 3. Test Health Endpoint

```bash
# Start server
./bin/coordination-engine &
SERVER_PID=$!

# Wait for startup
sleep 3

# Check lightweight health endpoint
curl http://localhost:8080/health

# Expected output:
# {"status":"ok","version":"ocp-4.20-abc123"}

# Check detailed health endpoint
curl http://localhost:8080/api/v1/health | jq

# Expected output:
# {
#   "status": "healthy",
#   "timestamp": "2025-12-18T...",
#   "version": "latest",
#   "dependencies": {
#     "kubernetes": "ok",
#     "ml_service": "ok"
#   }
# }

# Cleanup
kill $SERVER_PID
```

## Deployment Options

### Option 1: Direct Binary Deployment

```bash
# Copy binary to server
scp bin/coordination-engine user@server:/usr/local/bin/

# Create systemd service
sudo tee /etc/systemd/system/coordination-engine.service <<EOF
[Unit]
Description=OpenShift Coordination Engine
After=network.target

[Service]
Type=simple
User=coordination
Environment="KUBECONFIG=/var/lib/coordination/.kube/config"
Environment="ML_SERVICE_URL=http://aiops-ml-service:8080"
ExecStart=/usr/local/bin/coordination-engine
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable coordination-engine
sudo systemctl start coordination-engine
sudo systemctl status coordination-engine
```

### Option 2: Container Deployment

```bash
# Build container
docker build -t coordination-engine:latest .

# Run container
docker run -d \
  --name coordination-engine \
  -p 8080:8080 \
  -p 9090:9090 \
  -v ~/.kube/config:/config:ro \
  -e KUBECONFIG=/config \
  -e ML_SERVICE_URL=http://aiops-ml-service:8080 \
  coordination-engine:latest
```

### Option 3: Helm Deployment (Recommended)

```bash
# Deploy with Helm
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --create-namespace \
  --set env.ML_SERVICE_URL=http://aiops-ml-service:8080 \
  --set env.ARGOCD_API_URL=https://argocd-server.openshift-gitops.svc.cluster.local

# Check deployment
kubectl get pods -n self-healing-platform -l app=coordination-engine

# Check logs
kubectl logs -n self-healing-platform -l app=coordination-engine --tail=50
```

## Post-Deployment Verification

### 1. Check Server Logs

```bash
# Systemd
sudo journalctl -u coordination-engine -f

# Kubernetes
kubectl logs -n self-healing-platform -l app=coordination-engine -f

# Expected startup messages:
# "Starting OpenShift Coordination Engine"
# "Kubernetes clients initialized"
# "RBAC permissions verified successfully"
# "ML service client initialized"
# "Deployment detector initialized"
# "Manual remediator initialized"
# "ArgoCD remediator initialized" (if ArgoCD configured)
# "Remediation orchestrator initialized"
# "Starting API server" port=8080
# "Starting metrics server" port=9090
```

### 2. Test API Endpoints

```bash
# Health check
curl http://<server>:8080/api/v1/health | jq

# Metrics
curl http://<server>:9090/metrics | grep coordination_engine

# Trigger test remediation
curl -X POST http://<server>:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "incident_id": "test-001",
    "namespace": "default",
    "resource": {"kind": "Deployment", "name": "nginx"},
    "issue": {
      "type": "test",
      "description": "Test remediation",
      "severity": "low"
    }
  }' | jq
```

### 3. Verify RBAC

```bash
# Check ServiceAccount
kubectl get sa coordination-engine -n self-healing-platform

# Check Roles and RoleBindings
kubectl get role,rolebinding -n self-healing-platform | grep coordination

# Test permissions
kubectl auth can-i delete pods --as=system:serviceaccount:self-healing-platform:coordination-engine -n default
```

### 4. Monitor Metrics

```bash
# Check Prometheus metrics
curl http://<server>:9090/metrics | grep -E "coordination_engine|go_"

# Key metrics to watch:
# - coordination_engine_remediation_total
# - coordination_engine_remediation_duration_seconds
# - coordination_engine_strategy_selection_total
# - go_goroutines
# - go_memstats_alloc_bytes
```

## Troubleshooting

### Server Won't Start

```bash
# Check configuration
./bin/coordination-engine --help

# Validate config
export LOG_LEVEL=debug
./bin/coordination-engine 2>&1 | head -50

# Common issues:
# - KUBECONFIG not set or invalid
# - ML_SERVICE_URL unreachable
# - RBAC permissions missing
# - Port already in use
```

### RBAC Errors

```bash
# Run RBAC verification script
./scripts/verify-rbac.sh self-healing-platform

# If missing permissions, apply RBAC manifests
kubectl apply -f charts/coordination-engine/templates/rbac.yaml
```

### ArgoCD Integration Not Working

```bash
# Check ArgoCD URL is accessible
curl -k $ARGOCD_API_URL/api/version

# Verify token
curl -k -H "Authorization: Bearer $ARGOCD_TOKEN" \
  $ARGOCD_API_URL/api/v1/applications | jq

# Check logs for ArgoCD initialization
kubectl logs -n self-healing-platform -l app=coordination-engine | grep -i argocd
```

### ML Service Connection Failures

```bash
# Test ML service connectivity
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- \
  curl -v http://aiops-ml-service:8080/health

# Check service exists
kubectl get svc aiops-ml-service -n <namespace>
```

## Rollback Procedure

### Systemd

```bash
sudo systemctl stop coordination-engine
sudo systemctl disable coordination-engine
sudo rm /etc/systemd/system/coordination-engine.service
sudo systemctl daemon-reload
```

### Kubernetes/Helm

```bash
helm uninstall coordination-engine -n self-healing-platform
kubectl delete namespace self-healing-platform
```

## Success Criteria

✅ Server starts without errors
✅ Health endpoint returns "healthy"
✅ RBAC verification passes
✅ Can trigger test remediation workflow
✅ Workflow status can be retrieved
✅ Metrics endpoint responding
✅ Logs show no errors or warnings
✅ Integration with ML service working
✅ ArgoCD integration working (if configured)

## Next Steps After Deployment

1. **Monitor for 24 hours** - Watch logs and metrics
2. **Test real incidents** - Trigger actual remediations
3. **Performance testing** - Multiple concurrent workflows
4. **Integration testing** - MCP server → Coordination Engine → ArgoCD
5. **Documentation** - Update runbooks with production URLs

## Support

- Logs: `kubectl logs -n self-healing-platform -l app=coordination-engine`
- Metrics: `http://<server>:9090/metrics`
- Health: `http://<server>:8080/api/v1/health`
- Issues: See TROUBLESHOOTING.md
