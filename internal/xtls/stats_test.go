package xtls

import (
	"testing"

	statscommand "github.com/xtls/xray-core/app/stats/command"
)

func TestExtractOnlineUserID(t *testing.T) {
	if got := extractOnlineUserID("user>>>alice@example.com>>>online"); got != "alice@example.com" {
		t.Fatalf("unexpected user id: %q", got)
	}
	if got := extractOnlineUserID("invalid"); got != "" {
		t.Fatalf("expected empty for invalid metric")
	}
}

func TestUniqueOnlineUserIDs(t *testing.T) {
	users := uniqueOnlineUserIDs([]string{
		"user>>>a>>>online",
		"user>>>a>>>online",
		"user>>>b>>>online",
	})
	if len(users) != 2 || users[0] != "a" || users[1] != "b" {
		t.Fatalf("unexpected users: %#v", users)
	}
}

func TestParseUserTrafficStats(t *testing.T) {
	stats := []*statscommand.Stat{
		{Name: "user>>>alice@example.com>>>traffic>>>uplink", Value: 100},
		{Name: "user>>>alice@example.com>>>traffic>>>downlink", Value: 200},
	}
	users := parseUserTrafficStats(stats)
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Username != "alice@example.com" || users[0].Uplink != 100 || users[0].Downlink != 200 {
		t.Fatalf("unexpected user stats: %#v", users[0])
	}
}

func TestParseAllTagTraffic(t *testing.T) {
	stats := []*statscommand.Stat{
		{Name: "inbound>>>vless-in>>>traffic>>>uplink", Value: 10},
		{Name: "inbound>>>vless-in>>>traffic>>>downlink", Value: 20},
	}
	items := parseAllTagTraffic(stats, "inbound")
	if len(items) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(items))
	}
	if items[0].Tag != "vless-in" || items[0].Uplink != 10 || items[0].Downlink != 20 {
		t.Fatalf("unexpected inbound stats: %#v", items[0])
	}
}
