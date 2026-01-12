// Package storage provides in-memory and persistent storage for coordination engine data.
package storage

import (
	"fmt"
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
}

// NewIncidentStore creates a new incident store
func NewIncidentStore() *IncidentStore {
	return &IncidentStore{
		incidents: make(map[string]*models.Incident),
	}
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
