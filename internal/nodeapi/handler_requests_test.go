package nodeapi_test

import (
	"strings"
	"testing"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/contract"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func TestHandlerRequestsAcceptOfficialExamples(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/node/handler/add-user",
		"/node/handler/remove-user",
		"/node/handler/get-inbound-users-count",
		"/node/handler/get-inbound-users",
		"/node/handler/add-users",
		"/node/handler/remove-users",
		"/node/handler/drop-users-connections",
		"/node/handler/drop-ips",
	}
	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			route, ok := contract.FindRouteByPath(path)
			if !ok {
				t.Fatalf("contract route %s is missing", path)
			}
			if validation := decodeHandlerRequest(path, string(route.ValidRequest)); validation != nil {
				t.Fatalf("official request rejected: %+v", validation)
			}
		})
	}
}

func TestAddUserAcceptsEveryDiscriminatedUnionVariant(t *testing.T) {
	t.Parallel()

	body := `{
		"data":[
			{"type":"trojan","tag":"in","username":"u","password":"p"},
			{"type":"vless","tag":"in","username":"u","uuid":"not-required-to-be-uuid","flow":"xtls-rprx-vision"},
			{"type":"shadowsocks","tag":"in","username":"u","password":"p","cipherType":-1,"ivCheck":false},
			{"type":"shadowsocks22","tag":"in","username":"u","password":"p"},
			{"type":"hysteria","tag":"in","username":"u","password":"p"}
		],
		"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"},
		"ignored":"stripped"
	}`
	if validation := decodeHandlerRequest("/node/handler/add-user", body); validation != nil {
		t.Fatalf("request rejected: %+v", validation)
	}
}

func TestHandlerRequestsRejectContractViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "add user missing fields", path: "/node/handler/add-user", body: `{}`},
		{name: "add user unknown type", path: "/node/handler/add-user", body: `{"data":[{"type":"unknown"}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`},
		{name: "add user invalid flow", path: "/node/handler/add-user", body: `{"data":[{"type":"vless","tag":"in","username":"u","uuid":"u","flow":"invalid"}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`},
		{name: "add user invalid cipher", path: "/node/handler/add-user", body: `{"data":[{"type":"shadowsocks","tag":"in","username":"u","password":"p","cipherType":3,"ivCheck":false}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`},
		{name: "add user missing iv check", path: "/node/handler/add-user", body: `{"data":[{"type":"shadowsocks","tag":"in","username":"u","password":"p","cipherType":5}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`},
		{name: "add user bad hash UUID", path: "/node/handler/add-user", body: `{"data":[],"hashData":{"vlessUuid":"bad"}}`},
		{name: "add user null previous UUID", path: "/node/handler/add-user", body: `{"data":[],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001","prevVlessUuid":null}}`},
		{name: "remove user bad UUID", path: "/node/handler/remove-user", body: `{"username":"u","hashData":{"vlessUuid":"bad"}}`},
		{name: "add users bad nested UUID", path: "/node/handler/add-users", body: `{"affectedInboundTags":[],"users":[{"inboundData":[],"userData":{"userId":"u","hashUuid":"bad","vlessUuid":"bad","trojanPassword":"","ssPassword":""}}]}`},
		{name: "add users invalid flow", path: "/node/handler/add-users", body: `{"affectedInboundTags":[],"users":[{"inboundData":[{"type":"vless","tag":"in","flow":"invalid"}],"userData":{"userId":"u","hashUuid":"00000000-0000-4000-8000-000000000001","vlessUuid":"00000000-0000-4000-8000-000000000002","trojanPassword":"","ssPassword":""}}]}`},
		{name: "add users null affected inbound tag", path: "/node/handler/add-users", body: `{"affectedInboundTags":[null],"users":[]}`},
		{name: "remove users bad UUID", path: "/node/handler/remove-users", body: `{"users":[{"userId":"u","hashUuid":"bad"}]}`},
		{name: "drop users empty", path: "/node/handler/drop-users-connections", body: `{"userIds":[]}`},
		{name: "drop users null item", path: "/node/handler/drop-users-connections", body: `{"userIds":[null]}`},
		{name: "drop IPs empty", path: "/node/handler/drop-ips", body: `{"ips":[]}`},
		{name: "drop IPs null item", path: "/node/handler/drop-ips", body: `{"ips":[null]}`},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			validation := decodeHandlerRequest(test.path, test.body)
			if validation == nil || len(validation.Errors) == 0 {
				t.Fatalf("request accepted, want validation failure: %s", test.body)
			}
			route, _ := contract.FindRouteByPath(test.path)
			if err := route.Request.ValidateJSON([]byte(test.body)); err == nil {
				t.Fatalf("independent official schema accepted invalid fixture: %s", test.body)
			}
		})
	}
}

func TestHandlerArraySchemasAllowEmptyArraysWithoutMinimum(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/node/handler/add-user":     `{"data":[],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`,
		"/node/handler/add-users":    `{"affectedInboundTags":[],"users":[]}`,
		"/node/handler/remove-users": `{"users":[]}`,
	}
	for path, body := range tests {
		if validation := decodeHandlerRequest(path, body); validation != nil {
			t.Errorf("%s rejected: %+v", path, validation)
		}
	}
}

func decodeHandlerRequest(path, body string) *nodeapi.ValidationError {
	reader := strings.NewReader(body)
	switch path {
	case "/node/handler/add-user":
		var request nodeapi.AddUserRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/handler/remove-user":
		var request nodeapi.RemoveUserRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/handler/get-inbound-users-count", "/node/handler/get-inbound-users":
		var request nodeapi.TagRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/handler/add-users":
		var request nodeapi.AddUsersRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/handler/remove-users":
		var request nodeapi.RemoveUsersRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/handler/drop-users-connections":
		var request nodeapi.DropUsersConnectionsRequest
		return nodeapi.DecodeJSON(reader, &request)
	case "/node/handler/drop-ips":
		var request nodeapi.DropIPsRequest
		return nodeapi.DecodeJSON(reader, &request)
	default:
		panic("unknown handler path: " + path)
	}
}
