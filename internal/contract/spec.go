package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

const (
	OfficialNodeRepository = "https://github.com/remnawave/node.git"
	OfficialNodeVersion    = "2.8.0"
	OfficialNodeCommit     = "596f015a5c8f876dc9a9d61b6cb78d35bd8e379b"
)

// RouteContract is one executable route contract backed by the pinned
// controller and Zod command schemas.
type RouteContract struct {
	ID             string
	Method         string
	Path           string
	Request        *Schema
	ValidRequest   json.RawMessage
	Response       *Schema
	SuccessStatus  int
	SideEffects    []string
	ApplicationErr []string
	Sources        []string
}

type ErrorContract struct {
	ValidationStatus    int
	ValidationResponse  *Schema
	ApplicationResponse *Schema
	GenericHTTPResponse *Schema
	AuthFailure         string
	UnknownRoute        string
}

var OfficialErrors = ErrorContract{
	ValidationStatus:    http.StatusBadRequest,
	ValidationResponse:  validationErrorSchema(),
	ApplicationResponse: applicationErrorSchema(),
	GenericHTTPResponse: genericHTTPErrorSchema(),
	AuthFailure:         "destroy TLS socket without an HTTP response",
	UnknownRoute:        "destroy TLS socket without an HTTP response",
}

var officialRoutes = buildOfficialRoutes()

