package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	testValidationNow = time.Now().UTC().Truncate(time.Second)
	testCandidateAt   = testValidationNow.Add(-30 * time.Hour)
	testStartedAt     = testValidationNow.Add(-26 * time.Hour).Format(time.RFC3339)
	testFinishedAt    = testValidationNow.Add(-2 * time.Hour).Format(time.RFC3339)
	testAcceptedAt    = testValidationNow.Add(-time.Hour).Format(time.RFC3339)
)

type releaseFixture struct {
	t         *testing.T
	root      string
	manifest  string
	candidate string
}

func TestValidateReleaseEvidence(t *testing.T) {
	fixture := newReleaseFixture(t)

	result, err := validateReleaseEvidenceAt(context.Background(), fixture.root, fixture.manifest, expectedReleaseTag, testValidationNow)
	if err != nil {
		t.Fatalf("validateReleaseEvidence() error = %v", err)
	}
	if result.ReleaseTag != expectedReleaseTag || result.CandidateCommit != fixture.candidate {
		t.Fatalf("validateReleaseEvidence() = %#v", result)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if result.NodeSHA256ByArch[arch] != testNodeSHA(arch) {
			t.Fatalf("NodeSHA256ByArch[%s] = %q, want %q", arch, result.NodeSHA256ByArch[arch], testNodeSHA(arch))
		}
	}
}

