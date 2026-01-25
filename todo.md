# TODO: OpenShift Coordination Engine

**Last Updated**: 2026-01-25
**Source**: ADR Synchronization Report

This file tracks remaining implementation work to achieve 100% ADR compliance.

---

## ADR-014: Incident Persistence Enhancements (Q2 2026)

### Task 1: File-Based Incident Persistence
**Priority**: High
**Target**: Q2 2026 (April-May 2026)
**ADR**: ADR-014 (Prometheus/Thanos Observability Integration)

**Description**: Implement file-based persistence for incidents to enable data retention across pod restarts and support ML training on historical data.

**Current State**:
- ✅ In-memory incident storage operational (`internal/storage/incidents.go`)
- ✅ CRUD operations with concurrency safety (`sync.RWMutex`)
- ✅ API endpoints: `POST /api/v1/incidents`, `GET /api/v1/incidents`
- ❌ No file persistence - data lost on pod restart

**Implementation Requirements**:
1. **File Path**: `/app/data/incidents.json` (configurable via `DATA_DIR` env var)
2. **Atomic Writes**: Write to temp file (`.tmp`), then rename to prevent corruption
3. **Load on Startup**: Read incidents from JSON file in `NewIncidentStore()`
4. **Save on Changes**: Persist to file on `CreateIncident()`, `UpdateIncident()`, `DeleteIncident()`
5. **Backward Compatibility**: Gracefully handle missing file (first run)
6. **Error Handling**: Log errors, continue operation even if file save fails

**Example Implementation**:
```go
// internal/storage/incidents.go

func (s *IncidentStore) saveToFile() error {
    s.mu.RLock()
    defer s.mu.RUnlock()

    data, err := json.MarshalIndent(s.incidents, "", "  ")
    if err != nil {
        return fmt.Errorf("failed to marshal incidents: %w", err)
    }

    // Atomic write: write to temp file, then rename
    tempFile := s.dataFile + ".tmp"
    if err := os.WriteFile(tempFile, data, 0644); err != nil {
        return fmt.Errorf("failed to write temp file: %w", err)
    }

    if err := os.Rename(tempFile, s.dataFile); err != nil {
        return fmt.Errorf("failed to rename temp file: %w", err)
    }

    return nil
}

func (s *IncidentStore) loadFromFile() error {
    data, err := os.ReadFile(s.dataFile)
    if err != nil {
        if os.IsNotExist(err) {
            s.log.Info("No existing incidents file, starting fresh")
            return nil
        }
        return fmt.Errorf("failed to read incidents file: %w", err)
    }

    var incidents []models.Incident
    if err := json.Unmarshal(data, &incidents); err != nil {
        return fmt.Errorf("failed to unmarshal incidents: %w", err)
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    for i := range incidents {
        s.incidents[incidents[i].ID] = &incidents[i]
    }

    s.log.Infof("Loaded %d incidents from file", len(incidents))
    return nil
}
```

**Acceptance Criteria**:
- [ ] Incidents survive pod restarts (verify with `kubectl delete pod`)
- [ ] No data corruption on concurrent writes (unit test with goroutines)
- [ ] File write errors logged but don't crash the application
- [ ] Unit tests for `saveToFile()` and `loadFromFile()`
- [ ] Integration test: create incident → restart pod → verify incident exists
- [ ] Documentation in ADR-014 updated to reflect implementation

**Estimated Effort**: 2-3 days

---

### Task 2: Incident TTL Cleanup
**Priority**: Medium
**Target**: Q2 2026 (May-June 2026)
**ADR**: ADR-014 (Prometheus/Thanos Observability Integration)

**Description**: Implement background cleanup goroutine to automatically delete incidents older than 90 days (configurable).

**Current State**:
- ✅ Incidents stored with `CreatedAt` timestamp
- ❌ No automatic cleanup - incidents accumulate indefinitely
- ❌ Manual deletion only via API (`DELETE /api/v1/incidents/{id}`)

**Implementation Requirements**:
1. **Configurable Retention**: `INCIDENT_RETENTION_DAYS` environment variable (default: 90)
2. **Background Goroutine**: Run cleanup every 24 hours
3. **Prometheus Metric**: `coordination_engine_incidents_deleted_total{reason="ttl_expired"}`
4. **Graceful Shutdown**: Stop cleanup goroutine on application shutdown
5. **File Persistence**: Update persisted file after cleanup

**Example Implementation**:
```go
// internal/storage/incidents.go

func (s *IncidentStore) startCleanupWorker(retentionDays int) {
    ticker := time.NewTicker(24 * time.Hour)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            s.cleanupExpiredIncidents(retentionDays)
        case <-s.stopCh:
            s.log.Info("Stopping incident cleanup worker")
            return
        }
    }
}

func (s *IncidentStore) cleanupExpiredIncidents(retentionDays int) {
    s.mu.Lock()
    defer s.mu.Unlock()

    cutoffTime := time.Now().AddDate(0, 0, -retentionDays)
    deletedCount := 0

    for id, incident := range s.incidents {
        if incident.CreatedAt.Before(cutoffTime) {
            delete(s.incidents, id)
            deletedCount++
        }
    }

    if deletedCount > 0 {
        s.log.Infof("Cleaned up %d expired incidents (older than %d days)", deletedCount, retentionDays)

        // Update Prometheus metric
        incidentsDeletedTotal.WithLabelValues("ttl_expired").Add(float64(deletedCount))

        // Persist to file
        if err := s.saveToFile(); err != nil {
            s.log.WithError(err).Error("Failed to save incidents after cleanup")
        }
    }
}
```

