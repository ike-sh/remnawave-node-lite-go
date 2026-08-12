package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"remnawave-node-lite-go/internal/artifact"
)

const coreDownloadTimeout = 15 * time.Second

type artifactDownloadFunc func(context.Context, string, string, artifact.Options) (artifact.Result, error)

type corePaths struct {
	active string
	stock  string
	custom string
	staged string
	marker string
}

type coreMarker struct {
	URL           string `json:"url"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	MTimeUnixNano int64  `json:"mtimeUnixNano"`
	Version       string `json:"version"`
	InstalledAt   string `json:"installedAt"`
}

type coreSpec struct {
	URL    string
	SHA256 string
}

type coreLoader struct {
	paths       corePaths
	managed     bool
	download    artifactDownloadFunc
	readVersion func(context.Context, string) (string, error)
	now         func() time.Time
}

func newCoreLoader(activePath string) *coreLoader {
	clean := filepath.Clean(activePath)
	dir := filepath.Dir(clean)
	custom := filepath.Join(dir, "xray-custom")
	return &coreLoader{
		paths: corePaths{
			active: clean,
			stock:  filepath.Join(dir, "xray"),
			custom: custom,
			staged: custom + ".staged",
			marker: filepath.Join(dir, ".rw-core.json"),
		},
		managed:     filepath.Base(clean) == "rw-core",
		download:    artifact.Download,
		readVersion: readCoreVersion,
		now:         time.Now,
	}
}

func (l *coreLoader) prepare(ctx context.Context, geodata any) error {
	spec, present, err := parseCoreSpec(geodata)
	if err != nil {
		log.Printf("warning: invalid geodata.core, skipped: %v", err)
		return nil
	}
	if !l.managed {
		if present {
			log.Printf("warning: geodata.core ignored because XRAY_BIN %q is not a managed rw-core link", l.paths.active)
		}
		return nil
	}
	if !present {
		return l.restoreStock()
	}
	if l.isInstalled(spec) {
		return nil
	}
	if err := l.install(ctx, spec); err != nil {
		// Keep the previous active core running, matching upstream's fail-open
		// behavior for a bad custom-core download.
		log.Printf("warning: custom core installation failed: %v", err)
	}
	return nil
}

func parseCoreSpec(geodata any) (coreSpec, bool, error) {
	if geodata == nil {
		return coreSpec{}, false, nil
	}
	section, ok := geodata.(map[string]any)
	if !ok {
		return coreSpec{}, false, errors.New("geodata must be an object")
	}
	raw, exists := section["core"]
	if !exists || raw == nil {
		return coreSpec{}, false, nil
	}
	core, ok := raw.(map[string]any)
	if !ok {
		return coreSpec{}, false, errors.New("core must be an object")
	}
	rawURL, ok := core["url"].(string)
	if !ok {
		return coreSpec{}, false, errors.New("core.url must be a string")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return coreSpec{}, false, errors.New("core.url must be an absolute HTTPS URL")
	}
	sha, ok := core["sha256"].(string)
	if !ok || !isSHA256(sha) {
		return coreSpec{}, false, errors.New("core.sha256 must contain 64 hexadecimal characters")
	}
	return coreSpec{URL: parsed.String(), SHA256: strings.ToLower(sha)}, true, nil
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func (l *coreLoader) isInstalled(spec coreSpec) bool {
	raw, err := os.ReadFile(l.paths.marker)
	if err != nil {
		return false
	}
	var marker coreMarker
	if json.Unmarshal(raw, &marker) != nil || marker.URL != spec.URL || marker.SHA256 != spec.SHA256 {
		return false
	}
	info, err := os.Lstat(l.paths.custom)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 ||
		info.Size() != marker.Size || info.ModTime().UnixNano() != marker.MTimeUnixNano {
		return false
	}
	target, err := os.Readlink(l.paths.active)
	if err != nil {
		return false
	}
	return resolveLink(l.paths.active, target) == filepath.Clean(l.paths.custom)
}

func (l *coreLoader) install(ctx context.Context, spec coreSpec) error {
	if err := l.ensureActiveCanBeManaged(); err != nil {
		return err
	}
	if err := os.Remove(l.paths.staged); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale staged core: %w", err)
	}
	result, err := l.download(ctx, spec.URL, l.paths.staged, artifact.Options{
		TotalTimeout:   coreDownloadTimeout,
		ExpectedSHA256: spec.SHA256,
	})
	if err != nil {
		return err
	}
	defer os.Remove(l.paths.staged)

	if err := os.Chmod(l.paths.staged, 0o755); err != nil {
		return fmt.Errorf("make staged core executable: %w", err)
	}
	version, err := l.readVersion(ctx, l.paths.staged)
	if err != nil {
		return fmt.Errorf("validate staged core: %w", err)
	}
	if err := os.Rename(l.paths.staged, l.paths.custom); err != nil {
		return fmt.Errorf("activate custom core file: %w", err)
	}
	if err := l.pointLinkAt(l.paths.custom); err != nil {
		return fmt.Errorf("activate custom core link: %w", err)
	}
	info, err := os.Stat(l.paths.custom)
	if err != nil {
		return fmt.Errorf("stat custom core: %w", err)
	}
	marker := coreMarker{
		URL:           spec.URL,
		SHA256:        result.SHA256,
		Size:          info.Size(),
		MTimeUnixNano: info.ModTime().UnixNano(),
		Version:       version,
		InstalledAt:   l.now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeJSONAtomic(l.paths.marker, marker, 0o600); err != nil {
		return fmt.Errorf("write custom core marker: %w", err)
	}
	log.Printf("custom core installed: %s (%d bytes)", version, result.Size)
	return nil
}

func (l *coreLoader) restoreStock() error {
	target, err := os.Readlink(l.paths.active)
	if err != nil {
		if os.IsNotExist(err) || !isLink(l.paths.active) {
			return nil
		}
		return fmt.Errorf("read rw-core link: %w", err)
	}
	if resolveLink(l.paths.active, target) == filepath.Clean(l.paths.stock) {
		return nil
	}
	info, err := os.Stat(l.paths.stock)
	if err != nil {
		return fmt.Errorf("stock core is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("stock core %q is not an executable regular file", l.paths.stock)
	}
	if err := l.pointLinkAt(l.paths.stock); err != nil {
		return fmt.Errorf("restore stock core link: %w", err)
	}
	_ = os.Remove(l.paths.marker)
	_ = os.Remove(l.paths.custom)
	log.Printf("no custom core configured; restored stock rw-core")
	return nil
}

func (l *coreLoader) ensureActiveCanBeManaged() error {
	info, err := os.Lstat(l.paths.active)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect rw-core path: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("refusing to replace non-symlink XRAY_BIN %q", l.paths.active)
	}
	return nil
}

func (l *coreLoader) pointLinkAt(target string) error {
	tmp := l.paths.active + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.paths.active); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func resolveLink(linkPath, target string) string {
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	return filepath.Clean(target)
}

func isLink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readCoreVersion(ctx context.Context, path string) (string, error) {
	versionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := execCommandOutput(versionCtx, path, "version")
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.SplitN(string(output), "\n", 2)[0])
	if line == "" {
		return "", errors.New("core produced no version output")
	}
	return line, nil
}
