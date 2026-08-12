package xray

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"remnawave-node-lite-go/internal/artifact"
)

func TestParseGeodataAssetsRejectsTraversal(t *testing.T) {
	_, err := parseGeodataAssets(map[string]any{
		"assets": []any{map[string]any{
			"url":  "https://example.com/geo.dat",
			"file": "../geo.dat",
		}},
	})
	if err == nil {
		t.Fatal("expected traversal file name to be rejected")
	}
}

func TestGeodataLoaderDownloadsAndSkipsExistingAsset(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("geodata"))
	}))
	defer server.Close()
	dir := t.TempDir()
	loader := newGeodataLoader(dir)
	loader.download = func(ctx context.Context, rawURL, path string, opts artifact.Options) (artifact.Result, error) {
		opts.Client = server.Client()
		return artifact.Download(ctx, rawURL, path, opts)
	}
	config := map[string]any{
		"assets": []any{map[string]any{"url": server.URL, "file": "geo-custom.dat"}},
	}
	loader.prepare(context.Background(), config)
	server.Close()
	loader.prepare(context.Background(), config)
	got, err := os.ReadFile(filepath.Join(dir, "geo-custom.dat"))
	if err != nil || string(got) != "geodata" {
		t.Fatalf("downloaded asset = %q, err=%v", got, err)
	}
}

func TestGeodataLoaderCreatesStubAfterDownloadFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	dir := t.TempDir()
	loader := newGeodataLoader(dir)
	loader.download = func(ctx context.Context, rawURL, path string, opts artifact.Options) (artifact.Result, error) {
		opts.Client = server.Client()
		return artifact.Download(ctx, rawURL, path, opts)
	}
	loader.prepare(context.Background(), map[string]any{
		"assets": []any{map[string]any{"url": server.URL, "file": "missing.dat"}},
	})
	info, err := os.Stat(filepath.Join(dir, "missing.dat"))
	if err != nil || info.Size() != 0 {
		t.Fatalf("expected empty stub, info=%v err=%v", info, err)
	}
	if strings.Contains(filepath.Join(dir, "missing.dat"), "..") {
		t.Fatal("unexpected test path")
	}
}

func TestGeodataLoaderSerializesDuplicateDestinations(t *testing.T) {
	dir := t.TempDir()
	loader := newGeodataLoader(dir)
	var active atomic.Int32
	var maxActive atomic.Int32
	var calls atomic.Int32
	loader.download = func(_ context.Context, _, path string, _ artifact.Options) (artifact.Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		if calls.Add(1) == 1 {
			return artifact.Result{}, errors.New("first source failed")
		}
		if err := os.WriteFile(path, []byte("fallback"), 0o600); err != nil {
			return artifact.Result{}, err
		}
		return artifact.Result{Size: 8}, nil
	}

	loader.prepare(context.Background(), map[string]any{"assets": []any{
		map[string]any{"url": "https://example.com/first", "file": "same.dat"},
		map[string]any{"url": "https://example.com/fallback", "file": "same.dat"},
	}})

	if maxActive.Load() != 1 {
		t.Fatalf("duplicate destination concurrency = %d, want 1", maxActive.Load())
	}
	got, err := os.ReadFile(filepath.Join(dir, "same.dat"))
	if err != nil || string(got) != "fallback" {
		t.Fatalf("fallback asset = %q, err=%v", got, err)
	}
}

func TestGeodataLoaderLimitsDistinctAssetConcurrency(t *testing.T) {
	dir := t.TempDir()
	loader := newGeodataLoader(dir)
	var active atomic.Int32
	var maxActive atomic.Int32
	var once sync.Once
	release := make(chan struct{})
	loader.download = func(_ context.Context, _, path string, _ artifact.Options) (artifact.Result, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		if current == geodataConcurrency {
			once.Do(func() { close(release) })
		}
		select {
		case <-release:
		case <-time.After(time.Second):
			return artifact.Result{}, errors.New("worker pool did not reach expected concurrency")
		}
		if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
			return artifact.Result{}, err
		}
		return artifact.Result{Size: 2}, nil
	}

	assets := make([]any, 0, 20)
	for index := range 20 {
		assets = append(assets, map[string]any{
			"url":  "https://example.com/asset",
			"file": fmt.Sprintf("asset-%d.dat", index),
		})
	}
	loader.prepare(context.Background(), map[string]any{"assets": assets})
	if maximum := maxActive.Load(); maximum != geodataConcurrency {
		t.Fatalf("maximum concurrency = %d, want %d", maximum, geodataConcurrency)
	}
}
