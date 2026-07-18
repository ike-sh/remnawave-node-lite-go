package plugin

import (
	"context"
	"reflect"
	"testing"
)

type stubASNResolver struct{}

type oversizedASNResolver struct{}

func (stubASNResolver) PrefixesByASN(asn uint32) (ipv4, ipv6 []string) {
	if asn == 13335 {
		return []string{"1.1.1.0/24"}, []string{"2606:4700::/32"}
	}
	return nil, nil
}

func (oversizedASNResolver) PrefixesByASN(uint32) (ipv4, ipv6 []string) {
	return make([]string, maxResolvedIPItems+1), nil
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
	shared, err := buildSharedIPMapWithDiagnosticsContext(context.Background(), cfg, stubASNResolver{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := shared["ext:cf"]; !reflect.DeepEqual(got, []string{"1.1.1.0/24", "2606:4700::/32"}) {
		t.Errorf("ext:cf = %v", got)
	}
	if got := shared["ext:ips"]; !reflect.DeepEqual(got, []string{"9.9.9.9"}) {
		t.Errorf("ext:ips = %v", got)
	}
}

func TestBuildSharedIPMapDuplicateNameUsesLastList(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"sharedLists": []any{
			map[string]any{"name": "ext:duplicate", "type": "asList", "items": []any{float64(13335)}},
			map[string]any{"name": "ext:duplicate", "type": "ipList", "items": []any{"9.9.9.9"}},
		},
	}
	shared, err := buildSharedIPMapWithDiagnosticsContext(context.Background(), cfg, stubASNResolver{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := shared["ext:duplicate"]; !reflect.DeepEqual(got, []string{"9.9.9.9"}) {
		t.Errorf("ext:duplicate = %v, want the last list's items", got)
	}
}

func TestBuildSharedIPMapASListWithoutResolver(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"sharedLists": []any{
			map[string]any{"name": "ext:cf", "type": "asList", "items": []any{float64(13335)}},
		},
	}
	shared, err := buildSharedIPMapWithDiagnosticsContext(context.Background(), cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(shared["ext:cf"]) != 0 {
		t.Errorf("expected empty resolution without resolver, got %v", shared["ext:cf"])
	}
}

func TestBuildPluginPlanRejectsOversizedASNExpansion(t *testing.T) {
	t.Parallel()

	request := mustSyncPlugin(t, map[string]any{
		"uuid": "00000000-0000-4000-8000-000000000001",
		"name": "test",
		"config": map[string]any{
			"sharedLists": []any{
				map[string]any{"name": "ext:huge", "type": "asList", "items": []any{float64(64500)}},
			},
		},
	})
	if _, err := buildPluginPlan(request, oversizedASNResolver{}, true); err == nil {
		t.Fatal("oversized ASN expansion was accepted")
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
	if err := validateSharedLists([]any{
		map[string]any{"name": "ext:google", "type": "asList", "items": []any{"AS15169"}},
	}); err == nil {
		t.Error("string ASN should fail the official numeric schema")
	}
	if err := validateSharedLists([]any{
		map[string]any{"name": "ext:max", "type": "asList", "items": []any{float64(4294967296)}},
	}); err == nil {
		t.Error("ASN above uint32 should fail validation")
	}
}

func TestValidateSharedListsAcceptsMaximumASN(t *testing.T) {
	t.Parallel()
	if err := validateSharedLists([]any{
		map[string]any{"name": "ext:max", "type": "asList", "items": []any{float64(4294967295)}},
	}); err != nil {
		t.Fatalf("maximum uint32 ASN rejected: %v", err)
	}
}
