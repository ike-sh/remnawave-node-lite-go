package config

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/secret"
)

const (
	DefaultEnvPath            = "/etc/remnanode/node.env"
	defaultXrayBin            = "/usr/local/lib/remnanode/rw-core"
	defaultGeoDir             = "/usr/local/share/remnanode/xray"
	defaultLogDir             = "/var/log/remnanode"
	defaultInternalSocketPath = "/run/remnanode/internal.sock"
	defaultASNDBPath          = "/usr/local/share/remnanode/asn/asn-prefixes.bin"
	maxDotEnvBytes            = 1 << 20
	maxDotEnvLines            = 4096
	maxDotEnvAssignments      = 256
)

// ResolveEnvPath returns the first existing env file path, preferring production default.
func ResolveEnvPath() string {
	for _, path := range []string{DefaultEnvPath, ".env"} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ".env"
}

type Config struct {
	NodePort              int
	BindAddr              string
	SecretKey             string
	XrayBin               string
	GeoDir                string
	LogDir                string
	InternalSocketPath    string
	InternalRESTToken     string
	ASNDBPath             string
	DisableHashedSetCheck bool
	LowMemory             bool
	BodyLimitMB           int
	GoMemoryLimitBytes    int64
	GoMemoryLimitSet      bool
	NodeContractVersion   string
	XrayCoreVersion       string
}

var (
	nodeContractVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$`)
	xrayCoreVersionPattern     = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$`)
)

func Load(dotenvPath string) (Config, error) {
	values := map[string]string{}
	if dotenvPath != "" {
		fileValues, err := parseDotEnv(dotenvPath)
		if err != nil {
			return Config{}, err
		}
		for key, value := range fileValues {
			values[key] = value
		}
	}

	for _, key := range []string{
		"NODE_PORT",
		"NODE_BIND_ADDR",
		"SECRET_KEY",
		"SECRET_KEY_FILE",
		"XRAY_BIN",
		"GEO_DIR",
		"LOG_DIR",
		"INTERNAL_SOCKET_PATH",
		"INTERNAL_REST_TOKEN",
		"ASN_DB_PATH",
		"DISABLE_HASHED_SET_CHECK",
		"LOW_MEMORY",
		"BODY_LIMIT_MB",
		"GOMEMLIMIT",
		"NODE_CONTRACT_VERSION",
		"XRAY_CORE_VERSION",
	} {
		if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
			values[key] = value
		}
	}

	nodePort, err := requiredInt(values, "NODE_PORT")
	if err != nil {
		return Config{}, err
	}

	secretKey := strings.TrimSpace(values["SECRET_KEY"])
	if secretKey == "" {
		secretKey, err = loadSecretFromFile(values)
		if err != nil {
			return Config{}, err
		}
	}
	if secretKey == "" {
		return Config{}, errors.New("SECRET_KEY or SECRET_KEY_FILE is required")
	}

	internalSocketPath := optionalString(values, "INTERNAL_SOCKET_PATH", defaultInternalSocketPath)

	internalRESTToken := optionalString(values, "INTERNAL_REST_TOKEN", "")
	if internalRESTToken == "" {
		internalRESTToken, err = randomToken(48)
		if err != nil {
			return Config{}, err
		}
	}
	disableHashedSetCheck, err := optionalBool(values, "DISABLE_HASHED_SET_CHECK", false)
	if err != nil {
		return Config{}, err
	}
	lowMemory, err := optionalBool(values, "LOW_MEMORY", false)
	if err != nil {
		return Config{}, err
	}
	bodyLimitMB, err := optionalIntDefault(values, "BODY_LIMIT_MB", 0)
	if err != nil {
		return Config{}, err
	}
	goMemoryLimitBytes, goMemoryLimitSet, err := optionalMemoryLimit(values, "GOMEMLIMIT")
	if err != nil {
		return Config{}, err
	}
	nodeContractVersion, err := optionalRuntimeVersion(values, "NODE_CONTRACT_VERSION", nodeContractVersionPattern)
	if err != nil {
		return Config{}, err
	}
	xrayCoreVersion, err := optionalRuntimeVersion(values, "XRAY_CORE_VERSION", xrayCoreVersionPattern)
	if err != nil {
		return Config{}, err
	}

	return Config{
		NodePort:              nodePort,
		BindAddr:              strings.TrimSpace(values["NODE_BIND_ADDR"]),
		SecretKey:             secretKey,
		XrayBin:               optionalString(values, "XRAY_BIN", defaultXrayBin),
		GeoDir:                optionalString(values, "GEO_DIR", defaultGeoDir),
		LogDir:                optionalString(values, "LOG_DIR", defaultLogDir),
		InternalSocketPath:    internalSocketPath,
		InternalRESTToken:     internalRESTToken,
		ASNDBPath:             optionalString(values, "ASN_DB_PATH", defaultASNDBPath),
		DisableHashedSetCheck: disableHashedSetCheck,
		LowMemory:             lowMemory,
		BodyLimitMB:           bodyLimitMB,
		GoMemoryLimitBytes:    goMemoryLimitBytes,
		GoMemoryLimitSet:      goMemoryLimitSet,
		NodeContractVersion:   nodeContractVersion,
		XrayCoreVersion:       xrayCoreVersion,
	}, nil
}

func (c Config) HTTPAddr() string {
	host := c.BindAddr
	if len(host) >= 2 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	return net.JoinHostPort(host, strconv.Itoa(c.NodePort))
}

