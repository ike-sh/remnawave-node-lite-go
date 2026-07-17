package nodeapi_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func TestXrayStartAcceptsOfficialExample(t *testing.T) {
	t.Parallel()

	route, _ := contract.FindRouteByPath("/node/xray/start")
	var request nodeapi.XrayStartRequest
	if validation := nodeapi.DecodeJSON(strings.NewReader(string(route.ValidRequest)), &request); validation != nil {
		t.Fatalf("official request rejected: %+v", validation)
	}
}

func TestXrayStartDefaultsForceRestartAndAcceptsNumber(t *testing.T) {
	t.Parallel()

	body := `{
		"internals":{"hashes":{"emptyConfig":"hash","inbounds":[{"usersCount":1.5,"hash":"h","tag":"in"}]}},
		"xrayConfig":{"inbounds":[]},
		"ignored":true
	}`
	var request nodeapi.XrayStartRequest
	if validation := nodeapi.DecodeJSON(strings.NewReader(body), &request); validation != nil {
		t.Fatalf("request rejected: %+v", validation)
	}
	if request.Internals.ForceRestart.Present || request.Internals.ForceRestart.Value {
		t.Fatalf("forceRestart = %+v, want absent default false", request.Internals.ForceRestart)
	}
	inbound := (*request.Internals.Hashes.Inbounds)[0]
	if inbound.UsersCount == nil || *inbound.UsersCount != 1.5 {
		t.Fatalf("usersCount = %v, want 1.5", inbound.UsersCount)
	}
}

func TestXrayStartDoesNotApplyControlArrayLimitToOpaqueConfig(t *testing.T) {
	const clientsCount = 50_000
	var body strings.Builder
	body.Grow(6 << 20)
	body.WriteString(`{"internals":{"hashes":{"emptyConfig":"h","inbounds":[]}},"xrayConfig":{"inbounds":[{"settings":{"clients":[`)
	for index := range clientsCount {
		if index != 0 {
			body.WriteByte(',')
		}
		_, _ = fmt.Fprintf(
			&body,
			`{"id":"00000000-0000-4000-8000-%012d","email":"user-%05d","flow":"xtls-rprx-vision"}`,
			index,
			index,
		)
	}
	body.WriteString(`]}}]}}`)
	if body.Len() > 16<<20 {
		t.Fatalf("realistic 50k-client fixture = %d bytes, exceeds the low-memory body limit", body.Len())
	}
	var request nodeapi.XrayStartRequest
	if validation := nodeapi.DecodeJSON(strings.NewReader(body.String()), &request); validation != nil {
		t.Fatalf("50k-client Xray config rejected: %+v", validation)
	}
	inbounds, ok := (*request.XrayConfig)["inbounds"].([]any)
	if !ok || len(inbounds) != 1 {
		t.Fatalf("xrayConfig inbounds = %#v", (*request.XrayConfig)["inbounds"])
	}
	settings := inbounds[0].(map[string]any)["settings"].(map[string]any)
	clients, ok := settings["clients"].([]any)
	if !ok || len(clients) != clientsCount {
		t.Fatalf("client count = %d, want %d", len(clients), clientsCount)
	}
	first := clients[0].(map[string]any)
	last := clients[clientsCount-1].(map[string]any)
	if first["email"] != "user-00000" || last["email"] != "user-49999" {
		t.Fatalf("decoded client endpoints = %q ... %q", first["email"], last["email"])
	}
}

func TestXrayStartRejectsContractViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "missing all", body: `{}`},
		{name: "null force restart", body: `{"internals":{"forceRestart":null,"hashes":{"emptyConfig":"h","inbounds":[]}},"xrayConfig":{}}`},
		{name: "missing hashes", body: `{"internals":{},"xrayConfig":{}}`},
		{name: "missing empty config", body: `{"internals":{"hashes":{"inbounds":[]}},"xrayConfig":{}}`},
		{name: "missing inbound field", body: `{"internals":{"hashes":{"emptyConfig":"h","inbounds":[{"usersCount":1,"tag":"in"}]}},"xrayConfig":{}}`},
		{name: "config is array", body: `{"internals":{"hashes":{"emptyConfig":"h","inbounds":[]}},"xrayConfig":[]}`},
	}

	route, _ := contract.FindRouteByPath("/node/xray/start")
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var request nodeapi.XrayStartRequest
			if validation := nodeapi.DecodeJSON(strings.NewReader(test.body), &request); validation == nil {
				t.Fatalf("request accepted: %s", test.body)
			}
			if err := route.Request.ValidateJSON([]byte(test.body)); err == nil {
				t.Fatalf("independent official schema accepted invalid fixture: %s", test.body)
			}
		})
	}
}
