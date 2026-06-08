package xtls

import "testing"

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
