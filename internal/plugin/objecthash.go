package plugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var whitespacePattern = regexp.MustCompile(`(\s+|\t|\r\n|\n|\r)`)

// hashPluginConfigContext streams the object-hash representation directly
// into SHA-256. The previous recursive string concatenation had quadratic
// behavior for deeply nested objects and retained a second full config copy.
// Its output matches @remnawave/node's hasher({trim:true, sort:false}).
func hashPluginConfigContext(ctx context.Context, raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	hasher := sha256.New()
	if err := stringifyJSONToWriter(ctx, raw, hasher); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func stringifyJSONValue(raw json.RawMessage) (string, error) {
	var output strings.Builder
	if err := stringifyJSONToWriter(context.Background(), raw, &output); err != nil {
		return "", err
	}
	return output.String(), nil
}

func stringifyJSONToWriter(ctx context.Context, raw json.RawMessage, destination io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	state := hashTraversal{ctx: ctx}
	writer := &boundedHashWriter{destination: destination, remaining: maxPluginHashOutputBytes}
	if err := state.stringifyToken(dec, writer, 0); err != nil {
		return fmt.Errorf("hash plugin config: %w", err)
	}
	if _, err := state.nextToken(dec); err != io.EOF {
		if err == nil {
			return fmt.Errorf("hash plugin config: trailing JSON value")
		}
		return fmt.Errorf("hash plugin config: %w", err)
	}
	return nil
}

type hashTraversal struct {
	ctx    context.Context
	tokens int
}

func (s *hashTraversal) nextToken(dec *json.Decoder) (json.Token, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	s.tokens++
	if s.tokens > maxPluginHashTokens {
		return nil, fmt.Errorf("JSON token budget exceeded (%d)", maxPluginHashTokens)
	}
	return token, nil
}

func (s *hashTraversal) stringifyToken(dec *json.Decoder, writer io.Writer, depth int) error {
	token, err := s.nextToken(dec)
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		if depth >= maxPluginHashDepth {
			return fmt.Errorf("JSON nesting exceeds %d levels", maxPluginHashDepth)
		}
		switch value {
		case '{':
			return s.stringifyObject(dec, writer, depth+1)
		case '[':
			return s.stringifyArray(dec, writer, depth+1)
		default:
			return fmt.Errorf("unexpected delimiter %q", value)
		}
	case string:
		_, err = io.WriteString(writer, trimString(value))
		return err
	case json.Number:
		formatted, formatErr := formatJavaScriptNumber(value)
		if formatErr != nil {
			return formatErr
		}
		_, err = io.WriteString(writer, formatted)
		return err
	case bool:
		if value {
			_, err = io.WriteString(writer, "1")
		} else {
			_, err = io.WriteString(writer, "0")
		}
		return err
	case nil:
		return nil
	default:
		return fmt.Errorf("unsupported token type %T", token)
	}
}

// JSON.parse stores every number as an IEEE-754 double, and node-object-hash
// serializes it with JavaScript Number.prototype.toString(). Go's json.Number
// retains the original lexeme, so normalize both the value and JS's decimal /
// exponent formatting thresholds before hashing.
func formatJavaScriptNumber(value json.Number) (string, error) {
	number, err := strconv.ParseFloat(value.String(), 64)
	if err != nil && !math.IsInf(number, 0) {
		return "", fmt.Errorf("parse JSON number %q: %w", value.String(), err)
	}
	switch {
	case math.IsInf(number, 1):
		return "Infinity", nil
	case math.IsInf(number, -1):
		return "-Infinity", nil
	case number == 0:
		return "0", nil
	}

	absolute := math.Abs(number)
	if absolute >= 1e-6 && absolute < 1e21 {
		return strconv.FormatFloat(number, 'f', -1, 64), nil
	}

	scientific := strconv.FormatFloat(number, 'e', -1, 64)
	mantissa, exponentText, ok := strings.Cut(scientific, "e")
	if !ok {
		return "", fmt.Errorf("format JSON number %q: missing exponent", value.String())
	}
	exponent, err := strconv.Atoi(exponentText)
	if err != nil {
		return "", fmt.Errorf("format JSON number %q: %w", value.String(), err)
	}
	sign := ""
	if exponent >= 0 {
		sign = "+"
	}
	return mantissa + "e" + sign + strconv.Itoa(exponent), nil
}

func (s *hashTraversal) stringifyObject(dec *json.Decoder, writer io.Writer, depth int) error {
	if _, err := io.WriteString(writer, "{"); err != nil {
		return err
	}
	first := true
	for dec.More() {
		keyToken, err := s.nextToken(dec)
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("object key must be string, got %T", keyToken)
		}
		if !first {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		first = false
		if _, err := io.WriteString(writer, key); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, ":"); err != nil {
			return err
		}
		if err := s.stringifyToken(dec, writer, depth); err != nil {
			return err
		}
	}
	closing, err := s.nextToken(dec)
	if err != nil {
		return err
	}
	if closing != json.Delim('}') {
		return fmt.Errorf("unexpected object terminator %v", closing)
	}
	_, err = io.WriteString(writer, "}")
	return err
}

func (s *hashTraversal) stringifyArray(dec *json.Decoder, writer io.Writer, depth int) error {
	if _, err := io.WriteString(writer, "["); err != nil {
		return err
	}
	first := true
	for dec.More() {
		if !first {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		first = false
		if err := s.stringifyToken(dec, writer, depth); err != nil {
			return err
		}
	}
	closing, err := s.nextToken(dec)
	if err != nil {
		return err
	}
	if closing != json.Delim(']') {
		return fmt.Errorf("unexpected array terminator %v", closing)
	}
	_, err = io.WriteString(writer, "]")
	return err
}

type boundedHashWriter struct {
	destination io.Writer
	remaining   int
}

func (w *boundedHashWriter) Write(value []byte) (int, error) {
	if len(value) > w.remaining {
		return 0, fmt.Errorf("object-hash representation exceeds %d bytes", maxPluginHashOutputBytes)
	}
	n, err := w.destination.Write(value)
	w.remaining -= n
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	return n, err
}

func trimString(value string) string {
	return strings.TrimSpace(whitespacePattern.ReplaceAllString(value, " "))
}
