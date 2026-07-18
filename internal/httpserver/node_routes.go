package httpserver

import (
	"net/http"
	"sort"
)

// NodeRoute is one externally visible route in the Panel-to-Node API.
type NodeRoute struct {
	Method string
	Path   string
}

type nodeRouteID uint8

type nodeRequestBodyClass uint8

const (
	nodeRequestBodySmall nodeRequestBodyClass = iota + 1
	nodeRequestBodyMedium
	nodeRequestBodyBulk
)

const (
	smallRequestBodyBytes  int64 = 64 << 10
	mediumRequestBodyBytes int64 = 256 << 10
	bulkRequestBodyBytes   int64 = 16 << 20
)

const (
	routeXrayStart nodeRouteID = iota + 1
	routeXrayStop
	routeXrayHealthcheck
	routeStatsGetUserOnlineStatus
	routeStatsGetUsersStats
	routeStatsGetSystemStats
	routeStatsGetInboundStats
	routeStatsGetOutboundStats
	routeStatsGetAllOutboundsStats
	routeStatsGetAllInboundsStats
	routeStatsGetCombinedStats
	routeStatsGetUserIPList
	routeStatsGetUsersIPList
	routeHandlerAddUser
	routeHandlerRemoveUser
	routeHandlerGetInboundUsersCount
	routeHandlerGetInboundUsers
	routeHandlerAddUsers
	routeHandlerRemoveUsers
	routeHandlerDropUsersConnections
	routeHandlerDropIPs
	routePluginSync
	routePluginCollectTorrentReports
	routePluginBlockIPs
	routePluginUnblockIPs
	routePluginRecreateTables
)

type nodeRouteDefinition struct {
	NodeRoute
	id nodeRouteID
}

var nodeRouteDefinitions = [...]nodeRouteDefinition{
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/xray/start"}, id: routeXrayStart},
	{NodeRoute: NodeRoute{Method: http.MethodGet, Path: "/node/xray/stop"}, id: routeXrayStop},
	{NodeRoute: NodeRoute{Method: http.MethodGet, Path: "/node/xray/healthcheck"}, id: routeXrayHealthcheck},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-user-online-status"}, id: routeStatsGetUserOnlineStatus},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-users-stats"}, id: routeStatsGetUsersStats},
	{NodeRoute: NodeRoute{Method: http.MethodGet, Path: "/node/stats/get-system-stats"}, id: routeStatsGetSystemStats},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-inbound-stats"}, id: routeStatsGetInboundStats},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-outbound-stats"}, id: routeStatsGetOutboundStats},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-all-outbounds-stats"}, id: routeStatsGetAllOutboundsStats},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-all-inbounds-stats"}, id: routeStatsGetAllInboundsStats},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-combined-stats"}, id: routeStatsGetCombinedStats},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/stats/get-user-ip-list"}, id: routeStatsGetUserIPList},
	{NodeRoute: NodeRoute{Method: http.MethodGet, Path: "/node/stats/get-users-ip-list"}, id: routeStatsGetUsersIPList},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/add-user"}, id: routeHandlerAddUser},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/remove-user"}, id: routeHandlerRemoveUser},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/get-inbound-users-count"}, id: routeHandlerGetInboundUsersCount},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/get-inbound-users"}, id: routeHandlerGetInboundUsers},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/add-users"}, id: routeHandlerAddUsers},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/remove-users"}, id: routeHandlerRemoveUsers},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/drop-users-connections"}, id: routeHandlerDropUsersConnections},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/handler/drop-ips"}, id: routeHandlerDropIPs},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/plugin/sync"}, id: routePluginSync},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/plugin/torrent-blocker/collect"}, id: routePluginCollectTorrentReports},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/plugin/nftables/block-ips"}, id: routePluginBlockIPs},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/plugin/nftables/unblock-ips"}, id: routePluginUnblockIPs},
	{NodeRoute: NodeRoute{Method: http.MethodPost, Path: "/node/plugin/nftables/recreate-tables"}, id: routePluginRecreateTables},
}

type nodeRouteKey struct {
	method string
	path   string
}

var nodeRouteIndex = buildNodeRouteIndex()

