package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultEnvPath     = "/etc/remnanode/node.env"
	defaultXtlsAPIPort = 61000
	defaultXrayBin     = "/usr/local/bin/rw-core"
	defaultGeoDir      = "/usr/local/share/xray"
	defaultLogDir             = "/var/log/remnanode"
	defaultDataDir              = "/var/lib/remnanode"
	defaultInternalSocketPath = "/run/remnanode/internal.sock"
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
	NodePort               int
	BindAddr               string
	SecretKey              string
	XtlsAPIPort            int
	XrayBin                string
	GeoDir                 string
	LogDir                 string
	DataDir                string
	InternalSocketPath     string
	InternalRESTToken      string
	DisableHashedSetCheck  bool
	LowMemory              bool
	BodyLimitMB            int
}

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
		"XTLS_API_PORT",
		"XRAY_BIN",
		"GEO_DIR",
		"LOG_DIR",
		"DATA_DIR",
		"INTERNAL_SOCKET_PATH",
		"INTERNAL_REST_TOKEN",
		"DISABLE_HASHED_SET_CHECK",
		"LOW_MEMORY",
		"BODY_LIMIT_MB",
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

	xtlsAPIPort, err := optionalInt(values, "XTLS_API_PORT", defaultXtlsAPIPort)
	if err != nil {
		return Config{}, err
	}

	internalSocketPath := optionalString(values, "INTERNAL_SOCKET_PATH", defaultInternalSocketPath)

	internalRESTToken := optionalString(values, "INTERNAL_REST_TOKEN", "")
	if internalRESTToken == "" {
		internalRESTToken, err = randomToken(48)
		if err != nil {
			return Config{}, err
		}
	}

	return Config{
		NodePort:              nodePort,
		BindAddr:              strings.TrimSpace(values["NODE_BIND_ADDR"]),
		SecretKey:             secretKey,
		XtlsAPIPort:           xtlsAPIPort,
		XrayBin:               optionalString(values, "XRAY_BIN", defaultXrayBin),
		GeoDir:                optionalString(values, "GEO_DIR", defaultGeoDir),
		LogDir:                optionalString(values, "LOG_DIR", defaultLogDir),
		DataDir:               optionalString(values, "DATA_DIR", defaultDataDir),
		InternalSocketPath:    internalSocketPath,
		InternalRESTToken:     internalRESTToken,
		DisableHashedSetCheck: optionalBool(values, "DISABLE_HASHED_SET_CHECK", false),
		LowMemory:             optionalBool(values, "LOW_MEMORY", false),
		BodyLimitMB:           optionalIntDefault(values, "BODY_LIMIT_MB", 0),
	}, nil
}

func (c Config) HTTPAddr() string {
	if c.BindAddr != "" {
		return c.BindAddr + ":" + strconv.Itoa(c.NodePort)
	}
	return ":" + strconv.Itoa(c.NodePort)
}

func parseDotEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
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

func optionalInt(values map[string]string, key string, fallback int) (int, error) {
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

func optionalString(values map[string]string, key string, fallback string) string {
	if value := strings.TrimSpace(values[key]); value != "" {
		return value
	}
	return fallback
}

func optionalBool(values map[string]string, key string, fallback bool) bool {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback
	}
	return raw == "true" || raw == "1" || raw == "yes"
}

func optionalIntDefault(values map[string]string, key string, fallback int) int {
	raw := strings.TrimSpace(values[key])
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func loadSecretFromFile(values map[string]string) (string, error) {
	path := strings.TrimSpace(values["SECRET_KEY_FILE"])
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read SECRET_KEY_FILE %s: %w", path, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
