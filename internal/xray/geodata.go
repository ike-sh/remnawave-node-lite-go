package xray

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"remnawave-node-lite-go/internal/artifact"
)

const geodataConcurrency = 5

var geodataFileName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type geodataAsset struct {
	URL  string
	File string
}

type geodataLoader struct {
	dir      string
	download artifactDownloadFunc
}

func newGeodataLoader(dir string) *geodataLoader {
	return &geodataLoader{dir: dir, download: artifact.Download}
}

func (l *geodataLoader) prepare(ctx context.Context, geodata any) {
	assets, err := parseGeodataAssets(geodata)
	if err != nil {
		log.Printf("warning: invalid geodata.assets, skipped: %v", err)
		return
	}
	if len(assets) == 0 {
		return
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		log.Printf("warning: create geodata directory: %v", err)
		return
	}

	groups := groupGeodataAssets(assets)
	workerCount := min(geodataConcurrency, len(groups))
	jobs := make(chan []geodataAsset)
	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range jobs {
				for _, asset := range group {
					if ctx.Err() != nil {
						return
					}
					l.prepareAsset(ctx, asset)
				}
			}
		}()
	}
	for _, group := range groups {
		select {
		case jobs <- group:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}

// groupGeodataAssets keeps assets targeting the same file on one worker. This
// prevents duplicate entries from racing on the shared ".download" path while
// retaining their configured order as fallback download sources.
func groupGeodataAssets(assets []geodataAsset) [][]geodataAsset {
	groups := make([][]geodataAsset, 0, len(assets))
	indexByFile := make(map[string]int, len(assets))
	for _, asset := range assets {
		if index, exists := indexByFile[asset.File]; exists {
			groups[index] = append(groups[index], asset)
			continue
		}
		indexByFile[asset.File] = len(groups)
		groups = append(groups, []geodataAsset{asset})
	}
	return groups
}

func parseGeodataAssets(geodata any) ([]geodataAsset, error) {
	if geodata == nil {
		return nil, nil
	}
	section, ok := geodata.(map[string]any)
	if !ok {
		return nil, errors.New("geodata must be an object")
	}
	raw, exists := section["assets"]
	if !exists || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("assets must be an array")
	}
	assets := make([]geodataAsset, 0, len(items))
	for index, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("assets[%d] must be an object", index)
		}
		rawURL, ok := entry["url"].(string)
		if !ok {
			return nil, fmt.Errorf("assets[%d].url must be a string", index)
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return nil, fmt.Errorf("assets[%d].url must be an absolute HTTPS URL", index)
		}
		file, ok := entry["file"].(string)
		if !ok || !validGeodataFile(file) {
			return nil, fmt.Errorf("assets[%d].file must be a plain file name", index)
		}
		assets = append(assets, geodataAsset{URL: parsed.String(), File: file})
	}
	return assets, nil
}

func validGeodataFile(file string) bool {
	return file != "." && file != ".." && geodataFileName.MatchString(file) && filepath.Base(file) == file
}

func (l *geodataLoader) prepareAsset(ctx context.Context, asset geodataAsset) {
	path := filepath.Join(l.dir, asset.File)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
		return
	}
	result, err := l.download(ctx, asset.URL, path, artifact.Options{})
	if err == nil {
		log.Printf("geodata asset downloaded: %s (%d bytes)", asset.File, result.Size)
		return
	}
	log.Printf("warning: geodata asset download failed for %s: %v", asset.URL, err)
	file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if createErr == nil {
		_ = file.Close()
		log.Printf("warning: created empty geodata stub %s", path)
		return
	}
	if !os.IsExist(createErr) {
		log.Printf("warning: create geodata stub %s: %v", path, createErr)
	}
}
