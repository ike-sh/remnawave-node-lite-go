package nodeapi_test

import (
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func TestPluginRequestsAcceptOfficialExamples(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/node/plugin/sync",
		"/node/plugin/nftables/block-ips",
		"/node/plugin/nftables/unblock-ips",
	}
	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			route, _ := contract.FindRouteByPath(path)
			if validation := decodePluginRequest(path, string(route.ValidRequest)); validation != nil {
				t.Fatalf("official request rejected: %+v", validation)
			}
		})
	}
}

func TestPluginSyncDistinguishesNullFromMissing(t *testing.T) {
	t.Parallel()

	if validation := decodePluginRequest("/node/plugin/sync", `{"plugin":null}`); validation != nil {
		t.Fatalf("nullable plugin rejected: %+v", validation)
	}
	if validation := decodePluginRequest("/node/plugin/sync", `{}`); validation == nil {
		t.Fatal("missing plugin accepted")
	}
}

func TestPluginSyncDoesNotApplyControlArrayLimitToOpaqueConfig(t *testing.T) {
	const entriesCount = 20_000
	body := `{"plugin":{"config":{"entries":[` + strings.Repeat(`{},`, entriesCount-1) +
		`{}]},"uuid":"00000000-0000-4000-8000-000000000001","name":"p"}}`
	var request nodeapi.PluginSyncRequest
	if validation := nodeapi.DecodeJSON(strings.NewReader(body), &request); validation != nil {
		t.Fatalf("opaque plugin config rejected: %+v", validation)
	}
}

func TestPluginRequestsRejectContractViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "sync config is array", path: "/node/plugin/sync", body: `{"plugin":{"config":[],"uuid":"00000000-0000-4000-8000-000000000001","name":"p"}}`},
		{name: "sync bad UUID", path: "/node/plugin/sync", body: `{"plugin":{"config":{},"uuid":"bad","name":"p"}}`},
		{name: "sync missing name", path: "/node/plugin/sync", body: `{"plugin":{"config":{},"uuid":"00000000-0000-4000-8000-000000000001"}}`},
		{name: "block invalid IP", path: "/node/plugin/nftables/block-ips", body: `{"ips":[{"ip":"bad","timeout":60}]}`},
		{name: "block missing timeout", path: "/node/plugin/nftables/block-ips", body: `{"ips":[{"ip":"203.0.113.10"}]}`},
		{name: "unblock invalid IP", path: "/node/plugin/nftables/unblock-ips", body: `{"ips":["bad"]}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if validation := decodePluginRequest(test.path, test.body); validation == nil {
				t.Fatalf("request accepted: %s", test.body)
			}
			route, _ := contract.FindRouteByPath(test.path)
			if err := route.Request.ValidateJSON([]byte(test.body)); err == nil {
				t.Fatalf("independent official schema accepted invalid fixture: %s", test.body)
			}
		})
	}
}

func TestBlockIPsAcceptsFractionalTimeoutAndEmptyArray(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"ips":[]}`,
		`{"ips":[{"ip":"2001:db8::1","timeout":1.5}]}`,
	} {
		if validation := decodePluginRequest("/node/plugin/nftables/block-ips", body); validation != nil {
			t.Errorf("request %s rejected: %+v", body, validation)
		}
	}
}

func decodePluginRequest(path, body string) *nodeapi.ValidationError {
	reader := strings.NewReader(body)
	switch path {
	case "/node/plugin/sync":
		var request nodeapi.PluginSyncRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/plugin/nftables/block-ips":
		var request nodeapi.BlockIPsRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/plugin/nftables/unblock-ips":
		var request nodeapi.UnblockIPsRequest
		return nodeapi.DecodeJSON(reader, &request)
	default:
		panic("unknown plugin path: " + path)
	}
}
