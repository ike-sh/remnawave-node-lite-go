package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultXtlsAPIPort = 61000
	defaultXrayBin     = "/usr/local/bin/rw-core"
	defaultGeoDir      = "/usr/local/share/xray"
	defaultLogDir      = "./logs"
)

type Config struct {
	NodePort           int
	SecretKey          string
	XtlsAPIPort        int
	XrayBin            string
	GeoDir             string
	LogDir             string
	InternalSocketPath string
	InternalRESTToken  string
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
		"SECRET_KEY",
		"XTLS_API_PORT",
		"XRAY_BIN",
		"GEO_DIR",
		"LOG_DIR",
		"INTERNAL_SOCKET_PATH",
		"INTERNAL_REST_TOKEN",
	} {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}

	nodePort, err := requiredInt(values, "NODE_PORT")
	if err != nil {
		return Config{}, err
	}

	secretKey := strings.TrimSpace(values["SECRET_KEY"])
	if secretKey == "" {
		return Config{}, errors.New("SECRET_KEY is required")
	}

	xtlsAPIPort, err := optionalInt(values, "XTLS_API_PORT", defaultXtlsAPIPort)
	if err != nil {
		return Config{}, err
	}

	internalSocketPath := optionalString(values, "INTERNAL_SOCKET_PATH", "")
	if internalSocketPath == "" {
		internalSocketPath, err = randomSocketPath()
		if err != nil {
			return Config{}, err
		}
	}

	internalRESTToken := optionalString(values, "INTERNAL_REST_TOKEN", "")
	if internalRESTToken == "" {
		internalRESTToken, err = randomToken(48)
		if err != nil {
			return Config{}, err
		}
	}

	return Config{
		NodePort:           nodePort,
		SecretKey:          secretKey,
		XtlsAPIPort:        xtlsAPIPort,
		XrayBin:            optionalString(values, "XRAY_BIN", defaultXrayBin),
		GeoDir:             optionalString(values, "GEO_DIR", defaultGeoDir),
		LogDir:             optionalString(values, "LOG_DIR", defaultLogDir),
		InternalSocketPath: internalSocketPath,
		InternalRESTToken:  internalRESTToken,
	}, nil
}

func (c Config) HTTPAddr() string {
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

func randomSocketPath() (string, error) {
	id, err := randomToken(10)
	if err != nil {
		return "", err
	}
	return filepath.Join(os.TempDir(), "remnawave-internal-"+id+".sock"), nil
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
