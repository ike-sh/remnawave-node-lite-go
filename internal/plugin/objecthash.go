package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var whitespacePattern = regexp.MustCompile(`(\s+|\t|\r\n|\n|\r)`)

// hashPluginConfig matches @remnawave/node plugin.service.ts:
// hasher({ trim: true, sort: false }).hash(plugin.config)
func hashPluginConfig(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return ""
	}
	sorted, err := stringifyJSONValue(raw)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(sorted))
	return hex.EncodeToString(sum[:])
}

func stringifyJSONValue(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return stringifyToken(dec)
}

func stringifyToken(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}

	switch value := tok.(type) {
	case json.Delim:
		switch value {
		case '{':
			return stringifyObject(dec)
		case '[':
			return stringifyArray(dec)
		default:
			return "", fmt.Errorf("unexpected delimiter %q", value)
		}
	case string:
		return trimString(value), nil
	case json.Number:
		return value.String(), nil
	case bool:
		if value {
			return "1", nil
		}
		return "0", nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported token type %T", tok)
	}
}

func stringifyObject(dec *json.Decoder) (string, error) {
	parts := make([]string, 0, 8)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", err
		}
		key, ok := keyTok.(string)
		if !ok {
			return "", fmt.Errorf("object key must be string, got %T", keyTok)
		}
		val, err := stringifyToken(dec)
		if err != nil {
			return "", err
		}
		parts = append(parts, key+":"+val)
	}
	if _, err := dec.Token(); err != nil {
		return "", err
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func stringifyArray(dec *json.Decoder) (string, error) {
	parts := make([]string, 0, 8)
	for dec.More() {
		val, err := stringifyToken(dec)
		if err != nil {
			return "", err
		}
		parts = append(parts, val)
	}
	if _, err := dec.Token(); err != nil {
		return "", err
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

func trimString(value string) string {
	return strings.TrimSpace(whitespacePattern.ReplaceAllString(value, " "))
}
