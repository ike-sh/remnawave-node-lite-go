package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"strings"
	"testing"
)

func TestEncodePreparedRuntimeConfigMatchesStandardJSON(t *testing.T) {
	t.Parallel()

	config := map[string]any{
		"html":    "<tag>&value>",
		"control": "quote=\" newline=\n separator=\u2028 invalid=" + string([]byte{0xff}),
		"numbers": []any{float64(1e-9), float64(1e20), int64(-42), uint64(42)},
		"null":    nil,
	}

	got, err := encodePreparedRuntimeConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	var expected bytes.Buffer
	encoder := json.NewEncoder(&expected)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		t.Fatal(err)
	}
	want := bytes.TrimSuffix(expected.Bytes(), []byte{'\n'})
	if !bytes.Equal(got, want) {
		t.Fatalf("prepared JSON differs from encoding/json\n got: %q\nwant: %q", got, want)
	}
	if bytes.HasSuffix(got, []byte{'\n'}) {
		t.Fatalf("prepared JSON has a trailing newline: %q", got)
	}
	if !bytes.Contains(got, []byte(`"<tag>&value>"`)) {
		t.Fatalf("HTML characters were escaped: %q", got)
	}
}

func TestPreparedJSONSizerMatchesStandardEncoderBoundaries(t *testing.T) {
	t.Parallel()

	controls := make([]byte, 0, 32)
	for value := byte(0); value < 0x20; value++ {
		controls = append(controls, value)
	}

	tests := []struct {
		name  string
		value any
	}{
		{name: "float32 zero", value: float32(0)},
		{name: "float32 negative zero", value: math.Float32frombits(1 << 31)},
		{name: "float32 below small exponent boundary", value: math.Nextafter32(1e-6, 0)},
		{name: "float32 at small exponent boundary", value: float32(1e-6)},
		{name: "float32 above small exponent boundary", value: math.Nextafter32(1e-6, 1)},
		{name: "float32 below large exponent boundary", value: math.Nextafter32(1e21, 0)},
		{name: "float32 at large exponent boundary", value: float32(1e21)},
		{name: "float32 above large exponent boundary", value: math.Nextafter32(1e21, float32(math.Inf(1)))},
		{name: "float32 smallest", value: float32(math.SmallestNonzeroFloat32)},
		{name: "float32 largest", value: float32(math.MaxFloat32)},
		{name: "float64 zero", value: float64(0)},
		{name: "float64 negative zero", value: math.Copysign(0, -1)},
		{name: "float64 below small exponent boundary", value: math.Nextafter(1e-6, 0)},
		{name: "float64 at small exponent boundary", value: float64(1e-6)},
		{name: "float64 above small exponent boundary", value: math.Nextafter(1e-6, 1)},
		{name: "float64 below large exponent boundary", value: math.Nextafter(1e21, 0)},
		{name: "float64 at large exponent boundary", value: float64(1e21)},
		{name: "float64 above large exponent boundary", value: math.Nextafter(1e21, math.Inf(1))},
		{name: "float64 padded negative exponent", value: float64(-1e-9)},
		{name: "float64 positive exponent", value: float64(1e30)},
		{name: "float64 smallest", value: float64(math.SmallestNonzeroFloat64)},
		{name: "float64 largest", value: float64(math.MaxFloat64)},
		{name: "all ASCII controls", value: string(controls)},
		{name: "HTML characters", value: "<>&"},
		{name: "line separators", value: "before\u2028middle\u2029after"},
		{name: "invalid UTF-8", value: string([]byte{0xff, 'a', 0xc0, 0x80})},
		{name: "nil map", value: map[string]any(nil)},
		{name: "nil slice", value: []any(nil)},
		{
			name: "integer types",
			value: []any{
				int(-1), int8(-2), int16(-3), int32(-4), int64(-5),
				uint(1), uint8(2), uint16(3), uint32(4), uint64(5), uintptr(6),
			},
		},
		{name: "escaped map key", value: map[string]any{"<>&\u2028" + string([]byte{0xff}): "value"}},
		{name: "nested", value: []any{true, false, nil, map[string]any{"key": "value"}}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertPreparedJSONSizeMatchesStandard(t, map[string]any{"value": test.value})
		})
	}
}

