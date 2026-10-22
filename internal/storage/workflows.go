package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/pkg/models"
)

// WorkflowStore persists remediation workflows as JSON files under
// {dataDir}/workflows. Incident loading skips directories, so these files
// are not parsed as incidents.
type WorkflowStore struct {
	dir string
	log *logrus.Logger
}

// NewWorkflowStore creates {dataDir}/workflows and returns a store for it.
func NewWorkflowStore(dataDir string, log *logrus.Logger) (*WorkflowStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if log == nil {
		log = logrus.New()
	}
	dir := filepath.Join(dataDir, "workflows")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create workflow directory: %w", err)
	}
	return &WorkflowStore{dir: dir, log: log}, nil
}

// Save writes one workflow atomically.
func (s *WorkflowStore) Save(workflow *models.Workflow) error {
	if workflow == nil || workflow.ID == "" {
		return fmt.Errorf("workflow id is required")
	}
	data, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow %s: %w", workflow.ID, err)
	}
	return s.SaveBytes(workflow.ID, data)
}

// SaveBytes writes an already-encoded workflow.
func (s *WorkflowStore) SaveBytes(id string, data []byte) error {
	target, err := s.path(id)
	if err != nil {
		return err
	}
	tempFile := target + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("write workflow %s: %w", id, err)
	}
	if err := os.Rename(tempFile, target); err != nil {
		if removeErr := os.Remove(tempFile); removeErr != nil {
			s.log.WithError(removeErr).Warn("failed to remove workflow temp file")
		}
		return fmt.Errorf("rename workflow %s: %w", id, err)
	}
	return nil
}

// Load reads every workflow file. Unreadable files are skipped.
func (s *WorkflowStore) Load() ([]*models.Workflow, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read workflow directory: %w", err)
	}
	workflows := make([]*models.Workflow, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if readErr != nil {
			s.log.WithError(readErr).WithField("file", entry.Name()).Warn("failed to read workflow file")
			continue
		}
		var workflow models.Workflow
		if unmarshalErr := json.Unmarshal(data, &workflow); unmarshalErr != nil || workflow.ID == "" {
			s.log.WithError(unmarshalErr).WithField("file", entry.Name()).Warn("failed to parse workflow file")
			continue
		}
		workflows = append(workflows, &workflow)
	}
	return workflows, nil
}

func (s *WorkflowStore) path(id string) (string, error) {
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid workflow id")
	}
	return filepath.Join(s.dir, id+".json"), nil
}
