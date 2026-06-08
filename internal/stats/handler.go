package stats

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"remnawave-node-lite-go/internal/system"
	"remnawave-node-lite-go/internal/xtls"
)

type Provider interface {
	GetSysStats(ctx context.Context) (*xtls.SysStats, error)
	GetAllUsersStats(ctx context.Context, reset bool) ([]xtls.UserTraffic, error)
	GetUserOnlineStatus(ctx context.Context, username string) (bool, error)
	GetInboundStats(ctx context.Context, tag string, reset bool) (xtls.TagTraffic, error)
	GetOutboundStats(ctx context.Context, tag string, reset bool) (xtls.TagTraffic, error)
	GetAllInboundsStats(ctx context.Context, reset bool) ([]xtls.TagTraffic, error)
	GetAllOutboundsStats(ctx context.Context, reset bool) ([]xtls.TagTraffic, error)
	GetUserIPList(ctx context.Context, userID string, reset bool) ([]xtls.IPEntry, error)
	GetUsersIPList(ctx context.Context) ([]xtls.UserIPEntry, error)
}

type ReportsCounter interface {
	ReportsCount() int
}

type Service struct {
	provider       Provider
	reportsCounter ReportsCounter
}

func NewService(provider Provider, reportsCounter ReportsCounter) *Service {
	return &Service{provider: provider, reportsCounter: reportsCounter}
}

type envelope[T any] struct {
	Response T `json:"response"`
}

type systemStatsResponse struct {
	XrayInfo any `json:"xrayInfo"`
	Plugins  struct {
		TorrentBlocker struct {
			ReportsCount int `json:"reportsCount"`
		} `json:"torrentBlocker"`
	} `json:"plugins"`
	System struct {
		Stats system.Stats `json:"stats"`
	} `json:"system"`
}

type writeJSONFn func(w http.ResponseWriter, status int, value any)

func (s *Service) HandleGetSystemStats(w http.ResponseWriter, write writeJSONFn) {
	var xrayInfo any
	if s.provider != nil {
		if stats, err := s.provider.GetSysStats(context.Background()); err == nil && stats != nil {
			xrayInfo = stats
		}
	}

	var resp systemStatsResponse
	resp.XrayInfo = xrayInfo
	if s.reportsCounter != nil {
		resp.Plugins.TorrentBlocker.ReportsCount = s.reportsCounter.ReportsCount()
	}
	resp.System.Stats = system.GetStats()
	write(w, http.StatusOK, envelope[systemStatsResponse]{Response: resp})
}

func (s *Service) HandleGetUserOnlineStatus(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	body := decodeOnlineRequest(r)
	online := false
	if s.provider != nil && body.Username != "" {
		if value, err := s.provider.GetUserOnlineStatus(r.Context(), body.Username); err == nil {
			online = value
		}
	}
	write(w, http.StatusOK, envelope[struct {
		IsOnline bool `json:"isOnline"`
	}]{Response: struct {
		IsOnline bool `json:"isOnline"`
	}{IsOnline: online}})
}

func (s *Service) HandleGetUsersStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	users := []userTrafficResponse{}
	if s.provider != nil {
		if stats, err := s.provider.GetAllUsersStats(r.Context(), reset); err == nil {
			for _, item := range stats {
				users = append(users, userTrafficResponse{
					Username: item.Username,
					Downlink: item.Downlink,
					Uplink:   item.Uplink,
				})
			}
		}
	}
	write(w, http.StatusOK, envelope[struct {
		Users []userTrafficResponse `json:"users"`
	}]{Response: struct {
		Users []userTrafficResponse `json:"users"`
	}{Users: users}})
}

func (s *Service) HandleGetInboundStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	tag, reset := decodeTagResetRequest(r)
	item := tagTrafficResponse{}
	if s.provider != nil && tag != "" {
		if stats, err := s.provider.GetInboundStats(r.Context(), tag, reset); err == nil {
			item = tagTrafficResponse{
				Inbound:  stats.Tag,
				Downlink: stats.Downlink,
				Uplink:   stats.Uplink,
			}
		}
	}
	write(w, http.StatusOK, envelope[tagTrafficResponse]{Response: item})
}

func (s *Service) HandleGetOutboundStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	tag, reset := decodeTagResetRequest(r)
	item := outboundTrafficResponse{}
	if s.provider != nil && tag != "" {
		if stats, err := s.provider.GetOutboundStats(r.Context(), tag, reset); err == nil {
			item = outboundTrafficResponse{
				Outbound: stats.Tag,
				Downlink: stats.Downlink,
				Uplink:   stats.Uplink,
			}
		}
	}
	write(w, http.StatusOK, envelope[outboundTrafficResponse]{Response: item})
}

func (s *Service) HandleGetAllInboundsStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	items := []inboundTrafficResponse{}
	if s.provider != nil {
		if stats, err := s.provider.GetAllInboundsStats(r.Context(), reset); err == nil {
			for _, item := range stats {
				items = append(items, inboundTrafficResponse{
					Inbound:  item.Tag,
					Downlink: item.Downlink,
					Uplink:   item.Uplink,
				})
			}
		}
	}
	write(w, http.StatusOK, envelope[struct {
		Inbounds []inboundTrafficResponse `json:"inbounds"`
	}]{Response: struct {
		Inbounds []inboundTrafficResponse `json:"inbounds"`
	}{Inbounds: items}})
}

func (s *Service) HandleGetAllOutboundsStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	items := []outboundListItemResponse{}
	if s.provider != nil {
		if stats, err := s.provider.GetAllOutboundsStats(r.Context(), reset); err == nil {
			for _, item := range stats {
				items = append(items, outboundListItemResponse{
					Outbound: item.Tag,
					Downlink: item.Downlink,
					Uplink:   item.Uplink,
				})
			}
		}
	}
	write(w, http.StatusOK, envelope[struct {
		Outbounds []outboundListItemResponse `json:"outbounds"`
	}]{Response: struct {
		Outbounds []outboundListItemResponse `json:"outbounds"`
	}{Outbounds: items}})
}

func (s *Service) HandleGetCombinedStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	inbounds := []inboundTrafficResponse{}
	outbounds := []outboundListItemResponse{}
	if s.provider != nil {
		if items, err := s.provider.GetAllInboundsStats(r.Context(), reset); err == nil {
			for _, item := range items {
				inbounds = append(inbounds, inboundTrafficResponse{
					Inbound:  item.Tag,
					Downlink: item.Downlink,
					Uplink:   item.Uplink,
				})
			}
		}
		if items, err := s.provider.GetAllOutboundsStats(r.Context(), reset); err == nil {
			for _, item := range items {
				outbounds = append(outbounds, outboundListItemResponse{
					Outbound: item.Tag,
					Downlink: item.Downlink,
					Uplink:   item.Uplink,
				})
			}
		}
	}
	write(w, http.StatusOK, envelope[struct {
		Inbounds  []inboundTrafficResponse   `json:"inbounds"`
		Outbounds []outboundListItemResponse `json:"outbounds"`
	}]{Response: struct {
		Inbounds  []inboundTrafficResponse   `json:"inbounds"`
		Outbounds []outboundListItemResponse `json:"outbounds"`
	}{Inbounds: inbounds, Outbounds: outbounds}})
}

func (s *Service) HandleGetUserIPList(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	body := decodeUserIDRequest(r)
	ips := []ipEntryResponse{}
	if s.provider != nil && body.UserID != "" {
		if items, err := s.provider.GetUserIPList(r.Context(), body.UserID, true); err == nil {
			for _, item := range items {
				ips = append(ips, ipEntryResponse{
					IP:       item.IP,
					LastSeen: item.LastSeen.Format(time.RFC3339Nano),
				})
			}
		}
	}
	write(w, http.StatusOK, envelope[struct {
		IPs []ipEntryResponse `json:"ips"`
	}]{Response: struct {
		IPs []ipEntryResponse `json:"ips"`
	}{IPs: ips}})
}

func (s *Service) HandleGetUsersIPList(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	users := []userIPListResponse{}
	if s.provider != nil {
		if items, err := s.provider.GetUsersIPList(r.Context()); err == nil {
			for _, item := range items {
				ips := make([]ipEntryResponse, 0, len(item.IPs))
				for _, ip := range item.IPs {
					ips = append(ips, ipEntryResponse{
						IP:       ip.IP,
						LastSeen: ip.LastSeen.Format(time.RFC3339Nano),
					})
				}
				users = append(users, userIPListResponse{
					UserID: item.UserID,
					IPs:    ips,
				})
			}
		}
	}
	write(w, http.StatusOK, envelope[struct {
		Users []userIPListResponse `json:"users"`
	}]{Response: struct {
		Users []userIPListResponse `json:"users"`
	}{Users: users}})
}

type userTrafficResponse struct {
	Username string `json:"username"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type inboundTrafficResponse struct {
	Inbound  string `json:"inbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type outboundListItemResponse struct {
	Outbound string `json:"outbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type tagTrafficResponse struct {
	Inbound  string `json:"inbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type outboundTrafficResponse struct {
	Outbound string `json:"outbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type ipEntryResponse struct {
	IP       string `json:"ip"`
	LastSeen string `json:"lastSeen"`
}

type userIPListResponse struct {
	UserID string            `json:"userId"`
	IPs    []ipEntryResponse `json:"ips"`
}

func decodeResetRequest(r *http.Request) bool {
	defer r.Body.Close()
	var body struct {
		Reset bool `json:"reset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body.Reset
}

func decodeOnlineRequest(r *http.Request) struct {
	Username string `json:"username"`
} {
	defer r.Body.Close()
	var body struct {
		Username string `json:"username"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body
}

func decodeUserIDRequest(r *http.Request) struct {
	UserID string `json:"userId"`
} {
	defer r.Body.Close()
	var body struct {
		UserID string `json:"userId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body
}

func decodeTagResetRequest(r *http.Request) (string, bool) {
	defer r.Body.Close()
	var body struct {
		Tag   string `json:"tag"`
		Reset bool   `json:"reset"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	return body.Tag, body.Reset
}

func discardBody(r *http.Request) {
	if r.Body == nil {
		return
	}
	defer r.Body.Close()
	_, _ = io.Copy(io.Discard, r.Body)
}
