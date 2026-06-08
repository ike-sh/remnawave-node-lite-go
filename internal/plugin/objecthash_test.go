package plugin

import (
	"encoding/json"
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			if got := hashPluginConfig(raw); got != tc.hash {
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