func TestPreparedJSONSizerMatchesStandardEncoderRandomized(t *testing.T) {
	t.Parallel()

	random := rand.New(rand.NewSource(0x5eed))
	for iteration := 0; iteration < 1_000; iteration++ {
		raw := make([]byte, random.Intn(257))
		if _, err := random.Read(raw); err != nil {
			t.Fatal(err)
		}
		float64Value := math.Float64frombits(random.Uint64())
		if math.IsNaN(float64Value) || math.IsInf(float64Value, 0) {
			float64Value = 0
		}
		float32Value := math.Float32frombits(random.Uint32())
		if math.IsNaN(float64(float32Value)) || math.IsInf(float64(float32Value), 0) {
			float32Value = 0
		}

		config := map[string]any{
			"bytes":   string(raw),
			"float32": float32Value,
			"float64": float64Value,
			"nested": []any{
				int64(random.Int63()),
				uint64(random.Uint64()),
				map[string]any{"enabled": iteration%2 == 0},
			},
		}
		assertPreparedJSONSizeMatchesStandard(t, config)
	}
}

func assertPreparedJSONSizeMatchesStandard(t *testing.T, config map[string]any) {
	t.Helper()

	sizer := preparedJSONSizer{remaining: maxPreparedRuntimeConfigBytes}
	if err := sizer.addValue(config, 0); err != nil {
		t.Fatalf("size prepared JSON: %v", err)
	}
	var standard bytes.Buffer
	encoder := json.NewEncoder(&standard)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		t.Fatalf("encode standard JSON: %v", err)
	}
	if got, want := sizer.size+1, standard.Len(); got != want {
		t.Fatalf("sized JSON length = %d, standard Encoder length = %d; JSON=%q", got, want, standard.Bytes())
	}
}

func TestEncodePreparedRuntimeConfigSizeBoundary(t *testing.T) {
	const objectOverhead = len(`{"x":""}`)
	payload := strings.Repeat("x", maxPreparedRuntimeConfigBytes-objectOverhead+1)

	if _, err := encodePreparedRuntimeConfig(map[string]any{"x": payload}); !errors.Is(err, errPreparedRuntimeConfigTooLarge) {
		t.Fatalf("20 MiB + 1 output error = %v, want prepared-config size error", err)
	}

	exact, err := encodePreparedRuntimeConfig(map[string]any{"x": payload[:len(payload)-1]})
	if err != nil {
		t.Fatalf("encode exact 20 MiB output: %v", err)
	}
	if len(exact) != maxPreparedRuntimeConfigBytes {
		t.Fatalf("encoded length = %d, want %d", len(exact), maxPreparedRuntimeConfigBytes)
	}
	if bytes.HasSuffix(exact, []byte{'\n'}) {
		t.Fatal("exact 20 MiB output has an Encoder newline")
	}
}

func TestPreparedJSONSizerRejectsEscapedExpansionBeforeEncoding(t *testing.T) {
	const objectOverhead = len(`{"x":""}`)
	controlBytes := (maxPreparedRuntimeConfigBytes-objectOverhead)/len(`\u0000`) + 1
	config := map[string]any{"x": strings.Repeat("\x00", controlBytes)}

	sizer := preparedJSONSizer{remaining: maxPreparedRuntimeConfigBytes}
	if err := sizer.addValue(config, 0); !errors.Is(err, errPreparedRuntimeConfigTooLarge) {
		t.Fatalf("escaped expansion size error = %v, want prepared-config size error", err)
	}
	if rawBytes := len(config["x"].(string)); rawBytes >= maxPreparedRuntimeConfigBytes {
		t.Fatalf("test input is not a compact expansion case: %d raw bytes", rawBytes)
	}
	if _, err := encodePreparedRuntimeConfig(config); !errors.Is(err, errPreparedRuntimeConfigTooLarge) {
		t.Fatalf("escaped expansion encode error = %v, want prepared-config size error", err)
	}
}

