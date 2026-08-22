package stats

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const validGeocheckReport = `{"ip":"203.0.113.7","image":{"format":"svg","media_type":"image/svg+xml","encoding":"base64","data":"PHN2Zy8+"}}`

func TestGeocheckExecuteUsesIPBeforeInterface(t *testing.T) {
	service := newGeocheckService("/custom/geocheck")
	var gotBin string
	var gotArgs []string
	service.run = func(_ context.Context, bin string, args ...string) ([]byte, error) {
		gotBin = bin
		gotArgs = append([]string(nil), args...)
		return []byte(validGeocheckReport), nil
	}
	ip := " 203.0.113.7 "
	iface := "eth0"

	report, message := service.execute(context.Background(), geocheckRequest{IP: &ip, Interface: &iface})
	if message != "" {
		t.Fatalf("execute returned error: %s", message)
	}
	if report["ip"] != "203.0.113.7" {
		t.Fatalf("unexpected report: %#v", report)
	}
	if gotBin != "/custom/geocheck" {
		t.Fatalf("binary = %q", gotBin)
	}
	wantArgs := []string{"--interface", "203.0.113.7", "--json", "--svg-base64", "--quiet"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestGeocheckExecuteUsesInterfaceAndDefaultRoute(t *testing.T) {
	for _, tc := range []struct {
		name      string
		iface     *string
		wantArgs  []string
		wantError string
	}{
		{name: "interface", iface: stringPtr("eth0"), wantArgs: []string{"--interface", "eth0", "--json", "--svg-base64", "--quiet"}},
		{name: "default", wantArgs: []string{"--json", "--svg-base64", "--quiet"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newGeocheckService("geocheck")
			var gotArgs []string
			service.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				gotArgs = append([]string(nil), args...)
				return []byte(validGeocheckReport), nil
			}
			_, message := service.execute(context.Background(), geocheckRequest{Interface: tc.iface})
			if message != tc.wantError {
				t.Fatalf("message = %q, want %q", message, tc.wantError)
			}
			if !reflect.DeepEqual(gotArgs, tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", gotArgs, tc.wantArgs)
			}
		})
	}
}

func TestGeocheckExecuteRejectsInvalidIPBeforeRunning(t *testing.T) {
	service := newGeocheckService("geocheck")
	service.run = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("runner must not be called")
		return nil, nil
	}
	ip := "not-an-ip"
	_, message := service.execute(context.Background(), geocheckRequest{IP: &ip})
	if !strings.Contains(message, "not a valid IP address") {
		t.Fatalf("unexpected message: %q", message)
	}
}

func TestGeocheckExecuteAllowsOnlyOneRun(t *testing.T) {
	service := newGeocheckService("geocheck")
	started := make(chan struct{})
	release := make(chan struct{})
	service.run = func(context.Context, string, ...string) ([]byte, error) {
		close(started)
		<-release
		return []byte(validGeocheckReport), nil
	}
	done := make(chan string, 1)
	go func() {
		_, message := service.execute(context.Background(), geocheckRequest{})
		done <- message
	}()
	<-started

	if _, message := service.execute(context.Background(), geocheckRequest{}); !strings.Contains(message, "already in progress") {
		t.Fatalf("second run message = %q", message)
	}
	close(release)
	if message := <-done; message != "" {
		t.Fatalf("first run failed: %s", message)
	}
}

func TestGeocheckExecuteTimesOut(t *testing.T) {
	service := newGeocheckService("geocheck")
	service.timeout = 10 * time.Millisecond
	service.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	_, message := service.execute(context.Background(), geocheckRequest{})
	if !strings.Contains(message, "exceeded 10ms") {
		t.Fatalf("timeout message = %q", message)
	}
}

func TestGeocheckExecuteRejectsBadOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "invalid JSON", output: `{`, want: "invalid JSON"},
		{name: "missing metadata", output: `{"image":{"data":"x"}}`, want: "invalid image metadata"},
		{name: "missing image", output: `{}`, want: "invalid image metadata"},
		{name: "empty image", output: `{"image":{"format":"svg","media_type":"image/svg+xml","encoding":"base64","data":""}}`, want: "carries no image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := newGeocheckService("geocheck")
			service.run = func(context.Context, string, ...string) ([]byte, error) {
				return []byte(tc.output), nil
			}
			_, message := service.execute(context.Background(), geocheckRequest{})
			if !strings.Contains(message, tc.want) {
				t.Fatalf("message = %q, want substring %q", message, tc.want)
			}
		})
	}
}

func TestGeocheckExecuteIncludesRunnerError(t *testing.T) {
	service := newGeocheckService("geocheck")
	service.run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("exit status 1: probe failed")
	}
	_, message := service.execute(context.Background(), geocheckRequest{})
	if !strings.Contains(message, "probe failed") {
		t.Fatalf("message = %q", message)
	}
}

func TestHandleGetGeocheckResponseShapes(t *testing.T) {
	service := NewService(nil, nil, "geocheck")
	service.geocheck.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(validGeocheckReport), nil
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/node/stats/get-geocheck", strings.NewReader(`{}`))
	service.HandleGetGeocheck(recorder, request, writeGeocheckTestJSON)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var success map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &success); err != nil {
		t.Fatal(err)
	}
	if _, ok := success["response"].(map[string]any); !ok {
		t.Fatalf("missing response envelope: %#v", success)
	}

	service = NewService(nil, nil, "geocheck")
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/node/stats/get-geocheck", strings.NewReader(`{"ip":"invalid"}`))
	service.HandleGetGeocheck(recorder, request, writeGeocheckTestJSON)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	var failure map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure["errorCode"] != "A018" {
		t.Fatalf("errorCode = %v, want A018", failure["errorCode"])
	}
}

func TestLimitedBufferEnforcesCap(t *testing.T) {
	buffer := &limitedBuffer{remaining: 3}
	n, err := buffer.Write([]byte("abcde"))
	if n != 3 || !errors.Is(err, errGeocheckOutputTooLarge) {
		t.Fatalf("Write = (%d, %v), want (3, output-too-large)", n, err)
	}
	if buffer.String() != "abc" {
		t.Fatalf("buffer = %q", buffer.String())
	}
	if n, err := buffer.Write([]byte("z")); n != 0 || !errors.Is(err, errGeocheckOutputTooLarge) {
		t.Fatalf("second Write = (%d, %v)", n, err)
	}
}

func TestGeocheckOfficialBinaryIntegration(t *testing.T) {
	bin := os.Getenv("GEOCHECK_INTEGRATION_BIN")
	if bin == "" {
		t.Skip("set GEOCHECK_INTEGRATION_BIN to run the official binary")
	}

	service := newGeocheckService(bin)
	report, message := service.execute(context.Background(), geocheckRequest{})
	if message != "" {
		t.Fatalf("official GeoCheck failed: %s", message)
	}
	image, ok := report["image"].(map[string]any)
	if !ok || image["data"] == "" {
		t.Fatalf("official GeoCheck returned no image: %#v", report)
	}
}

func writeGeocheckTestJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func stringPtr(value string) *string { return &value }
