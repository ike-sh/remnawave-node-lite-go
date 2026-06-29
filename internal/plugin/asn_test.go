package plugin

import (
	"reflect"
	"testing"
)

type stubASNResolver struct{}

func (stubASNResolver) PrefixesByASN(asn uint32) (ipv4, ipv6 []string) {
	if asn == 13335 {
		return []string{"1.1.1.0/24"}, []string{"2606:4700::/32"}
	}
	return nil, nil
}

func TestParseASN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   any
		want uint32
		ok   bool
	}{
		{float64(13335), 13335, true},
		{"AS15169", 15169, true},
		{"15169", 15169, true},
		{float64(0), 0, false},
		{float64(-1), 0, false},
		{"notanasn", 0, false},
		{float64(1.5), 0, false},
	}
	for _, tc := range cases {
		got, ok := parseASN(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseASN(%v) = (%d,%v), want (%d,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuildSharedIPMapResolvesASList(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"sharedLists": []any{
			map[string]any{"name": "ext:cf", "type": "asList", "items": []any{float64(13335)}},
			map[string]any{"name": "ext:ips", "type": "ipList", "items": []any{"9.9.9.9"}},
		},
	}
	shared := buildSharedIPMap(cfg, stubASNResolver{})

	if got := shared["ext:cf"]; !reflect.DeepEqual(got, []string{"1.1.1.0/24", "2606:4700::/32"}) {
		t.Errorf("ext:cf = %v", got)
	}
	if got := shared["ext:ips"]; !reflect.DeepEqual(got, []string{"9.9.9.9"}) {
		t.Errorf("ext:ips = %v", got)
	}
}

func TestBuildSharedIPMapASListWithoutResolver(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"sharedLists": []any{
			map[string]any{"name": "ext:cf", "type": "asList", "items": []any{float64(13335)}},
		},
	}
	shared := buildSharedIPMap(cfg, nil)
	if len(shared["ext:cf"]) != 0 {
		t.Errorf("expected empty resolution without resolver, got %v", shared["ext:cf"])
	}
}

func TestValidateSharedListsAcceptsASList(t *testing.T) {
	t.Parallel()
	if err := validateSharedLists([]any{
		map[string]any{"name": "ext:cf", "type": "asList", "items": []any{float64(13335)}},
	}); err != nil {
		t.Errorf("asList should validate, got %v", err)
	}
}

func TestValidateSharedListsRejectsBadASNAndType(t *testing.T) {
	t.Parallel()
	if err := validateSharedLists([]any{
		map[string]any{"name": "ext:cf", "type": "asList", "items": []any{float64(0)}},
	}); err == nil {
		t.Error("asList with asn 0 should fail validation")
	}
	if err := validateSharedLists([]any{
		map[string]any{"name": "ext:x", "type": "unknownList", "items": []any{}},
	}); err == nil {
		t.Error("unknown shared list type should fail validation")
	}
}
