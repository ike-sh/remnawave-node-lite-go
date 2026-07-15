package xtls

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls/xrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SysStats struct {
	NumGoroutine int   `json:"numGoroutine"`
	NumGC        int   `json:"numGC"`
	Alloc        int64 `json:"alloc"`
	TotalAlloc   int64 `json:"totalAlloc"`
	Sys          int64 `json:"sys"`
	Mallocs      int64 `json:"mallocs"`
	Frees        int64 `json:"frees"`
	LiveObjects  int64 `json:"liveObjects"`
	PauseTotalNs int64 `json:"pauseTotalNs"`
	Uptime       int64 `json:"uptime"`
}

type UserTraffic struct {
	Username string `json:"username"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type TagTraffic struct {
	Tag      string `json:"tag"`
	Downlink int64  `json:"downlink"`
	Uplink   int64  `json:"uplink"`
}

type IPEntry struct {
	IP       string    `json:"ip"`
	LastSeen time.Time `json:"lastSeen"`
}

type UserIPEntry struct {
	UserID string    `json:"userId"`
	IPs    []IPEntry `json:"ips"`
}

type StatsAPI struct {
	conn         grpc.ClientConnInterface
	capabilities *StatsCapabilities
}

// StatsCapabilities caches optional rw-core RPC support across short-lived API clients.
type StatsCapabilities struct {
	usersStats atomic.Int32
}

const (
	usersStatsUnknown int32 = iota
	usersStatsSupported
	usersStatsLegacy
)

const (
	statsGetSysStatsMethod          = "/xray.app.stats.command.StatsService/GetSysStats"
	statsGetStatsOnlineMethod       = "/xray.app.stats.command.StatsService/GetStatsOnline"
	statsQueryStatsMethod           = "/xray.app.stats.command.StatsService/QueryStats"
	statsGetStatsOnlineIPListMethod = "/xray.app.stats.command.StatsService/GetStatsOnlineIpList"
	statsGetAllOnlineUsersMethod    = "/xray.app.stats.command.StatsService/GetAllOnlineUsers"
	getUsersStatsMethod             = "/xray.app.stats.command.StatsService/GetUsersStats"
	legacyIPLookupWorkers           = 8
)

func NewStatsAPI(conn grpc.ClientConnInterface, capabilities *StatsCapabilities) *StatsAPI {
	if capabilities == nil {
		capabilities = &StatsCapabilities{}
	}
	return &StatsAPI{
		conn:         conn,
		capabilities: capabilities,
	}
}

func (s *StatsAPI) GetSysStats(ctx context.Context) (*SysStats, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.SysStatsResponse{}
	err := s.invoke(ctx, statsGetSysStatsMethod, &xrpc.Empty{}, resp)
	if err != nil {
		return nil, err
	}
	return &SysStats{
		NumGoroutine: int(resp.NumGoroutine),
		NumGC:        int(resp.NumGc),
		Alloc:        int64(resp.Alloc),
		TotalAlloc:   int64(resp.TotalAlloc),
		Sys:          int64(resp.Sys),
		Mallocs:      int64(resp.Mallocs),
		Frees:        int64(resp.Frees),
		LiveObjects:  int64(resp.LiveObjects),
		PauseTotalNs: int64(resp.PauseTotalNs),
		Uptime:       int64(resp.Uptime),
	}, nil
}

func (s *StatsAPI) GetUserOnlineStatus(ctx context.Context, username string) (bool, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	err := s.invoke(ctx, statsGetStatsOnlineMethod, &xrpc.GetStatsRequest{
		Name:   fmt.Sprintf("user>>>%s>>>online", username),
		Reset_: false,
	}, &xrpc.GetStatsResponse{})
	if err == nil {
		return true, nil
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		return false, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return false, nil
	}
	return false, err
}

func (s *StatsAPI) GetAllUsersStats(ctx context.Context, reset bool) ([]UserTraffic, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	// Align with official @remnawave/xtls-sdk getAllUsersStats(): QueryStats only.
	// Preferring GetUsersStats here returns empty traffic on rw-core even when counters exist.
	resp := &xrpc.QueryStatsResponse{}
	err := s.invoke(ctx, statsQueryStatsMethod, &xrpc.QueryStatsRequest{
		Pattern: "user>>>",
		Reset_:  reset,
	}, resp)
	if err != nil {
		return nil, err
	}
	return parseUserTrafficStats(resp.Stat), nil
}

func (s *StatsAPI) GetInboundStats(ctx context.Context, tag string, reset bool) (TagTraffic, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.QueryStatsResponse{}
	err := s.invoke(ctx, statsQueryStatsMethod, &xrpc.QueryStatsRequest{
		Pattern: fmt.Sprintf("inbound>>>%s>>>", tag),
		Reset_:  reset,
	}, resp)
	if err != nil {
		return TagTraffic{}, err
	}
	traffic := parseTagTraffic(resp.Stat, "inbound")
	traffic.Tag = tag
	return traffic, nil
}

func (s *StatsAPI) GetOutboundStats(ctx context.Context, tag string, reset bool) (TagTraffic, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.QueryStatsResponse{}
	err := s.invoke(ctx, statsQueryStatsMethod, &xrpc.QueryStatsRequest{
		Pattern: fmt.Sprintf("outbound>>>%s>>>", tag),
		Reset_:  reset,
	}, resp)
	if err != nil {
		return TagTraffic{}, err
	}
	traffic := parseTagTraffic(resp.Stat, "outbound")
	traffic.Tag = tag
	return traffic, nil
}

func (s *StatsAPI) GetAllInboundsStats(ctx context.Context, reset bool) ([]TagTraffic, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.QueryStatsResponse{}
	err := s.invoke(ctx, statsQueryStatsMethod, &xrpc.QueryStatsRequest{
		Pattern: "inbound>>>",
		Reset_:  reset,
	}, resp)
	if err != nil {
		return nil, err
	}
	return parseAllTagTraffic(resp.Stat, "inbound"), nil
}

func (s *StatsAPI) GetAllOutboundsStats(ctx context.Context, reset bool) ([]TagTraffic, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.QueryStatsResponse{}
	err := s.invoke(ctx, statsQueryStatsMethod, &xrpc.QueryStatsRequest{
		Pattern: "outbound>>>",
		Reset_:  reset,
	}, resp)
	if err != nil {
		return nil, err
	}
	return parseAllTagTraffic(resp.Stat, "outbound"), nil
}

func (s *StatsAPI) Ping(ctx context.Context) error {
	ctx, cancel := withRPCDeadline(ctx, 3*time.Second)
	defer cancel()
	return s.invoke(ctx, statsGetSysStatsMethod, &xrpc.Empty{}, &xrpc.SysStatsResponse{})
}

func (s *StatsAPI) GetUserIPList(ctx context.Context, userID string, reset bool) ([]IPEntry, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	resp := &xrpc.GetStatsOnlineIpListResponse{}
	err := s.invoke(ctx, statsGetStatsOnlineIPListMethod, &xrpc.GetStatsRequest{
		Name:   fmt.Sprintf("user>>>%s>>>online", userID),
		Reset_: reset,
	}, resp)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return []IPEntry{}, nil
		}
		return nil, err
	}
	return mapIPList(resp.GetIps()), nil
}

func (s *StatsAPI) GetUsersIPList(ctx context.Context) ([]UserIPEntry, error) {
	ctx, cancel := withRPCTimeout(ctx)
	defer cancel()
	if s.capabilities.usersStats.Load() != usersStatsLegacy {
		items, err := s.getUsersIPListNative(ctx)
		if err == nil {
			s.capabilities.usersStats.Store(usersStatsSupported)
			return items, nil
		}
		if status.Code(err) != codes.Unimplemented {
			return nil, err
		}
		s.capabilities.usersStats.Store(usersStatsLegacy)
	}
	return s.getUsersIPListLegacy(ctx)
}

func (s *StatsAPI) getUsersIPListNative(ctx context.Context) ([]UserIPEntry, error) {
	response := &xrpc.GetUsersStatsResponse{}
	err := s.invoke(ctx, getUsersStatsMethod, &xrpc.GetUsersStatsRequest{IncludeTraffic: false, Reset_: true}, response)
	if err != nil {
		return nil, err
	}

	users := make([]UserIPEntry, 0, len(response.GetUsers()))
	for _, user := range response.GetUsers() {
		if user == nil {
			continue
		}
		ips := make([]IPEntry, 0, len(user.GetIps()))
		for _, item := range user.GetIps() {
			if item == nil {
				continue
			}
			ips = append(ips, IPEntry{
				IP:       item.GetIp(),
				LastSeen: time.Unix(item.GetLastSeen(), 0).UTC(),
			})
		}
		users = append(users, UserIPEntry{UserID: user.GetEmail(), IPs: ips})
	}
	return users, nil
}

func (s *StatsAPI) getUsersIPListLegacy(ctx context.Context) ([]UserIPEntry, error) {
	resp := &xrpc.GetAllOnlineUsersResponse{}
	err := s.invoke(ctx, statsGetAllOnlineUsersMethod, &xrpc.Empty{}, resp)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return []UserIPEntry{}, nil
		}
		return nil, err
	}

	userIDs := uniqueOnlineUserIDs(resp.GetUsers())
	if len(userIDs) == 0 {
		return []UserIPEntry{}, nil
	}

	results := make([]UserIPEntry, len(userIDs))
	var wg sync.WaitGroup
	var cursor atomic.Uint64
	workerCount := min(legacyIPLookupWorkers, len(userIDs))
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				index := int(cursor.Add(1) - 1)
				if index >= len(userIDs) || ctx.Err() != nil {
					return
				}
				userID := userIDs[index]
				ips, err := s.GetUserIPList(ctx, userID, true)
				if err == nil && len(ips) != 0 {
					results[index] = UserIPEntry{UserID: userID, IPs: ips}
				}
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	filtered := make([]UserIPEntry, 0, len(userIDs))
	for _, item := range results {
		if len(item.IPs) > 0 {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func mapIPList(raw map[string]int64) []IPEntry {
	if len(raw) == 0 {
		return []IPEntry{}
	}
	items := make([]IPEntry, 0, len(raw))
	for ip, timestamp := range raw {
		items = append(items, IPEntry{
			IP:       ip,
			LastSeen: time.Unix(timestamp, 0).UTC(),
		})
	}
	return items
}

func uniqueOnlineUserIDs(metrics []string) []string {
	seen := map[string]struct{}{}
	users := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		userID := extractOnlineUserID(metric)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		users = append(users, userID)
	}
	return users
}

func extractOnlineUserID(raw string) string {
	parts := strings.Split(raw, ">>>")
	if len(parts) < 3 || parts[0] != "user" {
		return ""
	}
	return parts[1]
}

func (s *StatsAPI) invoke(ctx context.Context, method string, request, response any) error {
	return s.conn.Invoke(ctx, method, request, response, grpc.StaticMethod())
}

func parseUserTrafficStats(stats []*xrpc.Stat) []UserTraffic {
	users := map[string]*UserTraffic{}
	for _, stat := range stats {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) < 4 || parts[0] != "user" {
			continue
		}
		username := parts[1]
		direction := parts[3]
		entry, ok := users[username]
		if !ok {
			entry = &UserTraffic{Username: username}
			users[username] = entry
		}
		switch direction {
		case "downlink":
			entry.Downlink = stat.Value
		case "uplink":
			entry.Uplink = stat.Value
		}
	}
	result := make([]UserTraffic, 0, len(users))
	for _, user := range users {
		result = append(result, *user)
	}
	return result
}

func parseTagTraffic(stats []*xrpc.Stat, prefix string) TagTraffic {
	traffic := TagTraffic{}
	for _, stat := range stats {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) < 4 || parts[0] != prefix {
			continue
		}
		if traffic.Tag == "" {
			traffic.Tag = parts[1]
		}
		switch parts[3] {
		case "downlink":
			traffic.Downlink = stat.Value
		case "uplink":
			traffic.Uplink = stat.Value
		}
	}
	return traffic
}

func parseAllTagTraffic(stats []*xrpc.Stat, prefix string) []TagTraffic {
	tags := map[string]*TagTraffic{}
	for _, stat := range stats {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) < 4 || parts[0] != prefix {
			continue
		}
		tag := parts[1]
		entry, ok := tags[tag]
		if !ok {
			entry = &TagTraffic{Tag: tag}
			tags[tag] = entry
		}
		switch parts[3] {
		case "downlink":
			entry.Downlink = stat.Value
		case "uplink":
			entry.Uplink = stat.Value
		}
	}
	result := make([]TagTraffic, 0, len(tags))
	for _, tag := range tags {
		result = append(result, *tag)
	}
	return result
}
