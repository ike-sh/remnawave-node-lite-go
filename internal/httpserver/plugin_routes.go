package httpserver

import (
	"net/http"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/plugin"
)

func (s *Server) handlePluginSync(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.PluginSyncRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}

	var command *plugin.SyncPlugin
	if !request.Plugin.Null {
		command = &plugin.SyncPlugin{
			UUID:   *request.Plugin.Value.UUID,
			Name:   *request.Plugin.Value.Name,
			Config: *request.Plugin.Value.Config,
		}
	}
	if !s.acquireXrayLifecycle(r.Context()) {
		panic(http.ErrAbortHandler)
	}
	defer s.releaseXrayLifecycle()
	writeNodeResponse(w, s.pluginService.SyncContext(r.Context(), command))
}

func (s *Server) handlePluginCollectReports(w http.ResponseWriter) {
	writeNodeResponse(w, s.pluginService.CollectReports())
}

func (s *Server) handlePluginBlockIPs(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.BlockIPsRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	items := make([]plugin.BlockIP, 0, len(*request.IPs))
	for _, item := range *request.IPs {
		items = append(items, plugin.BlockIP{IP: *item.IP, Timeout: *item.Timeout})
	}
	writeNodeResponse(w, s.pluginService.BlockIPsContext(r.Context(), items))
}

func (s *Server) handlePluginUnblockIPs(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.UnblockIPsRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}
	writeNodeResponse(w, s.pluginService.UnblockIPsContext(r.Context(), *request.IPs))
}

func (s *Server) handlePluginRecreateTables(w http.ResponseWriter, r *http.Request) {
	if !s.acquireXrayLifecycle(r.Context()) {
		panic(http.ErrAbortHandler)
	}
	defer s.releaseXrayLifecycle()
	writeNodeResponse(w, s.pluginService.RecreateTablesContext(r.Context()))
}
