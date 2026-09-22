// Package storage provides in-memory and persistent storage for coordination engine data.
package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

// IncidentStore manages incident storage and retrieval.
// When configured with a data directory, each incident is persisted as an
// individual JSON file named <id>.json under that directory.  On startup the
// directory is scanned, files are loaded newest-first, and the collection is
// truncated to maxIncidents.
type IncidentStore struct {
	incidents    map[string]*models.Incident
	mu           sync.RWMutex
	dataDir      string // Root directory for per-incident JSON files (empty = in-memory only)
	maxIncidents int    // 0 = unlimited
	log          *logrus.Logger
}

// NewIncidentStore creates a new in-memory incident store (no persistence).
func NewIncidentStore() *IncidentStore {
	return &IncidentStore{
		incidents:    make(map[string]*models.Incident),
		dataDir:      "",
		maxIncidents: 0,
		log:          logrus.New(),
	}
}

// NewIncidentStoreWithPersistence creates a new incident store backed by
// per-incident JSON files in dataDir.  On creation the directory is created
// (if absent) and existing files are loaded.
func NewIncidentStoreWithPersistence(dataDir string, maxIncidents int, log *logrus.Logger) (*IncidentStore, error) {
	if log == nil {
		log = logrus.New()
	}

	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	store := &IncidentStore{
		incidents:    make(map[string]*models.Incident),
		dataDir:      dataDir,
		maxIncidents: maxIncidents,
		log:          log,
	}

	if err := store.loadFromDirectory(); err != nil {
		log.WithError(err).Warn("Failed to load incidents from directory, starting with empty store")
	}

	return store, nil
}

// Create stores a new incident and returns the generated ID.
func (s *IncidentStore) Create(incident *models.Incident) (*models.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := incident.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	if incident.ID == "" {
		incident.ID = generateIncidentID()
	}

	now := time.Now()
	incident.CreatedAt = now
	incident.UpdatedAt = now

	if incident.Status == "" {
		incident.Status = models.IncidentStatusActive
	}

	s.incidents[incident.ID] = incident

	if s.dataDir != "" {
		if err := s.saveIncidentFile(incident); err != nil {
			delete(s.incidents, incident.ID)
			return nil, fmt.Errorf("failed to persist incident: %w", err)
		}
	}

	// Enforce max-limit after creation (evicts oldest resolved first, then oldest overall).
	s.enforceMaxLimit()

	return incident, nil
}

// Get retrieves an incident by ID.
func (s *IncidentStore) Get(id string) (*models.Incident, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	incident, exists := s.incidents[id]
	if !exists {
		return nil, fmt.Errorf("incident not found: %s", id)
	}

	return incident, nil
}

// Update modifies an existing incident.
func (s *IncidentStore) Update(incident *models.Incident) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldIncident, exists := s.incidents[incident.ID]
	if !exists {
		return fmt.Errorf("incident not found: %s", incident.ID)
	}

	incident.UpdatedAt = time.Now()
	s.incidents[incident.ID] = incident

	if s.dataDir != "" {
		if err := s.saveIncidentFile(incident); err != nil {
			s.incidents[incident.ID] = oldIncident
			return fmt.Errorf("failed to persist incident update: %w", err)
		}
	}

	return nil
}

// Delete removes an incident by ID.
func (s *IncidentStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted, exists := s.incidents[id]
	if !exists {
		return fmt.Errorf("incident not found: %s", id)
	}

	delete(s.incidents, id)

	if s.dataDir != "" {
		if err := s.removeIncidentFile(id); err != nil {
			s.incidents[id] = deleted
			return fmt.Errorf("failed to persist incident deletion: %w", err)
		}
	}

	return nil
}

// ListFilter defines filter options for listing incidents.
type ListFilter struct {
	Namespace string
	Severity  string
	Status    string
	Limit     int
}

// List returns incidents matching the filter criteria.
func (s *IncidentStore) List(filter ListFilter) []*models.Incident {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]*models.Incident, 0, len(s.incidents))

	for _, incident := range s.incidents {
		if filter.Namespace != "" && incident.Target != filter.Namespace {
			continue
		}
		if filter.Severity != "" && string(incident.Severity) != filter.Severity {
			continue
		}
		if filter.Status != "" && string(incident.Status) != filter.Status {
			continue
		}

		results = append(results, incident)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results
}

// Count returns the total number of incidents.
func (s *IncidentStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.incidents)
}

// SaveToFile saves all incidents to a single checkpoint file (for graceful shutdown).
// The file is written atomically via temp+rename.
func (s *IncidentStore) SaveToFile() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.dataDir == "" {
		return fmt.Errorf("no data directory configured for persistence")
	}

	// Also persist every incident as an individual file so the directory is
	// the canonical source of truth after restart.
	for _, inc := range s.incidents {
		if err := s.saveIncidentFile(inc); err != nil {
			return fmt.Errorf("failed to save incident %s: %w", inc.ID, err)
		}
	}

	if s.log != nil {
		s.log.WithFields(logrus.Fields{
			"dir":   s.dataDir,
			"count": len(s.incidents),
		}).Info("All incidents saved to individual files")
	}

	return nil
}

// LoadFromFile is a backward-compatible alias that loads from the directory.
func (s *IncidentStore) LoadFromFile() error {
	return s.loadFromDirectory()
}

