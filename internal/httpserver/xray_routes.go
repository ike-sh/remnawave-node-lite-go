package httpserver

import (
	"net/http"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xray"
)

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var request nodeapi.XrayStartRequest
	if !decodeNodeRequest(w, r, &request) {
		return
	}

	inbounds := make([]xray.InboundHash, 0, len(*request.Internals.Hashes.Inbounds))
	for _, inbound := range *request.Internals.Hashes.Inbounds {
		inbounds = append(inbounds, xray.InboundHash{
			UsersCount: *inbound.UsersCount,
			Hash:       *inbound.Hash,
			Tag:        *inbound.Tag,
		})
	}
	command := xray.StartRequest{
		Internals: xray.StartInternals{
			ForceRestart: request.Internals.ForceRestart.Value,
			Hashes: xray.ConfigHash{
				EmptyConfig: *request.Internals.Hashes.EmptyConfig,
				Inbounds:    inbounds,
			},
		},
		XrayConfig: *request.XrayConfig,
	}
	if !s.acquireXrayStart(r.Context()) {
		handleRequestWaitFailure(w, r)
		return
	}
	defer s.releaseXrayStart()
	writeNodeResponse(w, s.manager.Start(r.Context(), command))
}
