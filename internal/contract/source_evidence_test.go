package contract

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedOfficialSourceEvidence(t *testing.T) {
	root := os.Getenv("REMNANODE_OFFICIAL_SOURCE")
	if root == "" {
		t.Skip("REMNANODE_OFFICIAL_SOURCE is not set")
	}

	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read official Git HEAD: %v", err)
	}
	if got := strings.TrimSpace(string(head)); got != OfficialNodeCommit {
		t.Fatalf("official Git HEAD = %s, want %s", got, OfficialNodeCommit)
	}

	packageRaw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("read official package.json: %v", err)
	}
	var packageData struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(packageRaw, &packageData); err != nil {
		t.Fatalf("decode official package.json: %v", err)
	}
	if packageData.Name != "@remnawave/node" || packageData.Version != OfficialNodeVersion {
		t.Fatalf("official package = %s@%s, want @remnawave/node@%s", packageData.Name, packageData.Version, OfficialNodeVersion)
	}

	for _, source := range OfficialSourceFiles() {
		path := filepath.Join(root, filepath.FromSlash(source))
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing contract evidence %s: %v", source, err)
			continue
		}
		if info.IsDir() || info.Size() == 0 {
			t.Errorf("contract evidence %s is not a non-empty file", source)
		}
	}
}