func parseDotEnv(path string) (map[string]string, error) {
	raw, err := readStableRegularFile(path, maxDotEnvBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	values := make(map[string]string, 32)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), maxDotEnvBytes)
	lineNo := 0
	assignments := 0
	for scanner.Scan() {
		lineNo++
		if lineNo > maxDotEnvLines {
			return nil, fmt.Errorf("%s contains more than %d lines", path, maxDotEnvLines)
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("%s:%d invalid .env line", path, lineNo)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("%s:%d empty .env key", path, lineNo)
		}
		assignments++
		if assignments > maxDotEnvAssignments {
			return nil, fmt.Errorf("%s contains more than %d assignments", path, maxDotEnvAssignments)
		}
		values[key] = unquote(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return values, nil
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
		value = value[1 : len(value)-1]
	}
	return strings.ReplaceAll(value, `\n`, "\n")
}

func requiredInt(values map[string]string, key string) (int, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func optionalString(values map[string]string, key string, fallback string) string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return value
	}
	return fallback
}

func optionalBool(values map[string]string, key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean (true/false, 1/0, or yes/no)", key)
	}
}

func optionalIntDefault(values map[string]string, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return value, nil
}

func optionalMemoryLimit(values map[string]string, key string) (int64, bool, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return 0, false, nil
	}
	if raw == "off" {
		return int64(^uint64(0) >> 1), true, nil
	}

	multiplier := int64(1)
	number := raw
	for _, unit := range []struct {
		suffix     string
		multiplier int64
	}{
		{suffix: "TiB", multiplier: 1 << 40},
		{suffix: "GiB", multiplier: 1 << 30},
		{suffix: "MiB", multiplier: 1 << 20},
		{suffix: "KiB", multiplier: 1 << 10},
		{suffix: "B", multiplier: 1},
	} {
		if strings.HasSuffix(raw, unit.suffix) {
			number = strings.TrimSuffix(raw, unit.suffix)
			multiplier = unit.multiplier
			break
		}
	}
	if len(number) < 1 || len(number) > 19 {
		return 0, false, fmt.Errorf("%s must be off or a non-negative byte count with an optional B/KiB/MiB/GiB/TiB suffix", key)
	}
	for _, char := range number {
		if char < '0' || char > '9' {
			return 0, false, fmt.Errorf("%s must be off or a non-negative byte count with an optional B/KiB/MiB/GiB/TiB suffix", key)
		}
	}
	value, err := strconv.ParseInt(number, 10, 64)
	if err != nil || value > int64(^uint64(0)>>1)/multiplier {
		return 0, false, fmt.Errorf("%s is outside the supported byte range", key)
	}
	return value * multiplier, true, nil
}

func optionalRuntimeVersion(values map[string]string, key string, pattern *regexp.Regexp) (string, error) {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return "", nil
	}
	if len(raw) > 64 || !pattern.MatchString(raw) {
		return "", fmt.Errorf("%s has an invalid version value", key)
	}
	return raw, nil
}

func loadSecretFromFile(values map[string]string) (string, error) {
	path := strings.TrimSpace(values["SECRET_KEY_FILE"])
	if path == "" {
		return "", nil
	}

	canonical, err := ReadSecretFile(path)
	if err != nil {
		return "", fmt.Errorf("read SECRET_KEY_FILE %s: %w", path, err)
	}
	return canonical, nil
}

// ReadSecretFile safely reads and canonicalizes a bounded Secret Key file.
func ReadSecretFile(path string) (string, error) {
	maxFileBytes := int64(secret.MaxEncodedBytes + 2)
	raw, err := readStableRegularFile(path, maxFileBytes)
	if err != nil {
		return "", err
	}
	return canonicalSecretFile(raw)
}

// CanonicalizeSecretFileContent accepts one optional LF or CRLF suffix.
func CanonicalizeSecretFileContent(raw []byte) (string, error) {
	return canonicalSecretFile(raw)
}

func readStableRegularFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("file size limit must be positive")
	}
	file, err := openReadOnlyNoFollow(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened file: %w", err)
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("file must be a regular non-symlink file")
	}
	if opened.Size() > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}

	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", maxBytes)
	}
	final, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("reinspect file: %w", err)
	}
	if !final.Mode().IsRegular() || final.Size() > maxBytes ||
		opened.Size() != final.Size() || final.Size() != int64(len(raw)) ||
		!opened.ModTime().Equal(final.ModTime()) || !os.SameFile(opened, final) {
		return nil, errors.New("file changed while reading")
	}
	return raw, nil
}

func canonicalSecretFile(raw []byte) (string, error) {
	if len(raw) > secret.MaxEncodedBytes+2 {
		return "", fmt.Errorf("content exceeds %d bytes plus optional CRLF", secret.MaxEncodedBytes)
	}
	if len(raw) >= 2 && raw[len(raw)-2] == '\r' && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-2]
	} else if len(raw) >= 1 && raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	} else if len(raw) >= 1 && raw[len(raw)-1] == '\r' {
		return "", errors.New("content must have no newline or one LF/CRLF suffix")
	}
	if len(raw) == 0 || len(raw) > secret.MaxEncodedBytes {
		return "", fmt.Errorf("canonical content must contain 1..%d bytes", secret.MaxEncodedBytes)
	}
	for _, char := range raw {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') ||
			(char >= '0' && char <= '9') || char == '+' || char == '/' ||
			char == '-' || char == '_' || char == '=' {
			continue
		}
		return "", errors.New("content contains non-base64 bytes or internal whitespace")
	}
	return string(raw), nil
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
