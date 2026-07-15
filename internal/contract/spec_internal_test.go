package contract

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestOfficialContractInventory(t *testing.T) {
	t.Parallel()

	routes := OfficialRoutes()
	if len(routes) != 26 {
		t.Fatalf("route count = %d, want 26", len(routes))
	}
	seenIDs := make(map[string]struct{}, len(routes))
	seenRoutes := make(map[string]struct{}, len(routes))
	seenPaths := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		if route.ID == "" || route.Method == "" || route.Path == "" {
			t.Fatalf("route has empty identity: %#v", route)
		}
		if route.Method != http.MethodGet && route.Method != http.MethodPost {
			t.Errorf("%s: unsupported method %q", route.ID, route.Method)
		}
		if route.Response == nil || route.SuccessStatus != http.StatusOK {
			t.Errorf("%s: response contract is incomplete", route.ID)
		}
		if len(route.SideEffects) == 0 || len(route.Sources) != 2 {
			t.Errorf("%s: evidence is incomplete", route.ID)
		}
		assertUnique(t, seenIDs, route.ID, "route ID")
		assertUnique(t, seenRoutes, route.Method+" "+route.Path, "method/path")
		assertUnique(t, seenPaths, route.Path, "path")
	}
}

func TestOfficialValidRequestEvidence(t *testing.T) {
	for _, route := range OfficialRoutes() {
		route := route
		t.Run(route.ID, func(t *testing.T) {
			t.Parallel()
			if route.Request == nil {
				if len(route.ValidRequest) != 0 {
					t.Fatalf("bodyless route has a request example: %s", route.ValidRequest)
				}
				return
			}
			if len(route.ValidRequest) == 0 {
				t.Fatal("request schema has no valid example")
			}
			if err := route.Request.ValidateJSON(route.ValidRequest); err != nil {
				t.Fatalf("valid request rejected: %v\n%s", err, route.ValidRequest)
			}
		})
	}
}

func TestOfficialRequestBoundaryEvidence(t *testing.T) {
	for _, route := range OfficialRoutes() {
		if route.Request == nil {
			continue
		}
		route := route
		t.Run(route.ID, func(t *testing.T) {
			t.Parallel()

			valid := decodeObject(t, route.ValidRequest)
			for field := range route.Request.required {
				missing := cloneObject(t, valid)
				delete(missing, field)
				assertInvalid(t, route.Request, missing, "missing "+field)

				wrongType := cloneObject(t, valid)
				wrongType[field] = oppositeValue(route.Request.properties[field])
				assertInvalid(t, route.Request, wrongType, "wrong type for "+field)
			}

			extra := cloneObject(t, valid)
			extra["__extraContractProbe"] = true
			if err := route.Request.ValidateJSON(marshalJSON(t, extra)); err != nil {
				t.Fatalf("Zod-compatible extra property was rejected: %v", err)
			}
		})
	}
}

func TestOfficialSpecialRequestConstraints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   string
		body string
	}{
		{"handler.add-user", `{"data":[{"type":"unknown","tag":"in","username":"u"}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`},
		{"handler.add-users", `{"affectedInboundTags":[],"users":[{"inboundData":[{"type":"unknown","tag":"in"}],"userData":{"userId":"u","hashUuid":"00000000-0000-4000-8000-000000000001","vlessUuid":"00000000-0000-4000-8000-000000000002","trojanPassword":"t","ssPassword":"s"}}]}`},
		{"handler.remove-user", `{"username":"u","hashData":{"vlessUuid":"not-a-uuid"}}`},
		{"handler.drop-ips", `{"ips":[]}`},
		{"handler.drop-users-connections", `{"userIds":[]}`},
		{"plugin.nftables.block-ips", `{"ips":[{"ip":"not-an-ip","timeout":60}]}`},
		{"plugin.nftables.unblock-ips", `{"ips":["not-an-ip"]}`},
	}
	for _, test := range tests {
		route := routeByID(t, test.id)
		if err := route.Request.ValidateJSON([]byte(test.body)); err == nil {
			t.Errorf("%s accepted invalid evidence: %s", test.id, test.body)
		}
	}
}

func TestOfficialErrorSchemas(t *testing.T) {
	t.Parallel()

	validation := []byte(`{"statusCode":400,"message":"Validation failed","errors":[{"code":"invalid_type","path":["reset"],"message":"Required"}]}`)
	if err := OfficialErrors.ValidationResponse.ValidateJSON(validation); err != nil {
		t.Fatalf("validation error schema: %v", err)
	}
	application := []byte(`{"timestamp":"2026-07-15T12:00:00.000Z","path":"/node/stats/get-users-stats","message":"Failed to get users stats","errorCode":"A011"}`)
	if err := OfficialErrors.ApplicationResponse.ValidateJSON(application); err != nil {
		t.Fatalf("application error schema: %v", err)
	}
	generic := []byte(`{"statusCode":500,"message":"Unknown error","error":"Internal Server Error"}`)
	if err := OfficialErrors.GenericHTTPResponse.ValidateJSON(generic); err != nil {
		t.Fatalf("generic HTTP error schema: %v", err)
	}
}

func TestSchemaRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	schema := object(map[string]*Schema{"ok": booleanValue()}, "ok")
	for _, raw := range [][]byte{
		[]byte(`{"ok":true}{"ok":false}`),
		[]byte(`{"ok":true} trailing`),
	} {
		if err := schema.ValidateJSON(raw); err == nil {
			t.Errorf("accepted trailing JSON: %s", raw)
		}
	}
}

func routeByID(t *testing.T, id string) RouteContract {
	t.Helper()
	for _, route := range OfficialRoutes() {
		if route.ID == id {
			return route
		}
	}
	t.Fatalf("route %q not found", id)
	return RouteContract{}
}

func decodeObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func cloneObject(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	return decodeObject(t, marshalJSON(t, value))
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertInvalid(t *testing.T, schema *Schema, value any, name string) {
	t.Helper()
	if err := schema.ValidateJSON(marshalJSON(t, value)); err == nil {
		t.Errorf("accepted invalid request (%s): %#v", name, value)
	}
}

func assertUnique(t *testing.T, seen map[string]struct{}, value, kind string) {
	t.Helper()
	if _, exists := seen[value]; exists {
		t.Errorf("duplicate %s %q", kind, value)
	}
	seen[value] = struct{}{}
}