func TestValidateReleaseEvidenceFailures(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*releaseFixture)
		leaveDirty bool
		wantErr    string
	}{
		{
			name: "manifest unknown field",
			mutate: func(fixture *releaseFixture) {
				raw := fixture.readFile(manifestRepositoryPath)
				fixture.writeFile(manifestRepositoryPath, appendJSONField(raw, `"unexpected":true`))
			},
			wantErr: "unknown field \"unexpected\"",
		},
		{
			name: "manifest duplicate field",
			mutate: func(fixture *releaseFixture) {
				raw := fixture.readFile(manifestRepositoryPath)
				fixture.writeFile(manifestRepositoryPath, appendJSONField(raw, `"decision":"pass"`))
			},
			wantErr: "duplicate JSON field \"decision\"",
		},
		{
			name: "manifest case-insensitive field alias",
			mutate: func(fixture *releaseFixture) {
				raw := fixture.readFile(manifestRepositoryPath)
				fixture.writeFile(manifestRepositoryPath, appendJSONField(raw, `"Decision":"pass"`))
			},
			wantErr: "unknown field \"Decision\"",
		},
		{
			name: "evidence unknown field",
			mutate: func(fixture *releaseFixture) {
				path := acceptanceDirectory + "/panel.json"
				raw := fixture.readFile(path)
				fixture.writeFile(path, appendJSONField(raw, `"unexpected":true`))
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "unknown field \"unexpected\"",
		},
		{
			name: "evidence digest mismatch",
			mutate: func(fixture *releaseFixture) {
				path := acceptanceDirectory + "/panel.json"
				fixture.writeFile(path, append(fixture.readFile(path), '\n'))
			},
			wantErr: "SHA-256=",
		},
		{
			name: "untracked evidence",
			mutate: func(fixture *releaseFixture) {
				fixture.git("rm", "--cached", "--", acceptanceDirectory+"/panel.json")
			},
			leaveDirty: true,
			wantErr:    "must have exactly one Git index entry",
		},
		{
			name: "intent-to-add evidence",
			mutate: func(fixture *releaseFixture) {
				path := acceptanceDirectory + "/panel.json"
				fixture.git("rm", "--cached", "--", path)
				fixture.git("add", "--intent-to-add", "--", path)
			},
			leaveDirty: true,
			wantErr:    "index entry does not match HEAD",
		},
		{
			name: "conflicted evidence index",
			mutate: func(fixture *releaseFixture) {
				fixture.makeIndexConflict(acceptanceDirectory + "/panel.json")
			},
			leaveDirty: true,
			wantErr:    "must have exactly one Git index entry",
		},
		{
			name: "staged evidence differs from HEAD",
			mutate: func(fixture *releaseFixture) {
				path := acceptanceDirectory + "/panel.json"
				fixture.writeFile(path, append(fixture.readFile(path), '\n'))
				fixture.git("add", "--", path)
			},
			leaveDirty: true,
			wantErr:    "index entry does not match HEAD",
		},
		{
			name: "worktree evidence differs from HEAD",
			mutate: func(fixture *releaseFixture) {
				path := acceptanceDirectory + "/panel.json"
				fixture.writeFile(path, append(fixture.readFile(path), '\n'))
			},
			leaveDirty: true,
			wantErr:    "worktree bytes do not match HEAD blob",
		},
		{
			name: "worktree evidence is executable",
			mutate: func(fixture *releaseFixture) {
				path := filepath.Join(fixture.root, filepath.FromSlash(acceptanceDirectory+"/panel.json"))
				if err := os.Chmod(path, 0o755); err != nil {
					fixture.t.Fatalf("make Panel evidence executable: %v", err)
				}
			},
			leaveDirty: true,
			wantErr:    "worktree mode is executable",
		},
		{
			name: "acceptance ancestor symlink",
			mutate: func(fixture *releaseFixture) {
				fixture.replaceAcceptanceDirectoryWithSymlink()
			},
			leaveDirty: true,
			wantErr:    "contains symlink component",
		},
		{
			name: "evidence HEAD entry is a symlink",
			mutate: func(fixture *releaseFixture) {
				path := acceptanceDirectory + "/panel.json"
				original := fixture.readFile(path)
				absolute := filepath.Join(fixture.root, filepath.FromSlash(path))
				if err := os.Remove(absolute); err != nil {
					fixture.t.Fatalf("remove Panel evidence: %v", err)
				}
				if err := os.Symlink("systemd.json", absolute); err != nil {
					fixture.t.Fatalf("symlink Panel evidence: %v", err)
				}
				fixture.commitAll("replace Panel evidence with a symlink")
				if err := os.Remove(absolute); err != nil {
					fixture.t.Fatalf("remove Panel evidence symlink: %v", err)
				}
				fixture.writeFile(path, original)
			},
			leaveDirty: true,
			wantErr:    "has HEAD mode 120000",
		},
		{
			name: "Panel route mismatch",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.RoutesPassed = 25
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "26 total, 26 passed, 0 semantic mismatches",
		},
		{
			name: "rw-core process-group cleanup false",
			mutate: func(fixture *releaseFixture) {
				var evidence systemEvidence
				fixture.readJSON(acceptanceDirectory+"/systemd.json", &evidence)
				evidence.Checks.RWCoreProcessGroupCleanup = false
				fixture.writeJSON(acceptanceDirectory+"/systemd.json", evidence)
				fixture.updateEvidenceHash("systemd")
			},
			wantErr: "system check rwCoreProcessGroupCleanup must be true",
		},
		{
			name: "rw-core process-group cleanup missing",
			mutate: func(fixture *releaseFixture) {
				fixture.removeEvidenceCheck("systemd", "rwCoreProcessGroupCleanup")
			},
			wantErr: "system check rwCoreProcessGroupCleanup must be true",
		},
		{
			name: "lifecycle plugin serialization false",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.Checks.LifecyclePluginSerialization = false
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "Panel check lifecyclePluginSerialization must be true",
		},
		{
			name: "lifecycle plugin serialization missing",
			mutate: func(fixture *releaseFixture) {
				fixture.removeEvidenceCheck("panel", "lifecyclePluginSerialization")
			},
			wantErr: "Panel check lifecyclePluginSerialization must be true",
		},
		{
			name: "resource peak over limit",
			mutate: func(fixture *releaseFixture) {
				var evidence resourceFaultEvidence
				fixture.readJSON(acceptanceDirectory+"/resource-fault.json", &evidence)
				evidence.Metrics.PeakMemoryMiB = 449
				fixture.writeJSON(acceptanceDirectory+"/resource-fault.json", evidence)
				fixture.updateEvidenceHash("resource-fault")
			},
			wantErr: "peak memory 1..448 MiB",
		},
		{
			name: "resource soak shorter than declared",
			mutate: func(fixture *releaseFixture) {
				var evidence resourceFaultEvidence
				fixture.readJSON(acceptanceDirectory+"/resource-fault.json", &evidence)
				evidence.StartedAt = testValidationNow.Add(-3 * time.Hour).Format(time.RFC3339)
				fixture.writeJSON(acceptanceDirectory+"/resource-fault.json", evidence)
				fixture.updateEvidenceHash("resource-fault")
			},
			wantErr: "wall-clock duration=3600 seconds",
		},
		{
			name: "resource Node artifact differs from system evidence",
			mutate: func(fixture *releaseFixture) {
				var evidence resourceFaultEvidence
				fixture.readJSON(acceptanceDirectory+"/resource-fault.json", &evidence)
				evidence.Node.BinarySHA256 = strings.Repeat("c", 64)
				fixture.writeJSON(acceptanceDirectory+"/resource-fault.json", evidence)
				fixture.updateEvidenceHash("resource-fault")
			},
			wantErr: "node binarySha256 for arm64 does not match system evidence",
		},
		{
			name: "Panel artifact differs from system evidence",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.Artifacts[0].NodeBinarySHA256 = strings.Repeat("d", 64)
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "target systemd nodeBinarySha256 does not match system evidence",
		},
		{
			name: "resource rw-core artifact differs from system evidence",
			mutate: func(fixture *releaseFixture) {
				var evidence resourceFaultEvidence
				fixture.readJSON(acceptanceDirectory+"/resource-fault.json", &evidence)
				evidence.RWCore.BinarySHA256 = strings.Repeat("e", 64)
				fixture.writeJSON(acceptanceDirectory+"/resource-fault.json", evidence)
				fixture.updateEvidenceHash("resource-fault")
			},
			wantErr: "rw-core binarySha256 for arm64 does not match system evidence",
		},
		{
			name: "missing Panel artifacts",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.Artifacts = nil
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "Panel artifacts count=0, want 2",
		},
		{
			name: "missing no-swap policy field",
			mutate: func(fixture *releaseFixture) {
				manifest := fixture.readManifest()
				manifest.Policy.Swap = nil
				fixture.writeManifest(manifest)
			},
			wantErr: "acceptance policy must be",
		},
		{
			name: "missing semantic mismatch count",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.SemanticMismatches = nil
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "semanticMismatches is required",
		},
		{
			name: "system architectures incomplete",
			mutate: func(fixture *releaseFixture) {
				var evidence systemEvidence
				fixture.readJSON(acceptanceDirectory+"/openrc.json", &evidence)
				evidence.Environment.Arch = "amd64"
				evidence.RWCore = validRWCoreArtifact("amd64")
				fixture.writeJSON(acceptanceDirectory+"/openrc.json", evidence)
				fixture.updateEvidenceHash("openrc")
			},
			wantErr: "must cover amd64 and arm64",
		},
		{
			name: "fault recovery check failed",
			mutate: func(fixture *releaseFixture) {
				var evidence resourceFaultEvidence
				fixture.readJSON(acceptanceDirectory+"/resource-fault.json", &evidence)
				evidence.Checks.CoreKillRecovery = false
				fixture.writeJSON(acceptanceDirectory+"/resource-fault.json", evidence)
				fixture.updateEvidenceHash("resource-fault")
			},
			wantErr: "all resource and fault-recovery checks must pass",
		},
		{
			name: "candidate tree mismatch",
			mutate: func(fixture *releaseFixture) {
				manifest := fixture.readManifest()
				manifest.CandidateTree = strings.Repeat("0", 40)
				fixture.writeManifest(manifest)
			},
			wantErr: "candidate tree=",
		},
		{
			name: "evidence predates candidate",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.StartedAt = "2000-01-01T00:00:00Z"
				evidence.FinishedAt = "2000-01-01T01:00:00Z"
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "startedAt predates candidate commit",
		},
		{
			name: "acceptedAt is in the future",
			mutate: func(fixture *releaseFixture) {
				manifest := fixture.readManifest()
				manifest.AcceptedAt = testValidationNow.Add(10 * time.Minute).Format(time.RFC3339)
				fixture.writeManifest(manifest)
			},
			wantErr: "later than current time plus 5m0s",
		},
		{
			name: "evidence finishes in the future",
			mutate: func(fixture *releaseFixture) {
				var evidence panelEvidence
				fixture.readJSON(acceptanceDirectory+"/panel.json", &evidence)
				evidence.StartedAt = testValidationNow.Add(-time.Hour).Format(time.RFC3339)
				evidence.FinishedAt = testValidationNow.Add(10 * time.Minute).Format(time.RFC3339)
				fixture.writeJSON(acceptanceDirectory+"/panel.json", evidence)
				fixture.updateEvidenceHash("panel")
			},
			wantErr: "panel finishedAt",
		},
		{
			name: "acceptedAt is later than HEAD commit",
			mutate: func(fixture *releaseFixture) {
				fixture.amendCommitAt(testValidationNow.Add(-3 * time.Hour))
			},
			wantErr: "later than HEAD commit time plus 5m0s",
		},
		{
			name: "acceptedAt predates evidence completion",
			mutate: func(fixture *releaseFixture) {
				manifest := fixture.readManifest()
				manifest.AcceptedAt = testValidationNow.Add(-3 * time.Hour).Format(time.RFC3339)
				fixture.writeManifest(manifest)
			},
			wantErr: "is before evidence completion",
		},
		{
			name: "open P2 risk",
			mutate: func(fixture *releaseFixture) {
				manifest := fixture.readManifest()
				manifest.Risks = []releaseRisk{{
					ID: "R-P2", Severity: "P2", Status: "open",
					Summary: "known defect", Mitigation: "fix before release", ReleaseBlocking: boolPointer(false),
				}}
				fixture.writeManifest(manifest)
			},
			wantErr: "is an unclosed P2",
		},
		{
			name: "post-candidate code change",
			mutate: func(fixture *releaseFixture) {
				fixture.writeFile("internal/changed.go", []byte("package internal\n"))
				fixture.commitAll("change code after acceptance candidate")
			},
			wantErr: "outside the release-finalization allowlist",
		},
		{
			name: "post-candidate code change then revert",
			mutate: func(fixture *releaseFixture) {
				fixture.writeFile("code.txt", []byte("temporarily changed\n"))
				fixture.commitAll("temporarily change candidate code")
				fixture.writeFile("code.txt", []byte("release candidate\n"))
				fixture.commitAll("revert candidate code")
			},
			wantErr: "outside the release-finalization allowlist",
		},
		{
			name: "post-candidate merge",
			mutate: func(fixture *releaseFixture) {
				fixture.createAllowedMergeCommit()
			},
			wantErr: "merges are not allowed during release finalization",
		},
		{
			name: "post-candidate code rename",
			mutate: func(fixture *releaseFixture) {
				destination := "docs/releases/v0.1.0.md"
				fixture.git("rm", "--", destination)
				if err := os.MkdirAll(filepath.Join(fixture.root, filepath.Dir(destination)), 0o755); err != nil {
					fixture.t.Fatalf("recreate release-note directory: %v", err)
				}
				fixture.git("mv", "code.txt", destination)
				fixture.commitAll("rename code into acceptance directory")
			},
			wantErr: "outside the release-finalization allowlist",
		},
		{
			name: "extra acceptance file",
			mutate: func(fixture *releaseFixture) {
				fixture.writeFile(acceptanceDirectory+"/raw-output.json", []byte("{}\n"))
			},
			wantErr: "acceptance files in HEAD must be exactly",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleaseFixture(t)
			test.mutate(fixture)
			if !test.leaveDirty && fixture.isDirty() {
				fixture.commitAll("record invalid release evidence")
			}

			_, err := validateReleaseEvidenceAt(context.Background(), fixture.root, fixture.manifest, expectedReleaseTag, testValidationNow)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateReleaseEvidence() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestRunRequiresFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-manifest and -tag are required") {
		t.Fatalf("run() stderr = %q", stderr.String())
	}
}

func TestRunValidatesReleaseArtifacts(t *testing.T) {
	fixture := newReleaseFixture(t)
	artifactsDirectory := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		path := filepath.Join(artifactsDirectory, "remnanode-lite_linux_"+arch)
		if err := os.WriteFile(path, testNodeBinary(arch), 0o755); err != nil {
			t.Fatalf("write %s artifact: %v", arch, err)
		}
	}
	args := []string{
		"-manifest", fixture.manifest,
		"-tag", expectedReleaseTag,
		"-artifacts", artifactsDirectory,
	}
	var stdout, stderr bytes.Buffer
	if code := runInRepository(fixture.root, args, &stdout, &stderr); code != 0 {
		t.Fatalf("runInRepository() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "release evidence check passed") {
		t.Fatalf("runInRepository() stdout = %q", stdout.String())
	}

	arm64Path := filepath.Join(artifactsDirectory, "remnanode-lite_linux_arm64")
	if err := os.WriteFile(arm64Path, []byte("wrong artifact\n"), 0o755); err != nil {
		t.Fatalf("replace arm64 artifact: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runInRepository(fixture.root, args, &stdout, &stderr); code != 1 {
		t.Fatalf("runInRepository() mismatch code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "arm64 artifact SHA-256=") {
		t.Fatalf("runInRepository() mismatch stderr = %q", stderr.String())
	}
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for release evidence tests")
	}

	fixture := &releaseFixture{t: t, root: t.TempDir(), manifest: manifestRepositoryPath}
	fixture.git("init", "-q")
	fixture.writeFile("code.txt", []byte("release candidate\n"))
	fixture.commitAllAt("freeze release candidate", testCandidateAt)
	fixture.candidate = fixture.git("rev-parse", "HEAD")
	candidateTree := fixture.git("rev-parse", fixture.candidate+"^{tree}")

	systemd := validSystemEvidence("systemd", "amd64", fixture.candidate)
	openrc := validSystemEvidence("openrc", "arm64", fixture.candidate)
	panel := panelEvidence{
		evidenceCommon: validEvidenceCommon("panel", fixture.candidate),
		PanelVersion:   expectedPanelVersion,
		Targets:        []string{"systemd", "openrc"},
		Artifacts: []panelArtifactIdentity{
			{
				Target:             "systemd",
				Arch:               "amd64",
				NodeBinarySHA256:   testNodeSHA("amd64"),
				RWCoreBinarySHA256: testCoreSHA("amd64"),
			},
			{
				Target:             "openrc",
				Arch:               "arm64",
				NodeBinarySHA256:   testNodeSHA("arm64"),
				RWCoreBinarySHA256: testCoreSHA("arm64"),
			},
		},
		RoutesTotal:        26,
		RoutesPassed:       26,
		SemanticMismatches: intPointer(0),
		Checks: panelChecks{
			NodeRegistration:             true,
			XrayLifecycle:                true,
			Stats:                        true,
			UserMutations:                true,
			PluginSync:                   true,
			LifecyclePluginSerialization: true,
		},
	}
	resource := resourceFaultEvidence{
		evidenceCommon: validEvidenceCommon("resource-fault", fixture.candidate),
		Environment: machineEnvironment{
			OSID: "alpine", OSVersion: "3.22", Init: "none", Arch: "arm64",
			Kernel: "6.8.0", MemoryMiB: 512, CPUCount: 1, DiskMiB: 2048,
		},
		Node:   validNodeArtifact("arm64"),
		RWCore: validRWCoreArtifact("arm64"),
		Metrics: resourceFaultMetrics{
			Users: 50000, SoakSeconds: 86400, PeakMemoryMiB: 144,
			OOMKills: intPointer(0), ProjectDiskPeakMiB: 160,
		},
		Checks: resourceFaultChecks{
			NoSwap: true, CoreKillRecovery: true, NodeRestartRecovery: true,
			PanelDisconnectRecovery: true, NFTFailureRetry: true,
			LogFaultStormBounded: true, FailedUpgradeRollback: true,
		},
	}

	fixture.writeJSON(acceptanceDirectory+"/systemd.json", systemd)
	fixture.writeJSON(acceptanceDirectory+"/openrc.json", openrc)
	fixture.writeJSON(acceptanceDirectory+"/panel.json", panel)
	fixture.writeJSON(acceptanceDirectory+"/resource-fault.json", resource)

	manifest := acceptanceManifest{
		SchemaVersion:   1,
		ReleaseVersion:  expectedReleaseVersion,
		ReleaseTag:      expectedReleaseTag,
		CandidateCommit: fixture.candidate,
		CandidateTree:   candidateTree,
		AcceptedAt:      testAcceptedAt,
		Decision:        "pass",
		OfficialNode: officialNodeTarget{
			Version: expectedOfficialNodeVersion,
			Commit:  expectedOfficialNodeCommit,
		},
		PanelTarget: panelTarget{Version: expectedPanelVersion},
		RWCore: rwCoreTarget{
			Version: expectedRWCoreVersion,
			Commit:  expectedRWCoreCommit,
			SHA256: architectureSHAs{
				AMD64: expectedAMD64AssetSHA,
				ARM64: expectedARM64AssetSHA,
			},
		},
		Policy: acceptancePolicy{
			WholeMachineMemoryMiB: 512,
			ServiceMemoryMaxMiB:   448,
			CPUCount:              1, DiskMiB: 2048, Users: 50000, Swap: boolPointer(false), SoakSeconds: 86400,
		},
		Evidence: []evidenceReference{
			fixture.evidenceReference("systemd"),
			fixture.evidenceReference("openrc"),
			fixture.evidenceReference("panel"),
			fixture.evidenceReference("resource-fault"),
		},
		Risks: []releaseRisk{},
	}
	fixture.writeManifest(manifest)
	fixture.writeFile("README.md", []byte("# release\n"))
	fixture.writeFile("docs/CHANGELOG.md", []byte("# changelog\n"))
	fixture.writeFile("docs/development/roadmap.md", []byte("# roadmap\n"))
	fixture.writeFile("docs/releases/v0.1.0.md", []byte("# v0.1.0\n"))
	fixture.commitAll("record release acceptance")
	return fixture
}

func validEvidenceCommon(kind, candidate string) evidenceCommon {
	return evidenceCommon{
		SchemaVersion: 1, Kind: kind, CandidateCommit: candidate, Status: "pass",
		StartedAt: testStartedAt, FinishedAt: testFinishedAt, Command: []string{"scripts/acceptance.sh", kind},
	}
}

func validSystemEvidence(kind, arch, candidate string) systemEvidence {
	osID, osVersion, initSystem := "ubuntu", "24.04", "systemd"
	if kind == "openrc" {
		osID, osVersion, initSystem = "alpine", "3.22", "openrc"
	}
	return systemEvidence{
		evidenceCommon: validEvidenceCommon(kind, candidate),
		Environment: machineEnvironment{
			OSID: osID, OSVersion: osVersion, Init: initSystem, Arch: arch,
			Kernel: "6.8.0", MemoryMiB: 512, CPUCount: 1, DiskMiB: 2048,
		},
		Node:   validNodeArtifact(arch),
		RWCore: validRWCoreArtifact(arch),
		Checks: systemChecks{
			FreshInstall: true, RepeatInstall: true, StartStopRestart: true,
			SuccessfulUpgrade: true, FailedUpgradeRollback: true, RebootPanelResync: true,
			CapabilityBoundary: true, UninstallIsolation: true, NFTNamespace: true,
			SocketKillNamespace: true, RWCoreProcessGroupCleanup: true,
		},
	}
}

func validNodeArtifact(arch string) nodeArtifact {
	return nodeArtifact{VersionOutput: expectedVersionOutput, BinarySHA256: testNodeSHA(arch)}
}

func validRWCoreArtifact(arch string) rwCoreArtifact {
	return rwCoreArtifact{
		Version: expectedRWCoreVersion, Commit: expectedRWCoreCommit,
		AssetSHA256: expectedAssetSHAByArch[arch], BinarySHA256: testCoreSHA(arch),
	}
}

func testNodeBinary(arch string) []byte {
	return []byte("release-node-binary-" + arch + "\n")
}

func testNodeSHA(arch string) string {
	digest := sha256.Sum256(testNodeBinary(arch))
	return hex.EncodeToString(digest[:])
}

func testCoreSHA(arch string) string {
	digest := sha256.Sum256([]byte("installed-rw-core-" + arch + "\n"))
	return hex.EncodeToString(digest[:])
}

func (fixture *releaseFixture) evidenceReference(kind string) evidenceReference {
	fixture.t.Helper()
	path := acceptanceDirectory + "/" + kind + ".json"
	return evidenceReference{Kind: kind, Path: path, SHA256: fixture.fileSHA(path), Status: "pass"}
}

func (fixture *releaseFixture) updateEvidenceHash(kind string) {
	fixture.t.Helper()
	manifest := fixture.readManifest()
	for index := range manifest.Evidence {
		if manifest.Evidence[index].Kind == kind {
			manifest.Evidence[index].SHA256 = fixture.fileSHA(manifest.Evidence[index].Path)
			fixture.writeManifest(manifest)
			return
		}
	}
	fixture.t.Fatalf("manifest has no %s evidence", kind)
}

func (fixture *releaseFixture) removeEvidenceCheck(kind, check string) {
	fixture.t.Helper()
	path := acceptanceDirectory + "/" + kind + ".json"
	var document map[string]any
	fixture.readJSON(path, &document)
	checks, ok := document["checks"].(map[string]any)
	if !ok {
		fixture.t.Fatalf("%s checks are not a JSON object", kind)
	}
	if _, ok := checks[check]; !ok {
		fixture.t.Fatalf("%s check %s is already missing", kind, check)
	}
	delete(checks, check)
	fixture.writeJSON(path, document)
	fixture.updateEvidenceHash(kind)
}

func (fixture *releaseFixture) readManifest() acceptanceManifest {
	fixture.t.Helper()
	var manifest acceptanceManifest
	fixture.readJSON(manifestRepositoryPath, &manifest)
	return manifest
}

func (fixture *releaseFixture) writeManifest(manifest acceptanceManifest) {
	fixture.t.Helper()
	fixture.writeJSON(manifestRepositoryPath, manifest)
}

func (fixture *releaseFixture) readJSON(path string, target any) {
	fixture.t.Helper()
	if err := json.Unmarshal(fixture.readFile(path), target); err != nil {
		fixture.t.Fatalf("decode %s: %v", path, err)
	}
}

func (fixture *releaseFixture) writeJSON(path string, value any) {
	fixture.t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fixture.t.Fatalf("encode %s: %v", path, err)
	}
	fixture.writeFile(path, append(raw, '\n'))
}

func (fixture *releaseFixture) readFile(path string) []byte {
	fixture.t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture.root, filepath.FromSlash(path)))
	if err != nil {
		fixture.t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func (fixture *releaseFixture) writeFile(path string, raw []byte) {
	fixture.t.Helper()
	abs := filepath.Join(fixture.root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		fixture.t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		fixture.t.Fatalf("write %s: %v", path, err)
	}
}

func (fixture *releaseFixture) fileSHA(path string) string {
	fixture.t.Helper()
	digest := sha256.Sum256(fixture.readFile(path))
	return hex.EncodeToString(digest[:])
}

func (fixture *releaseFixture) commitAll(message string) {
	fixture.t.Helper()
	fixture.git("add", "--all")
	fixture.git(
		"-c", "user.name=Release Test",
		"-c", "user.email=release-test@example.invalid",
		"commit", "--no-gpg-sign", "-qm", message,
	)
}

func (fixture *releaseFixture) commitAllAt(message string, committedAt time.Time) {
	fixture.t.Helper()
	fixture.git("add", "--all")
	fixture.gitWithEnv(
		[]string{
			"GIT_AUTHOR_DATE=" + committedAt.Format(time.RFC3339),
			"GIT_COMMITTER_DATE=" + committedAt.Format(time.RFC3339),
		},
		"-c", "user.name=Release Test",
		"-c", "user.email=release-test@example.invalid",
		"commit", "--no-gpg-sign", "-qm", message,
	)
}

func (fixture *releaseFixture) amendCommitAt(committedAt time.Time) {
	fixture.t.Helper()
	fixture.gitWithEnv(
		[]string{
			"GIT_AUTHOR_DATE=" + committedAt.Format(time.RFC3339),
			"GIT_COMMITTER_DATE=" + committedAt.Format(time.RFC3339),
		},
		"-c", "user.name=Release Test",
		"-c", "user.email=release-test@example.invalid",
		"commit", "--amend", "--no-edit", "--no-gpg-sign", "-q",
	)
}

func (fixture *releaseFixture) makeIndexConflict(path string) {
	fixture.t.Helper()
	blob := fixture.git("rev-parse", "HEAD:"+path)
	fixture.git("update-index", "--force-remove", "--", path)
	fixture.gitWithInput(
		fmt.Sprintf("100644 %s 1\t%s\n100644 %s 2\t%s\n", blob, path, blob, path),
		"update-index", "--index-info",
	)
}

func (fixture *releaseFixture) replaceAcceptanceDirectoryWithSymlink() {
	fixture.t.Helper()
	original := filepath.Join(fixture.root, filepath.FromSlash(acceptanceDirectory))
	destination := filepath.Join(fixture.t.TempDir(), "acceptance-v0.1.0")
	if err := os.Rename(original, destination); err != nil {
		fixture.t.Fatalf("move acceptance directory: %v", err)
	}
	if err := os.Symlink(destination, original); err != nil {
		fixture.t.Fatalf("symlink acceptance directory: %v", err)
	}
}

func (fixture *releaseFixture) createAllowedMergeCommit() {
	fixture.t.Helper()
	baseBranch := fixture.git("branch", "--show-current")
	fixture.git("checkout", "-qb", "release-side")
	fixture.writeFile("README.md", []byte("# release from side branch\n"))
	fixture.commitAll("update README on release side branch")
	fixture.git("checkout", "-q", baseBranch)
	fixture.writeFile("docs/CHANGELOG.md", []byte("# changelog on main branch\n"))
	fixture.commitAll("update changelog on main branch")
	fixture.git(
		"-c", "user.name=Release Test",
		"-c", "user.email=release-test@example.invalid",
		"merge", "--no-ff", "--no-gpg-sign", "-qm", "merge release documentation", "release-side",
	)
}

func (fixture *releaseFixture) isDirty() bool {
	fixture.t.Helper()
	return fixture.git("status", "--porcelain", "--untracked-files=all") != ""
}

func (fixture *releaseFixture) git(args ...string) string {
	fixture.t.Helper()
	return fixture.gitWithEnv(nil, args...)
}

func (fixture *releaseFixture) gitWithEnv(environment []string, args ...string) string {
	fixture.t.Helper()
	command := exec.Command("git", append([]string{"-C", fixture.root}, args...)...)
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		fixture.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func (fixture *releaseFixture) gitWithInput(input string, args ...string) string {
	fixture.t.Helper()
	command := exec.Command("git", append([]string{"-C", fixture.root}, args...)...)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		fixture.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func appendJSONField(raw []byte, field string) []byte {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		panic(fmt.Sprintf("not a JSON object: %q", trimmed))
	}
	result := append([]byte{}, trimmed[:len(trimmed)-1]...)
	result = append(result, ',')
	result = append(result, field...)
	result = append(result, '}', '\n')
	return result
}

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
