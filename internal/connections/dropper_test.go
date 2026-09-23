package connections

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"remnawave-node-lite-go/internal/xtls"
)

type testIPProvider map[string][]xtls.IPEntry

func (p testIPProvider) GetUserIPList(_ context.Context, userID string, _ bool) ([]xtls.IPEntry, error) {
	return p[userID], nil
}

func TestDropIPsDeduplicatesAndContinuesOnFailure(t *testing.T) {
	var killed []string
	d := &Dropper{available: true, isWhitelisted: func(ip string) bool { return ip == "198.51.100.2" }, kill: func(ip string) error {
		killed = append(killed, ip)
		if ip == "203.0.113.1" {
			return errors.New("permission denied")
		}
		return nil
	}}
	if !d.DropIPs([]string{"203.0.113.1", "203.0.113.1", "198.51.100.2", "2001:db8::1", ""}) {
		t.Fatal("official async REST result should remain accepted")
	}
	if !reflect.DeepEqual(killed, []string{"203.0.113.1", "2001:db8::1"}) {
		t.Fatalf("killed = %v", killed)
	}
}

func TestDropUsersOneMultipleAndNoSocket(t *testing.T) {
	var killed []string
	d := &Dropper{available: true, isWhitelisted: func(string) bool { return false }, kill: func(ip string) error { killed = append(killed, ip); return nil }}
	provider := testIPProvider{"u1": {{IP: "192.0.2.1"}}, "u2": {{IP: "2001:db8::2"}}, "empty": {}}
	if !d.DropUsers(context.Background(), provider, []string{"u1", "u2", "empty"}) {
		t.Fatal("drop users rejected")
	}
	if !reflect.DeepEqual(killed, []string{"192.0.2.1", "2001:db8::2"}) {
		t.Fatalf("killed = %v", killed)
	}
}