func buildOfficialRoutes() []RouteContract {
	requests := requestSchemas()
	responses := responseSchemas()
	controller := func(name string) string {
		return "src/modules/" + name
	}
	command := func(name string) string {
		return "libs/contract/commands/" + name
	}
	route := func(
		id, method, path, validRequest string,
		effects, errors, sources []string,
	) RouteContract {
		var request json.RawMessage
		if validRequest != "" {
			request = json.RawMessage(validRequest)
		}
		return RouteContract{
			ID:             id,
			Method:         method,
			Path:           path,
			Request:        requests[id],
			ValidRequest:   request,
			Response:       responses[id],
			SuccessStatus:  http.StatusOK,
			SideEffects:    effects,
			ApplicationErr: errors,
			Sources:        sources,
		}
	}

	return []RouteContract{
		route(
			"xray.start", http.MethodPost, "/node/xray/start",
			`{"internals":{"forceRestart":false,"hashes":{"emptyConfig":"empty-hash","inbounds":[]}},"xrayConfig":{}}`,
			[]string{"start or replace the rw-core process", "replace active config and inbound hash state"},
			[]string{"RN-001 is represented inside the success response"},
			[]string{controller("xray-core/xray.controller.ts"), command("xray/start.command.ts")},
		),
		route(
			"xray.stop", http.MethodGet, "/node/xray/stop", "",
			[]string{"stop rw-core", "reset plugin state and nftables plugin rules"}, nil,
			[]string{controller("xray-core/xray.controller.ts"), command("xray/stop.command.ts")},
		),
		route(
			"xray.healthcheck", http.MethodGet, "/node/xray/healthcheck", "",
			[]string{"read cached process and internal health state"}, nil,
			[]string{controller("xray-core/xray.controller.ts"), command("xray/get-node-health-check.command.ts")},
		),
		route(
			"stats.user-online-status", http.MethodPost, "/node/stats/get-user-online-status",
			`{"username":"user-1"}`,
			[]string{"query rw-core online status"}, nil,
			[]string{controller("stats/stats.controller.ts"), command("stats/get-user-online-status.command.ts")},
		),
		route(
			"stats.users", http.MethodPost, "/node/stats/get-users-stats",
			`{"reset":false}`,
			[]string{"query all user traffic counters", "reset counters when reset is true"},
			[]string{"A011"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-users-stats.command.ts")},
		),
		route(
			"stats.system", http.MethodGet, "/node/stats/get-system-stats", "",
			[]string{"query rw-core runtime stats and host stats"},
			[]string{"A010"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-system-stats.command.ts")},
		),
		route(
			"stats.inbound", http.MethodPost, "/node/stats/get-inbound-stats",
			`{"tag":"inbound-1","reset":false}`,
			[]string{"query inbound counters", "reset counters when reset is true"},
			[]string{"A012"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-inbound-stats.command.ts")},
		),
		route(
			"stats.outbound", http.MethodPost, "/node/stats/get-outbound-stats",
			`{"tag":"outbound-1","reset":false}`,
			[]string{"query outbound counters", "reset counters when reset is true"},
			[]string{"A013"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-outbound-stats.command.ts")},
		),
		route(
			"stats.all-outbounds", http.MethodPost, "/node/stats/get-all-outbounds-stats",
			`{"reset":false}`,
			[]string{"query all outbound counters", "reset counters when reset is true"},
			[]string{"A016"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-all-outbounds-stats.command.ts")},
		),
		route(
			"stats.all-inbounds", http.MethodPost, "/node/stats/get-all-inbounds-stats",
			`{"reset":false}`,
			[]string{"query all inbound counters", "reset counters when reset is true"},
			[]string{"A015"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-all-inbounds-stats.command.ts")},
		),
		route(
			"stats.combined", http.MethodPost, "/node/stats/get-combined-stats",
			`{"reset":false}`,
			[]string{"query all inbound and outbound counters", "reset counters when reset is true"},
			[]string{"A017"},
			[]string{controller("stats/stats.controller.ts"), command("stats/get-combined-stats.command.ts")},
		),
		route(
			"stats.user-ip-list", http.MethodPost, "/node/stats/get-user-ip-list",
			`{"userId":"user-1"}`,
			[]string{"query and reset one user's IP statistics"}, nil,
			[]string{controller("stats/stats.controller.ts"), command("stats/get-user-ip-list.command.ts")},
		),
		route(
			"stats.users-ip-list", http.MethodGet, "/node/stats/get-users-ip-list", "",
			[]string{"query IP statistics for known users"}, nil,
			[]string{controller("stats/stats.controller.ts"), command("stats/get-users-ip-list.command.ts")},
		),
		route(
			"handler.add-user", http.MethodPost, "/node/handler/add-user",
			`{"data":[{"type":"vless","tag":"inbound-1","username":"user-1","uuid":"00000000-0000-4000-8000-000000000001","flow":""}],"hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000002"}}`,
			[]string{"add one user to one or more rw-core inbounds", "update inbound user hash state"}, nil,
			[]string{controller("handler/handler.controller.ts"), command("handler/add-user.command.ts")},
		),
		route(
			"handler.remove-user", http.MethodPost, "/node/handler/remove-user",
			`{"username":"user-1","hashData":{"vlessUuid":"00000000-0000-4000-8000-000000000001"}}`,
			[]string{"read and drop the user's active connections", "remove user from rw-core inbounds and hash state"}, nil,
			[]string{controller("handler/handler.controller.ts"), command("handler/remove-user.command.ts")},
		),
		route(
			"handler.inbound-users-count", http.MethodPost, "/node/handler/get-inbound-users-count",
			`{"tag":"inbound-1"}`,
			[]string{"query rw-core inbound users"},
			[]string{"A014"},
			[]string{controller("handler/handler.controller.ts"), command("handler/get-inbound-users-count.command.ts")},
		),
		route(
			"handler.inbound-users", http.MethodPost, "/node/handler/get-inbound-users",
			`{"tag":"inbound-1"}`,
			[]string{"query rw-core inbound users"},
			[]string{"A014"},
			[]string{controller("handler/handler.controller.ts"), command("handler/get-inbound-users.command.ts")},
		),
		route(
			"handler.add-users", http.MethodPost, "/node/handler/add-users",
			`{"affectedInboundTags":["inbound-1"],"users":[{"inboundData":[{"type":"vless","tag":"inbound-1","flow":""}],"userData":{"userId":"user-1","hashUuid":"00000000-0000-4000-8000-000000000001","vlessUuid":"00000000-0000-4000-8000-000000000002","trojanPassword":"trojan-secret","ssPassword":"ss-secret"}}]}`,
			[]string{"add multiple users to rw-core inbounds", "replace affected inbound hash state"}, nil,
			[]string{controller("handler/handler.controller.ts"), command("handler/add-users.command.ts")},
		),
		route(
			"handler.remove-users", http.MethodPost, "/node/handler/remove-users",
			`{"users":[{"userId":"user-1","hashUuid":"00000000-0000-4000-8000-000000000001"}]}`,
			[]string{"read and drop users' active connections", "remove multiple users from rw-core and hash state"}, nil,
			[]string{controller("handler/handler.controller.ts"), command("handler/remove-users.command.ts")},
		),
		route(
			"handler.drop-users-connections", http.MethodPost, "/node/handler/drop-users-connections",
			`{"userIds":["user-1"]}`,
			[]string{"query user IPs and terminate matching host connections"}, nil,
			[]string{controller("handler/handler.controller.ts"), command("handler/drop-users-connections.command.ts")},
		),
		route(
			"handler.drop-ips", http.MethodPost, "/node/handler/drop-ips",
			`{"ips":["203.0.113.10"]}`,
			[]string{"terminate matching host connections"}, nil,
			[]string{controller("handler/handler.controller.ts"), command("handler/drop-ips.command.ts")},
		),
		route(
			"plugin.sync", http.MethodPost, "/node/plugin/sync",
			`{"plugin":{"config":{},"uuid":"00000000-0000-4000-8000-000000000001","name":"node-plugin"}}`,
			[]string{"replace or clear plugin state", "reconcile nftables rules", "restart or reconfigure rw-core when required"}, nil,
			[]string{controller("_plugin/plugin.controller.ts"), command("plugin/sync.command.ts")},
		),
		route(
			"plugin.torrent-blocker.collect", http.MethodPost, "/node/plugin/torrent-blocker/collect", "",
			[]string{"atomically drain queued torrent-blocker reports"}, nil,
			[]string{controller("_plugin/plugin.controller.ts"), command("plugin/torrent-blocker/collect-reports.schema.ts")},
		),
		route(
			"plugin.nftables.block-ips", http.MethodPost, "/node/plugin/nftables/block-ips",
			`{"ips":[{"ip":"203.0.113.10","timeout":60}]}`,
			[]string{"add timed IP blocks to the plugin nftables table", "terminate matching host connections"}, nil,
			[]string{controller("_plugin/plugin.controller.ts"), command("plugin/nftables/block-ips.schema.ts")},
		),
		route(
			"plugin.nftables.unblock-ips", http.MethodPost, "/node/plugin/nftables/unblock-ips",
			`{"ips":["203.0.113.10"]}`,
			[]string{"remove IP blocks from the plugin nftables table"}, nil,
			[]string{controller("_plugin/plugin.controller.ts"), command("plugin/nftables/unblock-ips.schema.ts")},
		),
		route(
			"plugin.nftables.recreate-tables", http.MethodPost, "/node/plugin/nftables/recreate-tables", "",
			[]string{"recreate and repopulate the plugin nftables table"}, nil,
			[]string{controller("_plugin/plugin.controller.ts"), command("plugin/nftables/recreate-tables.schema.ts")},
		),
	}
}

// OfficialRoutes returns a copy of the pinned route contract.
func OfficialRoutes() []RouteContract {
	return append([]RouteContract(nil), officialRoutes...)
}

// OfficialSourceFiles returns every pinned source file used to derive the
// executable contract. The list is stable and contains no duplicates.
func OfficialSourceFiles() []string {
	sources := map[string]struct{}{
		"package.json": {},
		"src/main.ts":  {},
		"src/common/exception/http-exception.filter.ts":         {},
		"src/common/exception/not-found-exception.filter.ts":    {},
		"src/common/helpers/error-handler.helper.ts":            {},
		"libs/contract/api/routes.ts":                           {},
		"libs/contract/api/controllers/xray.ts":                 {},
		"libs/contract/api/controllers/stats.ts":                {},
		"libs/contract/api/controllers/handler.ts":              {},
		"libs/contract/api/controllers/plugin.ts":               {},
		"libs/contract/constants/errors/errors.ts":              {},
		"libs/contract/models/node-system.schema.ts":            {},
		"libs/contract/models/torrent-blocker.report.schema.ts": {},
		"libs/contract/models/xray-webhook.schema.ts":           {},
	}
	for _, route := range officialRoutes {
		for _, source := range route.Sources {
			sources[source] = struct{}{}
		}
	}
	result := make([]string, 0, len(sources))
	for source := range sources {
		result = append(result, source)
	}
	sort.Strings(result)
	return result
}

func FindRoute(method, path string) (RouteContract, bool) {
	for _, route := range officialRoutes {
		if route.Method == method && route.Path == path {
			return route, true
		}
	}
	return RouteContract{}, false
}

func FindRouteByPath(path string) (RouteContract, bool) {
	for _, route := range officialRoutes {
		if route.Path == path {
			return route, true
		}
	}
	return RouteContract{}, false
}

func ValidateResponse(path string, raw []byte) error {
	route, ok := FindRouteByPath(path)
	if !ok {
		return fmt.Errorf("unknown contract route %q", path)
	}
	return route.Response.ValidateJSON(raw)
}

// SafeForProbe reports whether the canonical request is read-only. Routes
// omitted here either mutate state directly or can reset/drain counters.
func (r RouteContract) SafeForProbe() bool {
	switch r.ID {
	case "xray.healthcheck",
		"stats.user-online-status",
		"stats.users",
		"stats.system",
		"stats.inbound",
		"stats.outbound",
		"stats.all-outbounds",
		"stats.all-inbounds",
		"stats.combined",
		"handler.inbound-users-count",
		"handler.inbound-users":
		return true
	default:
		return false
	}
}

func DefaultProbeRoutes() []RouteContract {
	routes := make([]RouteContract, 0, len(officialRoutes))
	for _, route := range officialRoutes {
		if route.SafeForProbe() {
			routes = append(routes, route)
		}
	}
	return routes
}