func TestPrepareRuntimeConfigRejectsUnsupportedJSONValue(t *testing.T) {
	t.Parallel()

	prepared, err := prepareRuntimeConfig(
		map[string]any{"unsupported": make(chan struct{})},
		ConfigHash{},
		"remnanode-xtls-test",
		TorrentBlockerOptions{},
	)
	if err == nil {
		t.Fatal("prepareRuntimeConfig accepted an unsupported JSON value")
	}
	if len(prepared.json) != 0 {
		t.Fatalf("prepared JSON length = %d, want 0", len(prepared.json))
	}
	if !errors.Is(err, errUnsupportedPreparedRuntimeJSON) {
		t.Fatalf("error = %q, want unsupported-value error", err)
	}
	if !strings.Contains(err.Error(), "chan struct {}") {
		t.Fatalf("error = %q, want unsupported-value detail", err)
	}
}

func TestPreparedJSONLimitWriterRejectsBeforeWriting(t *testing.T) {
	t.Parallel()

	var destination bytes.Buffer
	writer := preparedJSONLimitWriter{destination: &destination, remaining: 3}
	n, err := writer.Write([]byte("four"))
	if n != 0 {
		t.Fatalf("written bytes = %d, want 0", n)
	}
	if !errors.Is(err, errPreparedRuntimeConfigTooLarge) {
		t.Fatalf("error = %v, want prepared-config size error", err)
	}
	if destination.Len() != 0 {
		t.Fatalf("destination length = %d, want 0", destination.Len())
	}
}

func TestPrepareRuntimeConfigRejectsOversizedJSON(t *testing.T) {
	prepared, err := prepareRuntimeConfig(
		map[string]any{"oversized": strings.Repeat("x", maxPreparedRuntimeConfigBytes)},
		ConfigHash{},
		"remnanode-xtls-test",
		TorrentBlockerOptions{},
	)
	if err == nil {
		t.Fatal("prepareRuntimeConfig accepted JSON larger than 20 MiB")
	}
	if len(prepared.json) != 0 {
		t.Fatalf("prepared JSON length = %d, want 0", len(prepared.json))
	}
	if !errors.Is(err, errPreparedRuntimeConfigTooLarge) {
		t.Fatalf("error = %q, want prepared-config size error", err)
	}
}

func TestGenerateAPIConfigDedupesAPIRoutingRule(t *testing.T) {
	t.Parallel()

	config := generateAPIConfig(map[string]any{
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"inboundTag": []any{apiInboundTag}, "outboundTag": apiTag},
				map[string]any{"outboundTag": "direct"},
			},
		},
	}, "remnanode-xtls-test", TorrentBlockerOptions{})

	routing := config["routing"].(map[string]any)
	apiRules := 0
	for _, item := range arrayFrom(routing["rules"]) {
		rule, _ := item.(map[string]any)
		if tag, _ := rule["outboundTag"].(string); tag == apiTag {
			apiRules++
		}
	}
	if apiRules != 1 {
		t.Fatalf("expected exactly 1 %s routing rule after dedupe, got %d", apiTag, apiRules)
	}
}

func TestGenerateAPIConfigTorrentBlocker(t *testing.T) {
	t.Parallel()

	cfg := generateAPIConfig(map[string]any{
		"inbounds":  []any{map[string]any{"tag": "in-1"}},
		"outbounds": []any{},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"ruleTag": "custom", "domain": []any{"example.com"}},
			},
		},
	}, "remnanode-xtls-test", TorrentBlockerOptions{
		Enabled:         true,
		IncludeRuleTags: []string{"custom"},
		SocketPath:      "/run/test.sock",
		RESTToken:       "token",
	})

	outbounds := arrayFrom(cfg["outbounds"])
	foundOutbound := false
	for _, item := range outbounds {
		outbound, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if outbound["tag"] == torrentBlockerOutboundTag {
			foundOutbound = true
		}
	}
	if !foundOutbound {
		t.Fatal("expected torrent blocker outbound")
	}

	routing, ok := cfg["routing"].(map[string]any)
	if !ok {
		t.Fatal("missing routing")
	}
	rules := arrayFrom(routing["rules"])
	if len(rules) < 2 {
		t.Fatalf("expected at least 2 rules, got %d", len(rules))
	}
}
