package xray

import (
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkPrepareRuntimeConfig10000Users(b *testing.B) {
	payload := benchmarkStartPayload(b, 10_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for range b.N {
		var request StartRequest
		if err := json.Unmarshal(payload, &request); err != nil {
			b.Fatal(err)
		}
		prepared, err := prepareRuntimeConfig(request.XrayConfig, request.Internals.Hashes, "benchmark", TorrentBlockerOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if len(prepared.json) == 0 || len(prepared.hashState.inboundHashes) != 1 {
			b.Fatal("prepared config is incomplete")
		}
	}
}

func benchmarkStartPayload(tb testing.TB, users int) []byte {
	tb.Helper()
	clients := make([]any, 0, users)
	for index := range users {
		clients = append(clients, map[string]any{
			"email": fmt.Sprintf("user-%05d", index),
			"id":    fmt.Sprintf("00000000-0000-4000-8000-%012d", index),
		})
	}
	payload := StartRequest{
		Internals: StartInternals{Hashes: ConfigHash{
			EmptyConfig: "base-hash",
			Inbounds:    []InboundHash{{Tag: "in-1"}},
		}},
		XrayConfig: map[string]any{
			"inbounds": []any{map[string]any{
				"tag":      "in-1",
				"protocol": "vless",
				"settings": map[string]any{"clients": clients},
			}},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		tb.Fatal(err)
	}
	return raw
}