**Acceptance Criteria**:
- [ ] Incidents older than 90 days automatically deleted
- [ ] Cleanup runs every 24 hours (verify with logs)
- [ ] Prometheus metric `coordination_engine_incidents_deleted_total` incremented
- [ ] Configurable retention period via `INCIDENT_RETENTION_DAYS`
- [ ] Unit tests for `cleanupExpiredIncidents()`
- [ ] Cleanup goroutine stops gracefully on shutdown

**Estimated Effort**: 1-2 days

---

### Task 3: PostgreSQL Migration Design (Q3-Q4 2026)
**Priority**: Low (Future Enhancement)
**Target**: Q3-Q4 2026 (July-December 2026)
**ADR**: New ADR required (ADR-015: PostgreSQL Incident Storage)

**Description**: Design PostgreSQL schema for multi-replica deployments. This enables horizontal scaling of the coordination engine with shared incident/workflow state across replicas.

**Current State**:
- ✅ File-based persistence (after Task 1) works for single-replica deployments
- ❌ File-based storage not suitable for multi-replica deployments (file locking, consistency issues)

**Rationale**:
- **Multi-Replica Support**: Multiple coordination engine pods sharing state
- **Transactional Guarantees**: ACID transactions for incident updates
- **Query Performance**: Efficient filtering/sorting vs. in-memory search
- **Compliance**: PostgreSQL WAL logging for audit trails

**Design Requirements**:
1. **Schema Design**:
   - `incidents` table (id, namespace, resource_type, resource_name, severity, status, created_at, updated_at, resolved_at)
   - `workflows` table (id, incident_id, status, started_at, completed_at)
   - `remediation_steps` table (id, workflow_id, layer, action, status, executed_at)
2. **Connection Pooling**: `pgx` or `database/sql` with connection pool (max 25 connections)
3. **Migration Strategy**: Migrate from file-based storage to PostgreSQL (one-time migration script)
4. **Backward Compatibility**: Support both file-based and PostgreSQL via interface abstraction
5. **Health Checks**: PostgreSQL connection health in `/api/v1/health` endpoint

**Deliverables**:
- [ ] ADR-015: PostgreSQL Incident Storage (design document)
- [ ] Schema design diagram (ERD)
- [ ] Migration plan from file-based to PostgreSQL
- [ ] Interface abstraction (`IncidentStore` interface with `FileIncidentStore`, `PostgresIncidentStore` implementations)
- [ ] Team review and approval

**Estimated Effort**: 5-7 days (design phase only, implementation separate)

---

## ADR-013: GitHub Branch Protection (Minor Gap)

### Task 4: Enable GitHub Branch Protection Rules
**Priority**: High (Governance)
**Target**: Immediate (15 minutes)
**ADR**: ADR-013 (GitHub Branch Protection and Collaboration Workflow)

**Description**: Enable branch protection rules in GitHub repository settings to enforce code review and CI requirements.

**Current State**:
- ✅ CODEOWNERS file created (`.github/CODEOWNERS`)
- ✅ CI workflows configured (`.github/workflows/ci.yaml`)
- ✅ DCO sign-off documented
- ❌ Branch protection rules not yet enabled in GitHub UI (requires repository admin privileges)

**Implementation Steps**:
1. Navigate to GitHub repository settings: `https://github.com/org/openshift-coordination-engine/settings/branches`
2. Click "Add rule" for branch `main`
3. Configure:
   - ✅ Require a pull request before merging
   - ✅ Require approvals: 1
   - ✅ Dismiss stale pull request approvals when new commits are pushed
   - ✅ Require review from Code Owners
   - ✅ Require status checks to pass before merging:
     - `Lint`
     - `Test`
     - `Build`
     - `Security Scan`
   - ✅ Require conversation resolution before merging
   - ✅ Require signed commits
   - ✅ Require linear history
   - ✅ Do not allow bypassing the above settings
   - ❌ Allow force pushes: OFF
   - ❌ Allow deletions: OFF
4. Repeat steps 2-3 for `release-4.18`, `release-4.19`, `release-4.20`

**Acceptance Criteria**:
- [ ] Branch protection enabled for `main`, `release-4.18`, `release-4.19`, `release-4.20`
- [ ] Test: Attempt force push to `main` → should be rejected with error
- [ ] Test: Create PR without approval → should be blocked from merge
- [ ] Test: Create PR with failing CI → should be blocked from merge

**Estimated Effort**: 15 minutes (requires GitHub repository admin privileges)

---

## Summary

**Total Tasks**: 4
**High Priority**: 2 (Task 1: File Persistence, Task 4: Branch Protection)
**Medium Priority**: 1 (Task 2: TTL Cleanup)
**Low Priority**: 1 (Task 3: PostgreSQL Design)

**Q2 2026 Deliverables**: Tasks 1, 2, 4
**Q3-Q4 2026 Deliverables**: Task 3

**Overall Progress**: 95% ADR compliance achieved. Remaining 5% planned for Q2 2026.
