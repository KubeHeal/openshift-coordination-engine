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

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// IncidentStore manages incident storage and retrieval
type IncidentStore struct {
	incidents map[string]*models.Incident
	mu        sync.RWMutex
	filePath  string // Path to persistent storage file (empty = in-memory only)
	log       *logrus.Logger
}

// NewIncidentStore creates a new in-memory incident store (no persistence)
func NewIncidentStore() *IncidentStore {
	return &IncidentStore{
		incidents: make(map[string]*models.Incident),
		filePath:  "",
		log:       logrus.New(),
	}
}

// NewIncidentStoreWithPersistence creates a new incident store with file-based persistence
func NewIncidentStoreWithPersistence(dataDir string, log *logrus.Logger) (*IncidentStore, error) {
	if log == nil {
		log = logrus.New()
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	filePath := filepath.Join(dataDir, "incidents.json")

	store := &IncidentStore{
		incidents: make(map[string]*models.Incident),
		filePath:  filePath,
		log:       log,
	}

	// Load existing incidents from file
	if err := store.LoadFromFile(); err != nil {
		log.WithError(err).Warn("Failed to load incidents from file, starting with empty store")
	}

	return store, nil
}

// Create stores a new incident and returns the generated ID
func (s *IncidentStore) Create(incident *models.Incident) (*models.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate incident
	if err := incident.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Generate ID if not provided
	if incident.ID == "" {
		incident.ID = generateIncidentID()
	}

	// Set timestamps
	now := time.Now()
	incident.CreatedAt = now
	incident.UpdatedAt = now

	// Set default status
	if incident.Status == "" {
		incident.Status = models.IncidentStatusActive
	}

	// Store incident
	s.incidents[incident.ID] = incident

	// Persist to file if enabled
	if s.filePath != "" {
		if err := s.saveToFileUnsafe(); err != nil {
			// Rollback in-memory change on persistence failure
			delete(s.incidents, incident.ID)
			return nil, fmt.Errorf("failed to persist incident: %w", err)
		}
	}

	return incident, nil
}

// Get retrieves an incident by ID
func (s *IncidentStore) Get(id string) (*models.Incident, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	incident, exists := s.incidents[id]
	if !exists {
		return nil, fmt.Errorf("incident not found: %s", id)
	}

	return incident, nil
}

// Update modifies an existing incident
func (s *IncidentStore) Update(incident *models.Incident) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Store old incident for rollback
	oldIncident, exists := s.incidents[incident.ID]
	if !exists {
		return fmt.Errorf("incident not found: %s", incident.ID)
	}

	incident.UpdatedAt = time.Now()
	s.incidents[incident.ID] = incident

	// Persist to file if enabled
	if s.filePath != "" {
		if err := s.saveToFileUnsafe(); err != nil {
			// Rollback in-memory change on persistence failure
			s.incidents[incident.ID] = oldIncident
			return fmt.Errorf("failed to persist incident update: %w", err)
		}
	}

	return nil
}

// Delete removes an incident by ID
func (s *IncidentStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Store deleted incident for rollback
	deleted, exists := s.incidents[id]
	if !exists {
		return fmt.Errorf("incident not found: %s", id)
	}

	delete(s.incidents, id)

	// Persist to file if enabled
	if s.filePath != "" {
		if err := s.saveToFileUnsafe(); err != nil {
			// Rollback in-memory change on persistence failure
			s.incidents[id] = deleted
			return fmt.Errorf("failed to persist incident deletion: %w", err)
		}
	}

	return nil
}

// ListFilter defines filter options for listing incidents
type ListFilter struct {
	Namespace string
	Severity  string
	Status    string
	Limit     int
}

// List returns incidents matching the filter criteria
func (s *IncidentStore) List(filter ListFilter) []*models.Incident {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]*models.Incident, 0, len(s.incidents))

	for _, incident := range s.incidents {
		// Apply filters
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

	// Sort by created_at descending (newest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})

	// Apply limit
	if filter.Limit > 0 && len(results) > filter.Limit {
		results = results[:filter.Limit]
	}

	return results
}

// Count returns the total number of incidents
func (s *IncidentStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.incidents)
}

// generateIncidentID generates a unique incident ID
func generateIncidentID() string {
	return "inc-" + uuid.New().String()[:8]
}

// SaveToFile saves all incidents to the file system (thread-safe)
func (s *IncidentStore) SaveToFile() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.saveToFileUnsafe()
}

// saveToFileUnsafe saves incidents to file (caller must hold lock)
func (s *IncidentStore) saveToFileUnsafe() error {
	if s.filePath == "" {
		return fmt.Errorf("no file path configured for persistence")
	}

	// Marshal incidents to JSON
	data, err := json.MarshalIndent(s.incidents, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal incidents: %w", err)
	}

	// Atomic write pattern: write to temp file, then rename
	tempFile := s.filePath + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename (POSIX guarantees atomicity)
	if err := os.Rename(tempFile, s.filePath); err != nil {
		// Cleanup temp file on failure
		if removeErr := os.Remove(tempFile); removeErr != nil {
			s.log.WithError(removeErr).Warn("Failed to remove temp file after rename failure")
		}
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	if s.log != nil {
		s.log.WithField("file", s.filePath).Debug("Incidents saved to file")
	}

	return nil
}

// LoadFromFile loads incidents from the file system
func (s *IncidentStore) LoadFromFile() error {
	if s.filePath == "" {
		return fmt.Errorf("no file path configured for persistence")
	}

	// Check if file exists
	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		// First run, no file yet - this is not an error
		if s.log != nil {
			s.log.WithField("file", s.filePath).Debug("No incidents file found, starting with empty store")
		}
		return nil
	}

	// Read file
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to read incidents file: %w", err)
	}

	// Unmarshal incidents
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := json.Unmarshal(data, &s.incidents); err != nil {
		return fmt.Errorf("failed to unmarshal incidents: %w", err)
	}

	if s.log != nil {
		s.log.WithFields(logrus.Fields{
			"file":  s.filePath,
			"count": len(s.incidents),
		}).Info("Incidents loaded from file")
	}

	return nil
}

// CleanupOldIncidents removes resolved incidents older than the specified duration
func (s *IncidentStore) CleanupOldIncidents(retentionDays int) error {
	if retentionDays <= 0 {
		return nil // Cleanup disabled
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cutoffTime := time.Now().AddDate(0, 0, -retentionDays)
	deleted := 0

	for id, incident := range s.incidents {
		// Only delete resolved incidents
		if incident.Status == models.IncidentStatusResolved && incident.ResolvedAt != nil {
			if incident.ResolvedAt.Before(cutoffTime) {
				delete(s.incidents, id)
				deleted++
			}
		}
	}

	// Persist changes if any deletions occurred
	if deleted > 0 && s.filePath != "" {
		if err := s.saveToFileUnsafe(); err != nil {
			return fmt.Errorf("failed to persist cleanup: %w", err)
		}

		if s.log != nil {
			s.log.WithFields(logrus.Fields{
				"deleted":        deleted,
				"retention_days": retentionDays,
			}).Info("Old incidents cleaned up")
		}
	}

	return nil
}
