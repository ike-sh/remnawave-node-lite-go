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

func TestSemanticProjectionDetectsOppositeControlBooleans(t *testing.T) {
	t.Parallel()

	leftHash, err := successSemanticHash("xray.healthcheck", []byte(`{"response":{"isAlive":true,"xrayInternalStatusCached":true,"xrayVersion":"26.6.27","nodeVersion":"2.8.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := successSemanticHash("xray.healthcheck", []byte(`{"response":{"isAlive":true,"xrayInternalStatusCached":false,"xrayVersion":"26.6.27","nodeVersion":"2.8.0"}}`))
	if err != nil {
		t.Fatal(err)
	}
	baseline := []ProbeResult{{RouteID: "xray.healthcheck", Status: 200, Outcome: ProbeSuccess, SemanticSHA256: leftHash}}
	candidate := []ProbeResult{{RouteID: "xray.healthcheck", Status: 200, Outcome: ProbeSuccess, SemanticSHA256: rightHash}}
	if differences := CompareProbeResults(baseline, candidate); len(differences) != 1 {
		t.Fatalf("opposite health state was not detected: %#v", differences)
	}
}

func TestSemanticProjectionIgnoresTrafficButComparesIdentities(t *testing.T) {
	t.Parallel()

	left, err := successSemanticHash("stats.users", []byte(`{"response":{"users":[{"username":"alice","downlink":1,"uplink":2},{"username":"bob","downlink":3,"uplink":4}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := successSemanticHash("stats.users", []byte(`{"response":{"users":[{"username":"bob","downlink":300,"uplink":400},{"username":"alice","downlink":100,"uplink":200}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("dynamic traffic or ordering changed semantic hash: %s != %s", left, right)
	}
	different, err := successSemanticHash("stats.users", []byte(`{"response":{"users":[{"username":"alice","downlink":1,"uplink":2}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if left == different {
		t.Fatal("different user identity set produced the same semantic hash")
	}
}

func TestCompareProbeResultsDistinguishesGenericAndApplicationErrors(t *testing.T) {
	t.Parallel()
	applicationHash, err := errorSemanticHash("application", []byte(`{"timestamp":"2026-07-15T12:00:00Z","path":"/node/test","message":"failed","errorCode":""}`))
	if err != nil {
		t.Fatal(err)
	}
	genericHash, err := errorSemanticHash("generic", []byte(`{"statusCode":500,"message":"failed","error":"Internal Server Error"}`))
	if err != nil {
		t.Fatal(err)
	}
	baseline := []ProbeResult{{
		RouteID: "stats.system", Status: 500, Outcome: ProbeApplicationError,
		ErrorKind: "application", SemanticSHA256: applicationHash,
	}}
	candidate := []ProbeResult{{
		RouteID: "stats.system", Status: 500, Outcome: ProbeApplicationError,
		ErrorKind: "generic", SemanticSHA256: genericHash,
	}}
	if differences := CompareProbeResults(baseline, candidate); len(differences) != 1 {
		t.Fatalf("error kind mismatch was not detected: %#v", differences)
	}
}

func TestSemanticProjectionNormalizesEquivalentJSONNumbers(t *testing.T) {
	t.Parallel()
	integerHash, err := successSemanticHash("handler.inbound-users-count", []byte(`{"response":{"count":500}}`))
	if err != nil {
		t.Fatal(err)
	}
	exponentHash, err := successSemanticHash("handler.inbound-users-count", []byte(`{"response":{"count":5e2}}`))
	if err != nil {
		t.Fatal(err)
	}
	if integerHash != exponentHash {
		t.Fatalf("equivalent JSON numbers changed semantic hash: %s != %s", integerHash, exponentHash)
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
