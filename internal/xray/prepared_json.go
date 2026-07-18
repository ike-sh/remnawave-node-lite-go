package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

const (
	maxPreparedRuntimeConfigBytes = 20 << 20
	maxPreparedJSONDepth          = 128
)

var (
	errPreparedRuntimeConfigTooLarge  = errors.New("prepared Xray config JSON exceeds 20 MiB (20971520 bytes)")
	errUnsupportedPreparedRuntimeJSON = errors.New("unsupported prepared Xray config JSON value")
)

// encodePreparedRuntimeConfig sizes the generic JSON tree before invoking the
// standard encoder. This keeps both the encoder's internal buffer and the
// retained output bounded while preserving encoding/json's canonical map-key
// ordering and number formatting.
func encodePreparedRuntimeConfig(config map[string]any) ([]byte, error) {
	sizer := preparedJSONSizer{remaining: maxPreparedRuntimeConfigBytes}
	if err := sizer.addValue(config, 0); err != nil {
		return nil, err
	}

	// Encoder appends one newline. Preallocating the exact amount prevents the
	// destination buffer from growing beyond the verified output size.
	var destination bytes.Buffer
	destination.Grow(sizer.size + 1)
	limited := preparedJSONLimitWriter{
		destination: &destination,
		remaining:   maxPreparedRuntimeConfigBytes + 1,
	}
	encoder := json.NewEncoder(&limited)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(config); err != nil {
		return nil, err
	}

	encoded := destination.Bytes()
	if len(encoded) != sizer.size+1 || encoded[len(encoded)-1] != '\n' {
		return nil, fmt.Errorf(
			"prepared Xray config JSON framing changed during encoding: expected %d bytes including newline, got %d",
			sizer.size+1,
			len(encoded),
		)
	}
	return encoded[:len(encoded)-1], nil
}

type preparedJSONLimitWriter struct {
	destination io.Writer
	remaining   int
}

func (w *preparedJSONLimitWriter) Write(value []byte) (int, error) {
	if len(value) > w.remaining {
		return 0, errPreparedRuntimeConfigTooLarge
	}
	n, err := w.destination.Write(value)
	w.remaining -= n
	if err == nil && n != len(value) {
		err = io.ErrShortWrite
	}
	return n, err
}

type preparedJSONSizer struct {
	size      int
	remaining int
}

func (s *preparedJSONSizer) add(size int) error {
	if size < 0 || size > s.remaining {
		return errPreparedRuntimeConfigTooLarge
	}
	s.size += size
	s.remaining -= size
	return nil
}

func (s *preparedJSONSizer) addValue(value any, depth int) error {
	if depth > maxPreparedJSONDepth {
		return fmt.Errorf("prepared Xray config JSON exceeds %d nesting levels", maxPreparedJSONDepth)
	}

	switch typed := value.(type) {
	case nil:
		return s.add(len("null"))
	case bool:
		if typed {
			return s.add(len("true"))
		}
		return s.add(len("false"))
	case string:
		return s.addString(typed)
	case float32:
		return s.addFloat(float64(typed), 32)
	case float64:
		return s.addFloat(typed, 64)
	case int:
		return s.addInt(int64(typed))
	case int8:
		return s.addInt(int64(typed))
	case int16:
		return s.addInt(int64(typed))
	case int32:
		return s.addInt(int64(typed))
	case int64:
		return s.addInt(typed)
	case uint:
		return s.addUint(uint64(typed))
	case uint8:
		return s.addUint(uint64(typed))
	case uint16:
		return s.addUint(uint64(typed))
	case uint32:
		return s.addUint(uint64(typed))
	case uint64:
		return s.addUint(typed)
	case uintptr:
		return s.addUint(uint64(typed))
	case []any:
		if typed == nil {
			return s.add(len("null"))
		}
		if err := s.add(1); err != nil { // [
			return err
		}
		for index, item := range typed {
			if index != 0 {
				if err := s.add(1); err != nil { // ,
					return err
				}
			}
			if err := s.addValue(item, depth+1); err != nil {
				return err
			}
		}
		return s.add(1) // ]
	case map[string]any:
		if typed == nil {
			return s.add(len("null"))
		}
		if err := s.add(1); err != nil { // {
			return err
		}
		index := 0
		for key, item := range typed {
			if index != 0 {
				if err := s.add(1); err != nil { // ,
					return err
				}
			}
			index++
			if err := s.addString(key); err != nil {
				return err
			}
			if err := s.add(1); err != nil { // :
				return err
			}
			if err := s.addValue(item, depth+1); err != nil {
				return err
			}
		}
		return s.add(1) // }
	default:
		return fmt.Errorf("%w: %T", errUnsupportedPreparedRuntimeJSON, value)
	}
}

func (s *preparedJSONSizer) addInt(value int64) error {
	var scratch [32]byte
	return s.add(len(strconv.AppendInt(scratch[:0], value, 10)))
}

func (s *preparedJSONSizer) addUint(value uint64) error {
	var scratch [32]byte
	return s.add(len(strconv.AppendUint(scratch[:0], value, 10)))
}

func (s *preparedJSONSizer) addFloat(value float64, bits int) error {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return fmt.Errorf(
			"%w: number %s",
			errUnsupportedPreparedRuntimeJSON,
			strconv.FormatFloat(value, 'g', -1, bits),
		)
	}

	format := byte('f')
	absolute := math.Abs(value)
	if absolute != 0 && (bits == 64 && (absolute < 1e-6 || absolute >= 1e21) ||
		bits == 32 && (float32(absolute) < 1e-6 || float32(absolute) >= 1e21)) {
		format = 'e'
	}
	var scratch [32]byte
	encoded := strconv.AppendFloat(scratch[:0], value, format, -1, bits)
	if format == 'e' {
		length := len(encoded)
		if length >= 4 && encoded[length-4] == 'e' && encoded[length-3] == '-' && encoded[length-2] == '0' {
			encoded[length-2] = encoded[length-1]
			encoded = encoded[:length-1]
		}
	}
	return s.add(len(encoded))
}

// addString mirrors encoding/json with SetEscapeHTML(false). In particular,
// invalid UTF-8 is replaced and U+2028/U+2029 remain escaped.
func (s *preparedJSONSizer) addString(value string) error {
	if err := s.add(1); err != nil { // opening quote
		return err
	}
	for index := 0; index < len(value); {
		char := value[index]
		if char < utf8.RuneSelf {
			size := 1
			switch char {
			case '\\', '"', '\b', '\f', '\n', '\r', '\t':
				size = 2
			default:
				if char < 0x20 {
					size = 6
				}
			}
			if err := s.add(size); err != nil {
				return err
			}
			index++
			continue
		}

		runeValue, size := utf8.DecodeRuneInString(value[index:])
		encodedSize := size
		if (runeValue == utf8.RuneError && size == 1) || runeValue == '\u2028' || runeValue == '\u2029' {
			encodedSize = 6
		}
		if err := s.add(encodedSize); err != nil {
			return err
		}
		index += size
	}
	return s.add(1) // closing quote
}
