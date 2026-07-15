package stats

import (
	"context"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/system"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
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

type SystemStatsResponse struct {
	// Nullable per upstream contract when rw-core is not running yet.
	XrayInfo *xtls.SysStats `json:"xrayInfo"`
	Plugins  struct {
		TorrentBlocker struct {
			ReportsCount int `json:"reportsCount"`
		} `json:"torrentBlocker"`
	} `json:"plugins"`
	System struct {
		Stats system.Stats `json:"stats"`
	} `json:"system"`
}

type UserOnlineStatusResponse struct {
	IsOnline bool `json:"isOnline"`
}

type UsersStatsResponse struct {
	Users []UserTrafficResponse `json:"users"`
}

type UserTrafficResponse struct {
	Username string `json:"username"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type InboundStatsResponse struct {
	Inbound  string `json:"inbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type OutboundStatsResponse struct {
	Outbound string `json:"outbound"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type AllInboundsStatsResponse struct {
	Inbounds []InboundStatsResponse `json:"inbounds"`
}

type AllOutboundsStatsResponse struct {
	Outbounds []OutboundStatsResponse `json:"outbounds"`
}

type CombinedStatsResponse struct {
	Inbounds  []InboundStatsResponse  `json:"inbounds"`
	Outbounds []OutboundStatsResponse `json:"outbounds"`
}

type IPEntryResponse struct {
	IP       string `json:"ip"`
	LastSeen string `json:"lastSeen"`
}

type UserIPListResponse struct {
	UserID string            `json:"userId"`
	IPs    []IPEntryResponse `json:"ips"`
}

type GetUserIPListResponse struct {
	IPs []IPEntryResponse `json:"ips"`
}

type GetUsersIPListResponse struct {
	Users []UserIPListResponse `json:"users"`
}

func (s *Service) GetSystemStats(ctx context.Context) (SystemStatsResponse, error) {
	if s.provider == nil {
		return SystemStatsResponse{}, errFailedSystemStats
	}

	stats, err := s.provider.GetSysStats(ctx)
	if err != nil || stats == nil {
		return SystemStatsResponse{}, errFailedSystemStats
	}

	var response SystemStatsResponse
	response.XrayInfo = stats
	if s.reportsCounter != nil {
		response.Plugins.TorrentBlocker.ReportsCount = s.reportsCounter.ReportsCount()
	}
	response.System.Stats = system.GetStats()
	return response, nil
}

func (s *Service) GetUserOnlineStatus(ctx context.Context, username string) UserOnlineStatusResponse {
	if s.provider == nil {
		return UserOnlineStatusResponse{IsOnline: false}
	}
	online, err := s.provider.GetUserOnlineStatus(ctx, username)
	if err != nil {
		online = false
	}
	return UserOnlineStatusResponse{IsOnline: online}
}

func (s *Service) GetUsersStats(ctx context.Context, reset bool) (UsersStatsResponse, error) {
	if s.provider == nil {
		return UsersStatsResponse{}, errFailedUsersStats
	}
	items, err := s.provider.GetAllUsersStats(ctx, reset)
	if err != nil {
		return UsersStatsResponse{}, errFailedUsersStats
	}

	users := make([]UserTrafficResponse, 0, len(items))
	for _, item := range items {
		if item.Uplink == 0 && item.Downlink == 0 {
			continue
		}
		users = append(users, UserTrafficResponse{
			Username: item.Username,
			Downlink: item.Downlink,
			Uplink:   item.Uplink,
		})
	}
	return UsersStatsResponse{Users: users}, nil
}

func (s *Service) GetInboundStats(ctx context.Context, tag string, reset bool) (InboundStatsResponse, error) {
	if s.provider == nil {
		return InboundStatsResponse{}, errFailedInboundStats
	}
	item, err := s.provider.GetInboundStats(ctx, tag, reset)
	if err != nil || item.Tag == "" {
		return InboundStatsResponse{}, errFailedInboundStats
	}
	return InboundStatsResponse{Inbound: item.Tag, Downlink: item.Downlink, Uplink: item.Uplink}, nil
}

func (s *Service) GetOutboundStats(ctx context.Context, tag string, reset bool) (OutboundStatsResponse, error) {
	if s.provider == nil {
		return OutboundStatsResponse{}, errFailedOutboundStats
	}
	item, err := s.provider.GetOutboundStats(ctx, tag, reset)
	if err != nil || item.Tag == "" {
		return OutboundStatsResponse{}, errFailedOutboundStats
	}
	return OutboundStatsResponse{Outbound: item.Tag, Downlink: item.Downlink, Uplink: item.Uplink}, nil
}

func (s *Service) GetAllInboundsStats(ctx context.Context, reset bool) (AllInboundsStatsResponse, error) {
	if s.provider == nil {
		return AllInboundsStatsResponse{}, errFailedInboundsStats
	}
	stats, err := s.provider.GetAllInboundsStats(ctx, reset)
	if err != nil {
		return AllInboundsStatsResponse{}, errFailedInboundsStats
	}
	return AllInboundsStatsResponse{Inbounds: mapInbounds(stats)}, nil
}

func (s *Service) GetAllOutboundsStats(ctx context.Context, reset bool) (AllOutboundsStatsResponse, error) {
	if s.provider == nil {
		return AllOutboundsStatsResponse{}, errFailedOutboundsStats
	}
	stats, err := s.provider.GetAllOutboundsStats(ctx, reset)
	if err != nil {
		return AllOutboundsStatsResponse{}, errFailedOutboundsStats
	}
	return AllOutboundsStatsResponse{Outbounds: mapOutbounds(stats)}, nil
}

func (s *Service) GetCombinedStats(ctx context.Context, reset bool) (CombinedStatsResponse, error) {
	if s.provider == nil {
		return CombinedStatsResponse{}, errFailedCombinedStats
	}
	inbounds, err := s.provider.GetAllInboundsStats(ctx, reset)
	if err != nil {
		return CombinedStatsResponse{}, errFailedCombinedStats
	}
	outbounds, err := s.provider.GetAllOutboundsStats(ctx, reset)
	if err != nil {
		return CombinedStatsResponse{}, errFailedCombinedStats
	}
	return CombinedStatsResponse{
		Inbounds:  mapInbounds(inbounds),
		Outbounds: mapOutbounds(outbounds),
	}, nil
}

func (s *Service) GetUserIPList(ctx context.Context, userID string) GetUserIPListResponse {
	empty := GetUserIPListResponse{IPs: []IPEntryResponse{}}
	if s.provider == nil {
		return empty
	}
	items, err := s.provider.GetUserIPList(ctx, userID, true)
	if err != nil {
		return empty
	}

	ips := make([]IPEntryResponse, 0, len(items))
	for _, item := range items {
		ips = append(ips, IPEntryResponse{
			IP:       item.IP,
			LastSeen: item.LastSeen.Format(time.RFC3339Nano),
		})
	}
	return GetUserIPListResponse{IPs: ips}
}

func (s *Service) GetUsersIPList(ctx context.Context) GetUsersIPListResponse {
	empty := GetUsersIPListResponse{Users: []UserIPListResponse{}}
	if s.provider == nil {
		return empty
	}
	items, err := s.provider.GetUsersIPList(ctx)
	if err != nil {
		return empty
	}

	users := make([]UserIPListResponse, 0, len(items))
	for _, item := range items {
		ips := make([]IPEntryResponse, 0, len(item.IPs))
		for _, ip := range item.IPs {
			ips = append(ips, IPEntryResponse{
				IP:       ip.IP,
				LastSeen: ip.LastSeen.Format(time.RFC3339Nano),
			})
		}
		users = append(users, UserIPListResponse{UserID: item.UserID, IPs: ips})
	}
	return GetUsersIPListResponse{Users: users}
}

func mapInbounds(items []xtls.TagTraffic) []InboundStatsResponse {
	result := make([]InboundStatsResponse, 0, len(items))
	for _, item := range items {
		result = append(result, InboundStatsResponse{
			Inbound: item.Tag, Downlink: item.Downlink, Uplink: item.Uplink,
		})
	}
	return result
}

func mapOutbounds(items []xtls.TagTraffic) []OutboundStatsResponse {
	result := make([]OutboundStatsResponse, 0, len(items))
	for _, item := range items {
		result = append(result, OutboundStatsResponse{
			Outbound: item.Tag, Downlink: item.Downlink, Uplink: item.Uplink,
		})
	}
	return result
}
