package stats

import (
	"context"
	"encoding/json"
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
	if s.provider == nil {
		writeAPIError(write, w, errFailedSystemStats)
		return
	}
	stats, err := s.provider.GetSysStats(context.Background())
	if err != nil || stats == nil {
		writeAPIError(write, w, errFailedSystemStats)
		return
	}

	var resp systemStatsResponse
	resp.XrayInfo = stats
	if s.reportsCounter != nil {
		resp.Plugins.TorrentBlocker.ReportsCount = s.reportsCounter.ReportsCount()
	}
	resp.System.Stats = system.GetStats()
	write(w, http.StatusOK, envelope[systemStatsResponse]{Response: resp})
}

func (s *Service) HandleGetUserOnlineStatus(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	body := decodeOnlineRequest(r)
	if s.provider == nil {
		writeAPIError(write, w, errFailedUserOnlineStatus)
		return
	}
	if body.Username == "" {
		write(w, http.StatusOK, envelope[struct {
			IsOnline bool `json:"isOnline"`
		}]{Response: struct {
			IsOnline bool `json:"isOnline"`
		}{IsOnline: false}})
		return
	}
	online, err := s.provider.GetUserOnlineStatus(r.Context(), body.Username)
	if err != nil {
		// Match upstream: SDK errors return isOnline:false on HTTP 200.
		online = false
	}
	write(w, http.StatusOK, envelope[struct {
		IsOnline bool `json:"isOnline"`
	}]{Response: struct {
		IsOnline bool `json:"isOnline"`
	}{IsOnline: online}})
}

func (s *Service) HandleGetUsersStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	if s.provider == nil {
		writeAPIError(write, w, errFailedUsersStats)
		return
	}
	stats, err := s.provider.GetAllUsersStats(r.Context(), reset)
	if err != nil {
		writeAPIError(write, w, errFailedUsersStats)
		return
	}

	users := make([]userTrafficResponse, 0, len(stats))
	for _, item := range stats {
		if item.Uplink == 0 && item.Downlink == 0 {
			continue
		}
		users = append(users, userTrafficResponse{
			Username: item.Username,
			Downlink: item.Downlink,
			Uplink:   item.Uplink,
		})
	}
	write(w, http.StatusOK, envelope[struct {
		Users []userTrafficResponse `json:"users"`
	}]{Response: struct {
		Users []userTrafficResponse `json:"users"`
	}{Users: users}})
}

func (s *Service) HandleGetInboundStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	tag, reset := decodeTagResetRequest(r)
	if s.provider == nil || tag == "" {
		writeAPIError(write, w, errFailedInboundStats)
		return
	}
	stats, err := s.provider.GetInboundStats(r.Context(), tag, reset)
	if err != nil || stats.Tag == "" {
		writeAPIError(write, w, errFailedInboundStats)
		return
	}
	write(w, http.StatusOK, envelope[tagTrafficResponse]{Response: tagTrafficResponse{
		Inbound:  stats.Tag,
		Downlink: stats.Downlink,
		Uplink:   stats.Uplink,
	}})
}

func (s *Service) HandleGetOutboundStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	tag, reset := decodeTagResetRequest(r)
	if s.provider == nil || tag == "" {
		writeAPIError(write, w, errFailedOutboundStats)
		return
	}
	stats, err := s.provider.GetOutboundStats(r.Context(), tag, reset)
	if err != nil || stats.Tag == "" {
		writeAPIError(write, w, errFailedOutboundStats)
		return
	}
	write(w, http.StatusOK, envelope[outboundTrafficResponse]{Response: outboundTrafficResponse{
		Outbound: stats.Tag,
		Downlink: stats.Downlink,
		Uplink:   stats.Uplink,
	}})
}

func (s *Service) HandleGetAllInboundsStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	if s.provider == nil {
		writeAPIError(write, w, errFailedInboundsStats)
		return
	}
	stats, err := s.provider.GetAllInboundsStats(r.Context(), reset)
	if err != nil {
		writeAPIError(write, w, errFailedInboundsStats)
		return
	}
	items := make([]inboundTrafficResponse, 0, len(stats))
	for _, item := range stats {
		items = append(items, inboundTrafficResponse{
			Inbound:  item.Tag,
			Downlink: item.Downlink,
			Uplink:   item.Uplink,
		})
	}
	write(w, http.StatusOK, envelope[struct {
		Inbounds []inboundTrafficResponse `json:"inbounds"`
	}]{Response: struct {
		Inbounds []inboundTrafficResponse `json:"inbounds"`
	}{Inbounds: items}})
}

func (s *Service) HandleGetAllOutboundsStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	if s.provider == nil {
		writeAPIError(write, w, errFailedOutboundsStats)
		return
	}
	stats, err := s.provider.GetAllOutboundsStats(r.Context(), reset)
	if err != nil {
		writeAPIError(write, w, errFailedOutboundsStats)
		return
	}
	items := make([]outboundListItemResponse, 0, len(stats))
	for _, item := range stats {
		items = append(items, outboundListItemResponse{
			Outbound: item.Tag,
			Downlink: item.Downlink,
			Uplink:   item.Uplink,
		})
	}
	write(w, http.StatusOK, envelope[struct {
		Outbounds []outboundListItemResponse `json:"outbounds"`
	}]{Response: struct {
		Outbounds []outboundListItemResponse `json:"outbounds"`
	}{Outbounds: items}})
}

func (s *Service) HandleGetCombinedStats(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	reset := decodeResetRequest(r)
	if s.provider == nil {
		writeAPIError(write, w, errFailedCombinedStats)
		return
	}
	inboundItems, err := s.provider.GetAllInboundsStats(r.Context(), reset)
	if err != nil {
		writeAPIError(write, w, errFailedCombinedStats)
		return
	}
	outboundItems, err := s.provider.GetAllOutboundsStats(r.Context(), reset)
	if err != nil {
		writeAPIError(write, w, errFailedCombinedStats)
		return
	}

	inbounds := make([]inboundTrafficResponse, 0, len(inboundItems))
	for _, item := range inboundItems {
		inbounds = append(inbounds, inboundTrafficResponse{
			Inbound:  item.Tag,
			Downlink: item.Downlink,
			Uplink:   item.Uplink,
		})
	}
	outbounds := make([]outboundListItemResponse, 0, len(outboundItems))
	for _, item := range outboundItems {
		outbounds = append(outbounds, outboundListItemResponse{
			Outbound: item.Tag,
			Downlink: item.Downlink,
			Uplink:   item.Uplink,
		})
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
	if body.UserID == "" {
		write(w, http.StatusOK, envelope[struct {
			IPs []ipEntryResponse `json:"ips"`
		}]{Response: struct {
			IPs []ipEntryResponse `json:"ips"`
		}{IPs: []ipEntryResponse{}}})
		return
	}
	if s.provider == nil {
		writeAPIError(write, w, errFailedUserIPList)
		return
	}
	items, err := s.provider.GetUserIPList(r.Context(), body.UserID, true)
	if err != nil {
		writeAPIError(write, w, errFailedUserIPList)
		return
	}
	ips := make([]ipEntryResponse, 0, len(items))
	for _, item := range items {
		ips = append(ips, ipEntryResponse{
			IP:       item.IP,
			LastSeen: item.LastSeen.Format(time.RFC3339Nano),
		})
	}
	write(w, http.StatusOK, envelope[struct {
		IPs []ipEntryResponse `json:"ips"`
	}]{Response: struct {
		IPs []ipEntryResponse `json:"ips"`
	}{IPs: ips}})
}

func (s *Service) HandleGetUsersIPList(w http.ResponseWriter, r *http.Request, write writeJSONFn) {
	if s.provider == nil {
		writeAPIError(write, w, errFailedUsersIPList)
		return
	}
	items, err := s.provider.GetUsersIPList(r.Context())
	if err != nil {
		writeAPIError(write, w, errFailedUsersIPList)
		return
	}
	users := make([]userIPListResponse, 0, len(items))
	for _, item := range items {
		if len(item.IPs) == 0 {
			continue
		}
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
