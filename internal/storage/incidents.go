
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

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// IncidentStore manages incident storage and retrieval
type IncidentStore struct {
	incidents map[string]*models.Incident
	mu        sync.RWMutex
	dataFile  string
}

// NewIncidentStore creates a new incident store
func NewIncidentStore() *IncidentStore {
	return NewIncidentStoreWithPath("")
}

// NewIncidentStoreWithPath creates a new incident store with a custom data path
func NewIncidentStoreWithPath(dataDir string) *IncidentStore {
	if dataDir == "" {
		dataDir = os.Getenv("DATA_DIR")
	}
	if dataDir == "" {
		dataDir = "/data"
	}

	store := &IncidentStore{
		incidents: make(map[string]*models.Incident),
		dataFile:  filepath.Join(dataDir, "incidents.json"),
	}

	// Load existing data from disk
	if err := store.load(); err != nil {
		fmt.Printf("Warning: Could not load incidents from disk: %v\n", err)
	} else {
		fmt.Printf("Loaded %d incidents from %s\n", len(store.incidents), store.dataFile)
	}

	return store
}

// load reads incidents from the JSON file
func (s *IncidentStore) load() error {
	data, err := os.ReadFile(s.dataFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, that's OK
		}
		return fmt.Errorf("failed to read data file: %w", err)
	}

	var incidents []*models.Incident
	if err := json.Unmarshal(data, &incidents); err != nil {
		return fmt.Errorf("failed to unmarshal incidents: %w", err)
	}

	for _, inc := range incidents {
		s.incidents[inc.ID] = inc
	}

	return nil
}

// save writes all incidents to the JSON file
func (s *IncidentStore) save() error {
	// Ensure directory exists
	dir := filepath.Dir(s.dataFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	incidents := make([]*models.Incident, 0, len(s.incidents))
	for _, inc := range s.incidents {
		incidents = append(incidents, inc)
	}

	data, err := json.MarshalIndent(incidents, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal incidents: %w", err)
	}

	// Write to temp file first, then rename (atomic)
	tmpFile := s.dataFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, s.dataFile); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
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

	// Persist to disk
	if err := s.save(); err != nil {
		fmt.Printf("Warning: Failed to persist incident: %v\n", err)
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

	if _, exists := s.incidents[incident.ID]; !exists {
		return fmt.Errorf("incident not found: %s", incident.ID)
	}

	incident.UpdatedAt = time.Now()
	s.incidents[incident.ID] = incident

	// Persist to disk
	if err := s.save(); err != nil {
		fmt.Printf("Warning: Failed to persist incident update: %v\n", err)
	}

	return nil
}

// Delete removes an incident by ID
func (s *IncidentStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.incidents[id]; !exists {
		return fmt.Errorf("incident not found: %s", id)
	}

	delete(s.incidents, id)

	// Persist to disk
	if err := s.save(); err != nil {
		fmt.Printf("Warning: Failed to persist incident deletion: %v\n", err)
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
		if filter.Severity != "" && filter.Severity != "all" && string(incident.Severity) != filter.Severity {
			continue
		}
		if filter.Status != "" && filter.Status != "all" && string(incident.Status) != filter.Status {
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
