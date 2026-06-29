package xtls

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	statscommand "github.com/xtls/xray-core/app/stats/command"
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
	client statscommand.StatsServiceClient
	conn   *grpc.ClientConn
}

func NewStatsAPI(conn *grpc.ClientConn) *StatsAPI {
	return &StatsAPI{
		client: statscommand.NewStatsServiceClient(conn),
		conn:   conn,
	}
}

func (s *StatsAPI) GetSysStats(ctx context.Context) (*SysStats, error) {
	resp, err := s.client.GetSysStats(ctx, &statscommand.SysStatsRequest{})
	if err != nil {
		return nil, err
	}
	return &SysStats{
		NumGoroutine: int(resp.NumGoroutine),
		NumGC:        int(resp.NumGC),
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
	_, err := s.client.GetStatsOnline(ctx, &statscommand.GetStatsRequest{
		Name:   fmt.Sprintf("user>>>%s>>>online", username),
		Reset_: false,
	})
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
	// Align with official @remnawave/xtls-sdk getAllUsersStats(): QueryStats only.
	// Preferring GetUsersStats here returns empty traffic on rw-core even when counters exist.
	resp, err := s.client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: "user>>>",
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}
	return parseUserTrafficStats(resp.Stat), nil
}

func (s *StatsAPI) GetInboundStats(ctx context.Context, tag string, reset bool) (TagTraffic, error) {
	resp, err := s.client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: fmt.Sprintf("inbound>>>%s>>>", tag),
		Reset_:  reset,
	})
	if err != nil {
		return TagTraffic{}, err
	}
	traffic := parseTagTraffic(resp.Stat, "inbound")
	traffic.Tag = tag
	return traffic, nil
}

func (s *StatsAPI) GetOutboundStats(ctx context.Context, tag string, reset bool) (TagTraffic, error) {
	resp, err := s.client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: fmt.Sprintf("outbound>>>%s>>>", tag),
		Reset_:  reset,
	})
	if err != nil {
		return TagTraffic{}, err
	}
	traffic := parseTagTraffic(resp.Stat, "outbound")
	traffic.Tag = tag
	return traffic, nil
}

func (s *StatsAPI) GetAllInboundsStats(ctx context.Context, reset bool) ([]TagTraffic, error) {
	resp, err := s.client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: "inbound>>>",
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}
	return parseAllTagTraffic(resp.Stat, "inbound"), nil
}

func (s *StatsAPI) GetAllOutboundsStats(ctx context.Context, reset bool) ([]TagTraffic, error) {
	resp, err := s.client.QueryStats(ctx, &statscommand.QueryStatsRequest{
		Pattern: "outbound>>>",
		Reset_:  reset,
	})
	if err != nil {
		return nil, err
	}
	return parseAllTagTraffic(resp.Stat, "outbound"), nil
}

func (s *StatsAPI) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := s.client.GetSysStats(ctx, &statscommand.SysStatsRequest{})
	return err
}

func (s *StatsAPI) GetUserIPList(ctx context.Context, userID string, reset bool) ([]IPEntry, error) {
	resp, err := s.client.GetStatsOnlineIpList(ctx, &statscommand.GetStatsRequest{
		Name:   fmt.Sprintf("user>>>%s>>>online", userID),
		Reset_: reset,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return []IPEntry{}, nil
		}
		return nil, err
	}
	return mapIPList(resp.GetIps()), nil
}

func (s *StatsAPI) GetUsersIPList(ctx context.Context) ([]UserIPEntry, error) {
	resp, err := s.client.GetAllOnlineUsers(ctx, &statscommand.GetAllOnlineUsersRequest{})
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
	sem := make(chan struct{}, 50)
	var wg sync.WaitGroup

	for index, userID := range userIDs {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ips, err := s.GetUserIPList(ctx, id, true)
			if err != nil || len(ips) == 0 {
				return
			}
			results[i] = UserIPEntry{UserID: id, IPs: ips}
		}(index, userID)
	}
	wg.Wait()

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

func parseUserTrafficStats(stats []*statscommand.Stat) []UserTraffic {
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

func parseTagTraffic(stats []*statscommand.Stat, prefix string) TagTraffic {
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

func parseAllTagTraffic(stats []*statscommand.Stat, prefix string) []TagTraffic {
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
