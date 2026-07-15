package xraywebhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Payload mirrors the official XrayWebhookSchema. Pointer fields preserve the
// distinction between nullable values and their concrete scalar types.
type Payload struct {
	Email          *string  `json:"email"`
	Level          *float64 `json:"level"`
	Protocol       *string  `json:"protocol"`
	Network        *string  `json:"network"`
	Source         *string  `json:"source"`
	Destination    *string  `json:"destination"`
	RouteTarget    *string  `json:"routeTarget"`
	OriginalTarget *string  `json:"originalTarget"`
	InboundTag     *string  `json:"inboundTag"`
	InboundName    *string  `json:"inboundName"`
	InboundLocal   *string  `json:"inboundLocal"`
	OutboundTag    *string  `json:"outboundTag"`
	Timestamp      *float64 `json:"ts"`
}

var requiredFields = [...]string{
	"email",
	"level",
	"protocol",
	"network",
	"source",
	"destination",
	"routeTarget",
	"originalTarget",
	"inboundTag",
	"inboundName",
	"inboundLocal",
	"outboundTag",
	"ts",
}

func (p *Payload) UnmarshalJSON(raw []byte) error {
	type payloadAlias Payload
	var decoded payloadAlias
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, field := range requiredFields {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("missing webhook field %q", field)
		}
	}
	if decoded.Network == nil || decoded.Destination == nil || decoded.Timestamp == nil {
		return errors.New("network, destination, and ts must not be null")
	}
	*p = Payload(decoded)
	return nil
}

// Decode accepts exactly one JSON document and strips unknown object fields,
// matching the official Zod object behavior.
func Decode(reader io.Reader) (Payload, error) {
	decoder := json.NewDecoder(reader)
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		return Payload{}, err
	}
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if err == nil {
		return Payload{}, errors.New("multiple webhook JSON documents")
	}
	if !errors.Is(err, io.EOF) {
		return Payload{}, err
	}
	return payload, nil
}

func String(value string) *string {
	return &value
}

func Number(value float64) *float64 {
	return &value
}
