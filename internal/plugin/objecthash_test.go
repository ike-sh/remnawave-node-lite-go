package plugin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHashPluginConfigMatchesNodeObjectHash(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		hash string
		sort string
	}{
		{
			name: "full plugin skeleton",
			raw:  `{"sharedLists":[],"ingressFilter":{"enabled":false,"blockedIps":[]},"connectionDrop":{"enabled":true,"whitelistIps":["127.0.0.1"]},"torrentBlocker":{"enabled":false,"blockDuration":300,"includeRuleTags":[],"ignoreLists":{"ip":[],"userId":[]}},"egressFilter":{"enabled":false,"blockedIps":[],"blockedPorts":[]}}`,
			hash: "f97574f43d6818ffdcd6025ff63ba6043b3e678e66edae3d2c7f8ff5db3fd044",
			sort: "{sharedLists:[],ingressFilter:{enabled:0,blockedIps:[]},connectionDrop:{enabled:1,whitelistIps:[127.0.0.1]},torrentBlocker:{enabled:0,blockDuration:300,includeRuleTags:[],ignoreLists:{ip:[],userId:[]}},egressFilter:{enabled:0,blockedIps:[],blockedPorts:[]}}",
		},
		{
			name: "trim string",
			raw:  `{"a":1,"b":" x "}`,
			hash: "2ca5871e7557d67ac8d2ce9b7cdf93fde5c56727bd4f975de2c3e9f904db2121",
			sort: "{a:1,b:x}",
		},
		{
			name: "preserve key order",
			raw:  `{"nested":{"z":1,"y":2}}`,
			hash: "1e116c8ccee3b8dec1e8605259faf195ab85043fc19e6f44f61e9b02e5d5a985",
			sort: "{nested:{z:1,y:2}}",
		},
		{
			name: "normalize exponent to integer",
			raw:  `{"n":1e3}`,
			hash: "714e417f74bb5d72cdd1c3179dfae4d085cd9791d0de5af0a1d96b3f3db835b6",
			sort: "{n:1000}",
		},
		{
			name: "normalize negative zero",
			raw:  `{"n":-0}`,
			hash: "10b0e5ca77353a8c1727db43d5c62bc98149a3917e30612e43f9c5a09b3809cb",
			sort: "{n:0}",
		},
		{
			name: "javascript fixed lower boundary",
			raw:  `{"n":1e-6}`,
			hash: "b0c42483948f57227fc1b7105266b4fa29432b1f8d9876d26832a7fc09ef835b",
			sort: "{n:0.000001}",
		},
		{
			name: "javascript exponent below fixed boundary",
			raw:  `{"n":1e-7}`,
			hash: "95c8c062f697cb0db861f47f15e36abe2e9089a7f82eacd81092c8ed57dbb106",
			sort: "{n:1e-7}",
		},
		{
			name: "javascript fixed upper boundary",
			raw:  `{"n":1e20}`,
			hash: "07e708f1a3076e5719a629635a9d8c0b791fcd1e2a56c7015b4829ee7dd25945",
			sort: "{n:100000000000000000000}",
		},
		{
			name: "javascript exponent upper boundary",
			raw:  `{"n":1e21}`,
			hash: "7dc738fa1ae3483c57a51dd787082c4ea866e0fe0cccb70f5c9f5846c69567b6",
			sort: "{n:1e+21}",
		},
		{
			name: "javascript unsafe integer rounding",
			raw:  `{"n":9007199254740993}`,
			hash: "f723911a0a9863cfe718fed9bd3491fee36f9233db34b6cd4347e9d66a348504",
			sort: "{n:9007199254740992}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			got, err := hashPluginConfigContext(context.Background(), raw)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			if got != tc.hash {
				t.Fatalf("hash = %q, want %q", got, tc.hash)
			}
			sorted, err := stringifyJSONValue(raw)
			if err != nil {
				t.Fatalf("stringify: %v", err)
			}
			if sorted != tc.sort {
				t.Fatalf("sort string = %q, want %q", sorted, tc.sort)
			}
		})
	}
}

func TestHashPluginConfigRejectsExcessiveDepthAndTrailingJSON(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat(`{"x":`, maxPluginHashDepth+1) + `0` + strings.Repeat(`}`, maxPluginHashDepth+1)
	if _, err := hashPluginConfigContext(context.Background(), json.RawMessage(deep)); err == nil {
		t.Fatal("excessively deep config was hashed")
	}
	if _, err := hashPluginConfigContext(context.Background(), json.RawMessage(`{} {}`)); err == nil {
		t.Fatal("trailing JSON value was hashed")
	}
}

func TestHashPluginConfigHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := hashPluginConfigContext(ctx, json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("canceled hash completed")
	}
}

func TestHashPluginConfigEnforcesTokenBudget(t *testing.T) {
	t.Parallel()

	raw := `[` + strings.Repeat(`0,`, maxPluginHashTokens) + `0]`
	if _, err := hashPluginConfigContext(context.Background(), json.RawMessage(raw)); err == nil {
		t.Fatal("config beyond the token budget was hashed")
	}
}

func TestBuildPluginPlanRejectsUnknownDeepAndOversizedConfig(t *testing.T) {
	t.Parallel()

	deep := strings.Repeat(`{"unknown":`, maxPluginHashDepth+1) + `0` + strings.Repeat(`}`, maxPluginHashDepth+1)
	request := &SyncPlugin{Config: json.RawMessage(deep)}
	if _, err := buildPluginPlan(request, nil, true); err == nil {
		t.Fatal("unknown deeply nested config was accepted")
	}

	request.Config = json.RawMessage(strings.Repeat(" ", maxPluginConfigBytes+1))
	if _, err := buildPluginPlan(request, nil, true); err == nil {
		t.Fatal("oversized config was accepted")
	}
}
