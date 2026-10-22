package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoader_ReloadKeepsLastGoodDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	require.NoError(t, os.WriteFile(path, []byte(parasolPolicyJSON), 0o600))

	loader, err := NewLoader(path, discardLogger())
	require.NoError(t, err)
	require.Equal(t, 1, loader.Count())

	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	err = loader.Reload()
	require.Error(t, err)
	require.Equal(t, 1, loader.Count())
	require.Equal(t, "parasol-insurance", loader.Document().Policies[0].Name)
}

func TestLoader_WatchPicksUpSecondApplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	require.NoError(t, os.WriteFile(path, []byte(parasolPolicyJSON), 0o600))

	loader, err := NewLoader(path, discardLogger())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go loader.Watch(ctx)

	// Writes are debounced. Space them further apart than the debounce window
	// so a retry does not postpone the reload forever.
	require.Eventually(t, func() bool {
		if loader.Count() == 2 {
			return true
		}
		if err := os.WriteFile(path, []byte(twoApplicationPolicyJSON), 0o600); err != nil {
			return false
		}
		time.Sleep(400 * time.Millisecond)
		return loader.Count() == 2
	}, 3*time.Second, 450*time.Millisecond)
	require.NotNil(t, loader.Document().Match("checkout", "CheckoutQueueLag"))
}

func TestLoader_InvalidFileIsStartupError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"actionTypes":["nope"],"policies":[]}`), 0o600))

	_, err := NewLoader(path, discardLogger())
	require.Error(t, err)
}
