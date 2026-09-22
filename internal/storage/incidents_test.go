package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

// helper builds a minimal valid incident.
func newTestIncident(title, target string, severity models.IncidentSeverity) *models.Incident {
	return &models.Incident{
		Title:       title,
		Description: "test description for " + title,
		Severity:    severity,
		Target:      target,
	}
}

// --- In-memory tests (existing behaviour) ---

func TestIncidentStore_CreateAndGet(t *testing.T) {
	store := NewIncidentStore()

	inc := newTestIncident("CPU spike", "production", models.IncidentSeverityHigh)
	created, err := store.Create(inc)

	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, models.IncidentStatusActive, created.Status)
	assert.False(t, created.CreatedAt.IsZero())

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "CPU spike", got.Title)
}

func TestIncidentStore_UpdatePersists(t *testing.T) {
	store := NewIncidentStore()

	inc := newTestIncident("disk warning", "staging", models.IncidentSeverityMedium)
	created, err := store.Create(inc)
	require.NoError(t, err)

	created.Title = "disk critical"
	require.NoError(t, store.Update(created))

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "disk critical", got.Title)
}

func TestIncidentStore_Delete(t *testing.T) {
	store := NewIncidentStore()

	inc := newTestIncident("tmp", "dev", models.IncidentSeverityLow)
	created, _ := store.Create(inc)

	require.NoError(t, store.Delete(created.ID))

	_, err := store.Get(created.ID)
	assert.Error(t, err)
	assert.Equal(t, 0, store.Count())
}

func TestIncidentStore_ListFilters(t *testing.T) {
	store := NewIncidentStore()

	_, _ = store.Create(newTestIncident("a", "production", models.IncidentSeverityHigh))
	_, _ = store.Create(newTestIncident("b", "production", models.IncidentSeverityLow))
	_, _ = store.Create(newTestIncident("c", "staging", models.IncidentSeverityHigh))

	// Filter by namespace + severity
	results := store.List(ListFilter{Namespace: "production", Severity: "high"})
	assert.Len(t, results, 1)
	assert.Equal(t, "a", results[0].Title)
}

// --- Per-file persistence tests ---

func TestIncidentStore_CreatePersistsFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)

	inc := newTestIncident("file test", "ns1", models.IncidentSeverityHigh)
	created, err := store.Create(inc)
	require.NoError(t, err)

	// Verify <id>.json exists on disk.
	path := filepath.Join(dir, created.ID+".json")
	assert.FileExists(t, path)

	// Verify content is valid JSON matching the incident.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var loaded models.Incident
	require.NoError(t, json.Unmarshal(data, &loaded))
	assert.Equal(t, created.ID, loaded.ID)
	assert.Equal(t, "file test", loaded.Title)
}

func TestIncidentStore_ReloadFromDirectory(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()

	// Phase 1: create incidents and let them persist.
	store1, err := NewIncidentStoreWithPersistence(dir, 0, log)
	require.NoError(t, err)

	ids := make([]string, 3)
	for i := 0; i < 3; i++ {
		inc := newTestIncident(fmt.Sprintf("incident-%d", i), "ns", models.IncidentSeverityMedium)
		created, createErr := store1.Create(inc)
		require.NoError(t, createErr)
		ids[i] = created.ID
		time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	}
	assert.Equal(t, 3, store1.Count())

	// Phase 2: open a new store from the same directory.
	store2, err := NewIncidentStoreWithPersistence(dir, 0, log)
	require.NoError(t, err)
	assert.Equal(t, 3, store2.Count())

	for _, id := range ids {
		got, getErr := store2.Get(id)
		require.NoError(t, getErr)
		assert.Equal(t, id, got.ID)
	}
}

func TestIncidentStore_MaxLimitTruncationOnLoad(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()

	// Create 15 incidents with no limit.
	store1, err := NewIncidentStoreWithPersistence(dir, 0, log)
	require.NoError(t, err)

	for i := 0; i < 15; i++ {
		inc := newTestIncident(fmt.Sprintf("inc-%02d", i), "ns", models.IncidentSeverityLow)
		_, createErr := store1.Create(inc)
		require.NoError(t, createErr)
		time.Sleep(2 * time.Millisecond)
	}
	assert.Equal(t, 15, store1.Count())

	// Reload with limit=10.
	store2, err := NewIncidentStoreWithPersistence(dir, 10, log)
	require.NoError(t, err)
	assert.Equal(t, 10, store2.Count())

	// The 10 kept should be the newest (sorted by CreatedAt desc, truncated).
	list := store2.List(ListFilter{})
	assert.Len(t, list, 10)

	// Verify evicted files were removed from disk.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	jsonCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonCount++
		}
	}
	assert.Equal(t, 10, jsonCount)
}

