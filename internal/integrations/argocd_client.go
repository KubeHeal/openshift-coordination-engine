// Package integrations provides clients for external service integrations (ArgoCD, ML service).
package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// ArgoCDClient handles communication with ArgoCD API
type ArgoCDClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
	log        *logrus.Logger
}

// NewArgoCDClient creates a new ArgoCD API client
func NewArgoCDClient(baseURL, token string, log *logrus.Logger) *ArgoCDClient {
	return &ArgoCDClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// Application represents an ArgoCD application
type Application struct {
	Metadata ApplicationMetadata `json:"metadata"`
	Spec     ApplicationSpec     `json:"spec"`
	Status   ApplicationStatus   `json:"status"`
}

// ApplicationMetadata contains application metadata
type ApplicationMetadata struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// ApplicationSpec contains application specification
type ApplicationSpec struct {
	Source      ApplicationSource      `json:"source"`
	Destination ApplicationDestination `json:"destination"`
}

// ApplicationSource contains Git repository information
type ApplicationSource struct {
	RepoURL        string `json:"repoURL"`
	Path           string `json:"path"`
	TargetRevision string `json:"targetRevision"`
}

// ApplicationDestination contains deployment destination
type ApplicationDestination struct {
	Server    string `json:"server"`
	Namespace string `json:"namespace"`
}

// ApplicationStatus contains application sync status
type ApplicationStatus struct {
	Sync   SyncStatus   `json:"sync"`
	Health HealthStatus `json:"health"`
}

// SyncStatus contains synchronization status
type SyncStatus struct {
	Status     string     `json:"status"` // "Synced", "OutOfSync"
	Revision   string     `json:"revision"`
	ComparedTo ComparedTo `json:"comparedTo"`
}

// ComparedTo contains comparison information
type ComparedTo struct {
	Source      ApplicationSource      `json:"source"`
	Destination ApplicationDestination `json:"destination"`
}

// HealthStatus contains application health
type HealthStatus struct {
	Status  string `json:"status"` // "Healthy", "Progressing", "Degraded"
	Message string `json:"message,omitempty"`
}

// SyncRequest represents a sync operation request
type SyncRequest struct {
	Revision  string         `json:"revision,omitempty"`
	Prune     bool           `json:"prune"`
	DryRun    bool           `json:"dryRun"`
	Resources []SyncResource `json:"resources,omitempty"`
}

// SyncResource represents a resource to sync
type SyncResource struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// GetApplication retrieves an ArgoCD application
func (c *ArgoCDClient) GetApplication(ctx context.Context, appName string) (*Application, error) {
	url := fmt.Sprintf("%s/api/v1/applications/%s", c.baseURL, appName)

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get application: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("ArgoCD API error (status %d), failed to read body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("ArgoCD API error (status %d): %s", resp.StatusCode, string(body))
	}

	var app Application
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &app, nil
}

// SyncApplication triggers a sync operation for an ArgoCD application
func (c *ArgoCDClient) SyncApplication(ctx context.Context, appName string, syncReq *SyncRequest) error {
	url := fmt.Sprintf("%s/api/v1/applications/%s/sync", c.baseURL, appName)

	// Default sync request if not provided
	if syncReq == nil {
		syncReq = &SyncRequest{
			Prune:  false,
			DryRun: false,
		}
	}

	body, err := json.Marshal(syncReq)
	if err != nil {
		return fmt.Errorf("failed to marshal sync request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	c.log.WithFields(logrus.Fields{
		"app_name": appName,
		"url":      url,
	}).Info("Triggering ArgoCD sync")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to sync application: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("ArgoCD sync failed (status %d), failed to read body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("ArgoCD sync failed (status %d): %s", resp.StatusCode, string(body))
	}

	c.log.WithField("app_name", appName).Info("ArgoCD sync triggered successfully")
	return nil
}

// WaitForSync waits for an ArgoCD application to be synced and healthy
func (c *ArgoCDClient) WaitForSync(ctx context.Context, appName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	c.log.WithFields(logrus.Fields{
		"app_name": appName,
		"timeout":  timeout.String(),
	}).Info("Waiting for ArgoCD sync completion")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for sync: %w", ctx.Err())
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for sync after %s", timeout)
			}

			app, err := c.GetApplication(ctx, appName)
			if err != nil {
				c.log.WithError(err).Warn("Failed to get application status")
				continue
			}

			c.log.WithFields(logrus.Fields{
				"sync_status":   app.Status.Sync.Status,
				"health_status": app.Status.Health.Status,
			}).Debug("Application status")

			// Check if synced and healthy
			if app.Status.Sync.Status == "Synced" && app.Status.Health.Status == "Healthy" {
				c.log.WithField("app_name", appName).Info("Application synced and healthy")
				return nil
			}

			// Check for degraded health
			if app.Status.Health.Status == "Degraded" {
				return fmt.Errorf("application health degraded: %s", app.Status.Health.Message)
			}
		}
	}
}

// FindApplicationByResource finds an ArgoCD application managing a specific Kubernetes resource
func (c *ArgoCDClient) FindApplicationByResource(ctx context.Context, namespace, name, kind string) (*Application, error) {
	// List all applications (simplified - in production, use label selectors)
	url := fmt.Sprintf("%s/api/v1/applications", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("ArgoCD API error (status %d), failed to read body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("ArgoCD API error (status %d): %s", resp.StatusCode, string(body))
	}

	var appList struct {
		Items []Application `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&appList); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Find application managing this resource
	for i := range appList.Items {
		if appList.Items[i].Spec.Destination.Namespace == namespace {
			// In production, check managed resources more carefully
			// For now, match by namespace
			return &appList.Items[i], nil
		}
	}

	return nil, fmt.Errorf("no ArgoCD application found managing %s/%s", namespace, name)
}

// setAuthHeaders sets authentication headers
func (c *ArgoCDClient) setAuthHeaders(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

// HealthCheck verifies ArgoCD API is accessible
func (c *ArgoCDClient) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/version", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ArgoCD health check failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ArgoCD health check failed with status %d", resp.StatusCode)
	}

	return nil
}

// Close closes the HTTP client connections
func (c *ArgoCDClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}
