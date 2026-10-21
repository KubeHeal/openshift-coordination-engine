package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

const maxPolicyBytes = 1 << 20

// Loader reads the policy document and swaps it in on reload.
// A failed reload keeps the last valid document.
type Loader struct {
	path string
	log  *logrus.Logger

	mu  sync.RWMutex
	doc *Document
}

// NewLoader reads and validates the policy file. An invalid file is a startup error.
func NewLoader(path string, log *logrus.Logger) (*Loader, error) {
	if log == nil {
		log = logrus.New()
	}
	doc, err := LoadFile(path)
	if err != nil {
		return nil, err
	}
	log.WithFields(logrus.Fields{
		"path":     path,
		"policies": len(doc.Policies),
	}).Info("loaded self-healing policies")
	return &Loader{path: path, log: log, doc: doc}, nil
}

// LoadFile reads a policy document from disk.
func LoadFile(path string) (*Document, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("policy config path is required")
	}
	data, err := readPolicyFile(path)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("policy file is empty")
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse policy document: %w", err)
	}
	if err := doc.Validate(); err != nil {
		return nil, fmt.Errorf("validate policy document: %w", err)
	}
	return &doc, nil
}

// Reload reads the file again. On failure the previous document stays in effect.
func (l *Loader) Reload() error {
	if l == nil {
		return errors.New("policy loader is nil")
	}
	doc, err := LoadFile(l.path)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.doc = doc
	l.mu.Unlock()
	l.log.WithFields(logrus.Fields{
		"path":     l.path,
		"policies": len(doc.Policies),
	}).Info("reloaded self-healing policies")
	return nil
}

// Document returns the current policy document. The caller must not mutate it.
func (l *Loader) Document() *Document {
	if l == nil {
		return nil
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.doc
}

// Count returns the number of loaded policies.
func (l *Loader) Count() int {
	doc := l.Document()
	if doc == nil {
		return 0
	}
	return len(doc.Policies)
}

// Watch reloads the document when the policy file or its ConfigMap symlink changes.
// Kubernetes mounts the file as a symlink and replaces ..data atomically.
func (l *Loader) Watch(ctx context.Context) {
	if l == nil || ctx == nil {
		return
	}
	dir := filepath.Dir(filepath.Clean(l.path))
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		l.log.WithError(err).Error("policy file watch unavailable")
		return
	}
	defer func() {
		if cerr := watcher.Close(); cerr != nil {
			l.log.WithError(cerr).Debug("close policy watcher")
		}
	}()
	if err := watcher.Add(dir); err != nil {
		l.log.WithError(err).Error("failed to watch policy directory")
		return
	}
	l.log.WithField("dir", dir).Info("watching policy directory for changes")

	var debounce *time.Timer
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()

	base := filepath.Base(l.path)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !policyFileEvent(event, base) {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(200*time.Millisecond, func() {
				if err := l.Reload(); err != nil {
					l.log.WithError(err).Error("policy reload failed; keeping previous document")
				}
			})
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			l.log.WithError(err).Warn("policy watch error")
		}
	}
}

func policyFileEvent(event fsnotify.Event, base string) bool {
	name := filepath.Base(event.Name)
	if name != base && name != "..data" {
		return false
	}
	return event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)
}

func readPolicyFile(path string) ([]byte, error) {
	cleaned := filepath.Clean(path)
	// #nosec G304 -- path is the operator-configured POLICY_CONFIG_PATH, not request input
	file, err := os.Open(cleaned)
	if err != nil {
		return nil, fmt.Errorf("open policy file: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxPolicyBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read policy file: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close policy file: %w", closeErr)
	}
	if len(data) > maxPolicyBytes {
		return nil, fmt.Errorf("policy file exceeds %d bytes", maxPolicyBytes)
	}
	return data, nil
}
