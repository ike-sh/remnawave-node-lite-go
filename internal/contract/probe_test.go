package contract

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeValidSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/node/xray/healthcheck" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"response":{"isAlive":true,"xrayInternalStatusCached":true,"xrayVersion":"1.0.0","nodeVersion":"2.8.0"}}`)
	}))
	defer server.Close()

	result := Prober{Client: server.Client(), BearerToken: "test-token"}.Probe(
		context.Background(),
		ProbeTarget{Name: "candidate", BaseURL: server.URL},
		routeByID(t, "xray.healthcheck"),
	)
	if result.Outcome != ProbeSuccess || result.Status != http.StatusOK || !result.ValidContractResponse() {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.BodyBytes == 0 || result.BodySHA256 == "" {
		t.Fatalf("missing response metadata: %#v", result)
	}
}

func TestProbeClassifiesApplicationError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"timestamp":"2026-07-15T12:00:00Z","path":"/node/stats/get-system-stats","message":"Failed to get system stats","errorCode":"A010"}`)
	}))
	defer server.Close()

	result := Prober{Client: server.Client()}.Probe(
		context.Background(),
		ProbeTarget{Name: "official", BaseURL: server.URL},
		routeByID(t, "stats.system"),
	)
	if result.Outcome != ProbeApplicationError || result.ApplicationCode != "A010" || !result.ValidContractResponse() {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestProbeClassifiesGenericHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"statusCode":500,"message":"Unknown error","error":"Internal Server Error"}`)
	}))
	defer server.Close()

	result := Prober{Client: server.Client()}.Probe(
		context.Background(),
		ProbeTarget{Name: "official", BaseURL: server.URL},
		routeByID(t, "stats.system"),
	)
	if result.Outcome != ProbeApplicationError || result.ApplicationCode != "" || !result.ValidContractResponse() {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestProbeRejectsInvalidAndOversizedResponses(t *testing.T) {
	t.Parallel()

	t.Run("invalid schema", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"response":{"isAlive":"yes"}}`)
		}))
		defer server.Close()
		result := Prober{Client: server.Client()}.Probe(
			context.Background(),
			ProbeTarget{Name: "candidate", BaseURL: server.URL},
			routeByID(t, "xray.healthcheck"),
		)
		if result.Outcome != ProbeInvalidResponse || result.ValidContractResponse() {
			t.Fatalf("unexpected result: %#v", result)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, strings.Repeat("x", 32))
		}))
		defer server.Close()
		result := Prober{Client: server.Client(), MaxResponseBytes: 8}.Probe(
			context.Background(),
			ProbeTarget{Name: "candidate", BaseURL: server.URL},
			routeByID(t, "xray.healthcheck"),
		)
		if result.Outcome != ProbeResponseTooLarge {
			t.Fatalf("unexpected result: %#v", result)
		}
	})

	t.Run("wrong content type", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, `{"response":{"isAlive":true,"xrayInternalStatusCached":true,"xrayVersion":null,"nodeVersion":"2.8.0"}}`)
		}))
		defer server.Close()
		result := Prober{Client: server.Client()}.Probe(
			context.Background(),
			ProbeTarget{Name: "candidate", BaseURL: server.URL},
			routeByID(t, "xray.healthcheck"),
		)
		if result.Outcome != ProbeInvalidResponse {
			t.Fatalf("unexpected result: %#v", result)
		}
	})
}

func TestCompareProbeResults(t *testing.T) {
	t.Parallel()

	baseline := []ProbeResult{{
		RouteID: "stats.system", Status: 500, Outcome: ProbeApplicationError, ApplicationCode: "A010",
		BodySHA256: "baseline", DurationMillis: 10,
	}}
	candidate := []ProbeResult{{
		RouteID: "stats.system", Status: 500, Outcome: ProbeApplicationError, ApplicationCode: "A010",
		BodySHA256: "candidate", DurationMillis: 99,
	}}
	if differences := CompareProbeResults(baseline, candidate); len(differences) != 0 {
		t.Fatalf("dynamic metadata caused a mismatch: %#v", differences)
	}

	candidate[0].ApplicationCode = "A011"
	if differences := CompareProbeResults(baseline, candidate); len(differences) != 1 {
		t.Fatalf("error-code mismatch not detected: %#v", differences)
	}
}

func TestDefaultProbeRoutesAreReadOnly(t *testing.T) {
	t.Parallel()

	routes := DefaultProbeRoutes()
	if len(routes) != 11 {
		t.Fatalf("safe route count = %d, want 11", len(routes))
	}
	for _, route := range routes {
		if !route.SafeForProbe() {
			t.Errorf("unsafe route included by default: %s", route.ID)
		}
	}
}