func TestIncidentStore_MaxLimitTruncationOnCreate(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()

	store, err := NewIncidentStoreWithPersistence(dir, 5, log)
	require.NoError(t, err)

	// Create 7 incidents; the store should never exceed 5.
	for i := 0; i < 7; i++ {
		inc := newTestIncident(fmt.Sprintf("inc-%d", i), "ns", models.IncidentSeverityLow)
		_, createErr := store.Create(inc)
		require.NoError(t, createErr)
		time.Sleep(2 * time.Millisecond)
	}

	assert.Equal(t, 5, store.Count())
}

func TestIncidentStore_MaxLimitEvictsResolvedFirst(t *testing.T) {
	dir := t.TempDir()
	log := logrus.New()

	store, err := NewIncidentStoreWithPersistence(dir, 3, log)
	require.NoError(t, err)

	// Create 3 incidents (fills to limit).
	inc1 := newTestIncident("old-resolved", "ns", models.IncidentSeverityLow)
	c1, _ := store.Create(inc1)
	time.Sleep(2 * time.Millisecond)

	inc2 := newTestIncident("active-mid", "ns", models.IncidentSeverityMedium)
	c2, _ := store.Create(inc2)
	time.Sleep(2 * time.Millisecond)

	inc3 := newTestIncident("active-new", "ns", models.IncidentSeverityHigh)
	c3, _ := store.Create(inc3)
	time.Sleep(2 * time.Millisecond)

	// Resolve the first incident.
	c1.Resolve()
	require.NoError(t, store.Update(c1))

	// Create a 4th incident; limit is 3 so something must be evicted.
	inc4 := newTestIncident("newest", "ns", models.IncidentSeverityCritical)
	_, err = store.Create(inc4)
	require.NoError(t, err)

	assert.Equal(t, 3, store.Count())

	// The resolved incident should have been evicted first.
	_, err = store.Get(c1.ID)
	assert.Error(t, err, "resolved incident should be evicted")

	// Active incidents should remain.
	_, err = store.Get(c2.ID)
	assert.NoError(t, err)
	_, err = store.Get(c3.ID)
	assert.NoError(t, err)
}

func TestIncidentStore_UpdatePersistsFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)

	inc := newTestIncident("v1", "ns", models.IncidentSeverityLow)
	created, _ := store.Create(inc)

	created.Title = "v2-updated"
	require.NoError(t, store.Update(created))

	// Reload and verify.
	store2, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)
	got, err := store2.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "v2-updated", got.Title)
}

func TestIncidentStore_DeleteRemovesFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)

	inc := newTestIncident("to-delete", "ns", models.IncidentSeverityLow)
	created, _ := store.Create(inc)

	path := filepath.Join(dir, created.ID+".json")
	assert.FileExists(t, path)

	require.NoError(t, store.Delete(created.ID))
	assert.NoFileExists(t, path)
}

func TestIncidentStore_SaveToFileWritesAll(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, _ = store.Create(newTestIncident(fmt.Sprintf("s-%d", i), "ns", models.IncidentSeverityLow))
	}

	// Remove all files to simulate a dirty state.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}

	require.NoError(t, store.SaveToFile())

	entries, _ = os.ReadDir(dir)
	jsonCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonCount++
		}
	}
	assert.Equal(t, 5, jsonCount)
}

func TestIncidentStore_CleanupOldIncidents(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)

	// Create and resolve an incident, then backdate its resolution.
	inc := newTestIncident("old", "ns", models.IncidentSeverityLow)
	created, _ := store.Create(inc)
	created.Resolve()
	require.NoError(t, store.Update(created))

	// Backdate ResolvedAt to 100 days ago.
	old := time.Now().AddDate(0, 0, -100)
	created.ResolvedAt = &old
	require.NoError(t, store.Update(created))

	// Cleanup with 90-day retention should remove it.
	require.NoError(t, store.CleanupOldIncidents(90))
	assert.Equal(t, 0, store.Count())
	assert.NoFileExists(t, filepath.Join(dir, created.ID+".json"))
}

func TestIncidentStore_ConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	store, err := NewIncidentStoreWithPersistence(dir, 0, logrus.New())
	require.NoError(t, err)

	var wg sync.WaitGroup
	const n = 100

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			inc := newTestIncident(fmt.Sprintf("concurrent-%d", id), "ns", models.IncidentSeverityMedium)
			_, createErr := store.Create(inc)
			assert.NoError(t, createErr)
		}(i)
	}

	wg.Wait()
	assert.Equal(t, n, store.Count())

	// Verify all files exist.
	entries, _ := os.ReadDir(dir)
	jsonCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonCount++
		}
	}
	assert.Equal(t, n, jsonCount)
}

func TestIncidentStore_InMemoryNoFileOnDisk(t *testing.T) {
	store := NewIncidentStore()

	inc := newTestIncident("mem-only", "ns", models.IncidentSeverityLow)
	_, err := store.Create(inc)
	require.NoError(t, err)

	// SaveToFile should return an error when no data dir is configured.
	assert.Error(t, store.SaveToFile())
}