func buildNodeRouteIndex() map[nodeRouteKey]nodeRouteID {
	index := make(map[nodeRouteKey]nodeRouteID, len(nodeRouteDefinitions))
	for _, definition := range nodeRouteDefinitions {
		key := nodeRouteKey{method: definition.Method, path: definition.Path}
		if _, exists := index[key]; exists {
			panic("duplicate node route: " + definition.Method + " " + definition.Path)
		}
		index[key] = definition.id
	}
	return index
}

func lookupNodeRoute(method, path string) (nodeRouteID, bool) {
	id, ok := nodeRouteIndex[nodeRouteKey{method: method, path: path}]
	return id, ok
}

func nodeRouteHasRequestDTO(route nodeRouteID) bool {
	switch route {
	case routeXrayStart,
		routeStatsGetUserOnlineStatus,
		routeStatsGetUsersStats,
		routeStatsGetInboundStats,
		routeStatsGetOutboundStats,
		routeStatsGetAllOutboundsStats,
		routeStatsGetAllInboundsStats,
		routeStatsGetCombinedStats,
		routeStatsGetUserIPList,
		routeHandlerAddUser,
		routeHandlerRemoveUser,
		routeHandlerGetInboundUsersCount,
		routeHandlerGetInboundUsers,
		routeHandlerAddUsers,
		routeHandlerRemoveUsers,
		routeHandlerDropUsersConnections,
		routeHandlerDropIPs,
		routePluginSync,
		routePluginBlockIPs,
		routePluginUnblockIPs:
		return true
	default:
		return false
	}
}

func nodeRouteRequestBodyClass(route nodeRouteID) nodeRequestBodyClass {
	switch route {
	case routeXrayStart,
		routeHandlerAddUsers,
		routeHandlerRemoveUsers,
		routeHandlerDropUsersConnections,
		routeHandlerDropIPs,
		routePluginSync:
		return nodeRequestBodyBulk
	case routeHandlerAddUser,
		routePluginBlockIPs,
		routePluginUnblockIPs:
		return nodeRequestBodyMedium
	case routeXrayStop,
		routeXrayHealthcheck,
		routeStatsGetUserOnlineStatus,
		routeStatsGetUsersStats,
		routeStatsGetSystemStats,
		routeStatsGetInboundStats,
		routeStatsGetOutboundStats,
		routeStatsGetAllOutboundsStats,
		routeStatsGetAllInboundsStats,
		routeStatsGetCombinedStats,
		routeStatsGetUserIPList,
		routeStatsGetUsersIPList,
		routeHandlerRemoveUser,
		routeHandlerGetInboundUsersCount,
		routeHandlerGetInboundUsers,
		routePluginCollectTorrentReports,
		routePluginRecreateTables:
		return nodeRequestBodySmall
	default:
		return nodeRequestBodySmall
	}
}

func nodeRouteRequestBodyLimit(route nodeRouteID) int64 {
	switch nodeRouteRequestBodyClass(route) {
	case nodeRequestBodyMedium:
		return mediumRequestBodyBytes
	case nodeRequestBodyBulk:
		return bulkRequestBodyBytes
	default:
		return smallRequestBodyBytes
	}
}

func nodeRouteHasBulkRequestBody(route nodeRouteID) bool {
	return nodeRouteRequestBodyClass(route) == nodeRequestBodyBulk
}

func nodeRouteUsesBulkHandlerSlot(route nodeRouteID) bool {
	return route != routeXrayStart && nodeRouteHasBulkRequestBody(route)
}

func nodeRouteIsReadOnly(route nodeRouteID) bool {
	switch route {
	case routeXrayHealthcheck,
		routeStatsGetUserOnlineStatus,
		routeStatsGetUsersStats,
		routeStatsGetSystemStats,
		routeStatsGetInboundStats,
		routeStatsGetOutboundStats,
		routeStatsGetAllOutboundsStats,
		routeStatsGetAllInboundsStats,
		routeStatsGetCombinedStats,
		routeStatsGetUserIPList,
		routeStatsGetUsersIPList,
		routeHandlerGetInboundUsersCount,
		routeHandlerGetInboundUsers:
		return true
	default:
		return false
	}
}

// RegisteredNodeRoutes returns a stable copy of the routes used by the dispatcher.
func RegisteredNodeRoutes() []NodeRoute {
	routes := make([]NodeRoute, 0, len(nodeRouteDefinitions))
	for _, definition := range nodeRouteDefinitions {
		routes = append(routes, definition.NodeRoute)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
	return routes
}