// CleanupOldIncidents removes resolved incidents older than the specified duration.
func (s *IncidentStore) CleanupOldIncidents(retentionDays int) error {
	if retentionDays <= 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoffTime := time.Now().AddDate(0, 0, -retentionDays)
	deleted := 0

	for id, incident := range s.incidents {
		if incident.Status == models.IncidentStatusResolved && incident.ResolvedAt != nil {
			if incident.ResolvedAt.Before(cutoffTime) {
				delete(s.incidents, id)
				if s.dataDir != "" {
					if err := s.removeIncidentFile(id); err != nil {
						s.log.WithError(err).WithField("id", id).Warn("Failed to remove incident file during cleanup")
					}
				}
				deleted++
			}
		}
	}

	if deleted > 0 && s.log != nil {
		s.log.WithFields(logrus.Fields{
			"deleted":        deleted,
			"retention_days": retentionDays,
		}).Info("Old incidents cleaned up")
	}

	return nil
}

// --- private helpers ---

// generateIncidentID generates a unique incident ID.
func generateIncidentID() string {
	return "inc-" + uuid.New().String()[:8]
}

// incidentFileName returns the file path for a given incident ID.
func (s *IncidentStore) incidentFileName(id string) string {
	return filepath.Join(s.dataDir, id+".json")
}

// saveIncidentFile writes a single incident to <dataDir>/<id>.json atomically.
// Caller must hold at least a read lock.
func (s *IncidentStore) saveIncidentFile(incident *models.Incident) error {
	data, err := json.MarshalIndent(incident, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal incident %s: %w", incident.ID, err)
	}

	target := s.incidentFileName(incident.ID)
	tempFile := target + ".tmp"

	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempFile, target); err != nil {
		if removeErr := os.Remove(tempFile); removeErr != nil {
			s.log.WithError(removeErr).Warn("Failed to remove temp file after rename failure")
		}
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// removeIncidentFile deletes <dataDir>/<id>.json from disk.
// Caller must hold the write lock.
func (s *IncidentStore) removeIncidentFile(id string) error {
	path := s.incidentFileName(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove incident file %s: %w", path, err)
	}
	return nil
}

// loadFromDirectory scans dataDir for *.json files, unmarshals each into an
// Incident, sorts newest-first, and truncates to maxIncidents.
func (s *IncidentStore) loadFromDirectory() error {
	if s.dataDir == "" {
		return fmt.Errorf("no data directory configured for persistence")
	}

	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	loaded := make([]*models.Incident, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		// Skip temp files left over from interrupted writes.
		if filepath.Ext(entry.Name()) == ".tmp" {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(s.dataDir, entry.Name()))
		if readErr != nil {
			s.log.WithError(readErr).WithField("file", entry.Name()).Warn("Failed to read incident file, skipping")
			continue
		}

		var inc models.Incident
		if unmarshalErr := json.Unmarshal(data, &inc); unmarshalErr != nil {
			s.log.WithError(unmarshalErr).WithField("file", entry.Name()).Warn("Failed to unmarshal incident file, skipping")
			continue
		}

		loaded = append(loaded, &inc)
	}

	// Sort newest-first (by CreatedAt descending).
	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].CreatedAt.After(loaded[j].CreatedAt)
	})

	// Truncate to maxIncidents, removing excess files from disk.
	if s.maxIncidents > 0 && len(loaded) > s.maxIncidents {
		evicted := loaded[s.maxIncidents:]
		loaded = loaded[:s.maxIncidents]

		for _, inc := range evicted {
			if removeErr := s.removeIncidentFile(inc.ID); removeErr != nil {
				s.log.WithError(removeErr).WithField("id", inc.ID).Warn("Failed to remove evicted incident file")
			}
		}

		s.log.WithFields(logrus.Fields{
			"evicted": len(evicted),
			"kept":    len(loaded),
		}).Info("Truncated incidents to max limit on load")
	}

	// Populate the in-memory map.
	for _, inc := range loaded {
		incCopy := *inc
		s.incidents[inc.ID] = &incCopy
	}

	if s.log != nil {
		s.log.WithFields(logrus.Fields{
			"dir":   s.dataDir,
			"count": len(s.incidents),
		}).Info("Incidents loaded from directory")
	}

	return nil
}

// enforceMaxLimit evicts the oldest incidents when the store exceeds maxIncidents.
// Resolved incidents are evicted first; then the oldest overall.
// Caller must hold the write lock.
func (s *IncidentStore) enforceMaxLimit() {
	if s.maxIncidents <= 0 || len(s.incidents) <= s.maxIncidents {
		return
	}

	all := make([]*models.Incident, 0, len(s.incidents))
	for _, inc := range s.incidents {
		all = append(all, inc)
	}

	// Partition: resolved first (sorted oldest-first), then non-resolved (sorted oldest-first).
	sort.Slice(all, func(i, j int) bool {
		iResolved := all[i].Status == models.IncidentStatusResolved
		jResolved := all[j].Status == models.IncidentStatusResolved
		if iResolved != jResolved {
			return iResolved // resolved items come first (evict them first)
		}
		return all[i].CreatedAt.Before(all[j].CreatedAt) // oldest first within each group
	})

	excess := len(all) - s.maxIncidents
	for k := 0; k < excess; k++ {
		evictID := all[k].ID
		delete(s.incidents, evictID)
		if s.dataDir != "" {
			if err := s.removeIncidentFile(evictID); err != nil {
				s.log.WithError(err).WithField("id", evictID).Warn("Failed to remove evicted incident file")
			}
		}
	}
}
