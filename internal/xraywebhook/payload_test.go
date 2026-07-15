package xraywebhook

import (
	"encoding/json"
	"strings"
	"testing"
)

const validPayload = `{
	"email":"user-1",
	"level":0,
	"protocol":"vless",
	"network":"tcp",
	"source":"tcp:203.0.113.10:443",
	"destination":"198.51.100.1:443",
	"routeTarget":null,
	"originalTarget":null,
	"inboundTag":"in-1",
	"inboundName":null,
	"inboundLocal":null,
	"outboundTag":"direct",
	"ts":123,
	"unknown":"stripped"
}`

func TestDecodeValidPayloadAndStripUnknownFields(t *testing.T) {
	payload, err := Decode(strings.NewReader(validPayload))
	if err != nil {
		t.Fatal(err)
	}
	if payload.Email == nil || *payload.Email != "user-1" || payload.Network == nil || *payload.Network != "tcp" {
		t.Fatalf("payload = %#v", payload)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "unknown") {
		t.Fatalf("unknown field retained: %s", raw)
	}
}

func TestDecodeRejectsMissingRequiredAndTrailingDocuments(t *testing.T) {
	for _, raw := range []string{
		`{"email":"user-1"}`,
		validPayload + `{}`,
		strings.Replace(validPayload, `"network":"tcp"`, `"network":null`, 1),
	} {
		if _, err := Decode(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted invalid webhook: %s", raw)
		}
	}
}
