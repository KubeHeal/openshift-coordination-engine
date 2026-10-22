package policy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestPredictURL_DerivesServiceWithoutEnvRegistration(t *testing.T) {
	t.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "not-used-predictor")

	url, err := PredictURL(&ModelSpec{Name: "anomaly-detector"}, "self-healing-platform", 8080)
	require.NoError(t, err)
	require.Equal(t,
		"http://anomaly-detector-predictor.self-healing-platform.svc.cluster.local:8080/v1/models/anomaly-detector:predict",
		url,
	)
	require.NotContains(t, url, os.Getenv("KSERVE_ANOMALY_DETECTOR_SERVICE"))
	require.NotContains(t, url, "not-used-predictor")
}

func TestPredictURL_UsesPolicyOverrides(t *testing.T) {
	url, err := PredictURL(&ModelSpec{
		Name:      "checkout-detector",
		Namespace: "checkout-ml",
		Service:   "checkout-detector-predictor",
	}, "self-healing-platform", 9090)
	require.NoError(t, err)
	require.Equal(t,
		"http://checkout-detector-predictor.checkout-ml.svc.cluster.local:9090/v1/models/checkout-detector:predict",
		url,
	)
}

func TestHTTPScorer_ParsesIntegerVerdict(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(`{"predictions":[-1]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	log := logrus.New()
	log.SetOutput(io.Discard)
	verdict, err := NewHTTPScorer(0, log).Score(context.Background(), server.URL, []float64{0.5, 0.25})
	require.NoError(t, err)
	require.Equal(t, -1, verdict)
	require.JSONEq(t, `{"instances":[[0.5,0.25]]}`, string(gotBody))
}

func TestHTTPScorer_FloatOneIsNormal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(`{"predictions":[1.0]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	verdict, err := NewHTTPScorer(0, logrus.New()).Score(context.Background(), server.URL, []float64{1})
	require.NoError(t, err)
	require.Equal(t, 1, verdict)
}

func TestHTTPScorer_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	_, err := NewHTTPScorer(0, logrus.New()).Score(context.Background(), server.URL, []float64{1})
	require.Error(t, err)
	require.Contains(t, err.Error(), "503")
}
