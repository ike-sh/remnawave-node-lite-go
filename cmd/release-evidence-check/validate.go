package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	expectedSchemaVersion = 1

	expectedReleaseVersion = "0.1.0"
	expectedReleaseTag     = "v0.1.0"
	expectedVersionOutput  = "remnawave-node-lite-go 0.1.0 (contract 2.8.0)"

	expectedOfficialNodeVersion = "2.8.0"
	expectedOfficialNodeCommit  = "596f015a5c8f876dc9a9d61b6cb78d35bd8e379b"
	expectedPanelVersion        = "2.8.1"

	expectedRWCoreVersion = "v26.6.27"
	expectedRWCoreCommit  = "45cf2898ab12e97a55dd8f1f3d78d903340bdc9e"
	expectedAMD64AssetSHA = "b3e5902d06d6282fe53cfa2fc426058b9aeaa429b2c812e20887cd47f26d08bf"
	expectedARM64AssetSHA = "13a251379bea366c2cf10363ad71e75734193d401f26f518bf0c25e5c8f8c931"

	expectedWholeMachineMemoryMiB = 512
	expectedServiceMemoryMaxMiB   = 448
	expectedCPUCount              = 1
	expectedDiskMiB               = 2048
	expectedUsers                 = 50000
	minimumSoakSeconds            = 86400
	maximumClockSkew              = 5 * time.Minute
	maximumAcceptanceFileBytes    = 1 << 20

	acceptanceDirectory    = "docs/development/acceptance/v0.1.0"
	manifestRepositoryPath = acceptanceDirectory + "/manifest.json"
)

var expectedAssetSHAByArch = map[string]string{
	"amd64": expectedAMD64AssetSHA,
	"arm64": expectedARM64AssetSHA,
}

var expectedAcceptancePaths = []string{
	manifestRepositoryPath,
	acceptanceDirectory + "/systemd.json",
	acceptanceDirectory + "/openrc.json",
	acceptanceDirectory + "/panel.json",
	acceptanceDirectory + "/resource-fault.json",
}

type validationResult struct {
	ReleaseTag       string
	CandidateCommit  string
	NodeSHA256ByArch map[string]string
}

type acceptanceManifest struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	ReleaseVersion  string              `json:"releaseVersion"`
	ReleaseTag      string              `json:"releaseTag"`
	CandidateCommit string              `json:"candidateCommit"`
	CandidateTree   string              `json:"candidateTree"`
	AcceptedAt      string              `json:"acceptedAt"`
	Decision        string              `json:"decision"`
	OfficialNode    officialNodeTarget  `json:"officialNode"`
	PanelTarget     panelTarget         `json:"panelTarget"`
	RWCore          rwCoreTarget        `json:"rwCore"`
	Policy          acceptancePolicy    `json:"policy"`
	Evidence        []evidenceReference `json:"evidence"`
	Risks           []releaseRisk       `json:"risks"`
}

type officialNodeTarget struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type panelTarget struct {
	Version string `json:"version"`
}

type rwCoreTarget struct {
	Version string           `json:"version"`
	Commit  string           `json:"commit"`
	SHA256  architectureSHAs `json:"sha256"`
}

type architectureSHAs struct {
	AMD64 string `json:"amd64"`
	ARM64 string `json:"arm64"`
}

type acceptancePolicy struct {
	WholeMachineMemoryMiB int   `json:"wholeMachineMemoryMiB"`
	ServiceMemoryMaxMiB   int   `json:"serviceMemoryMaxMiB"`
	CPUCount              int   `json:"cpuCount"`
	DiskMiB               int   `json:"diskMiB"`
	Users                 int   `json:"users"`
	Swap                  *bool `json:"swap"`
	SoakSeconds           int   `json:"soakSeconds"`
}

type evidenceReference struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Status string `json:"status"`
}

type releaseRisk struct {
	ID              string `json:"id"`
	Severity        string `json:"severity"`
	Status          string `json:"status"`
	Summary         string `json:"summary"`
	Mitigation      string `json:"mitigation"`
	ReleaseBlocking *bool  `json:"releaseBlocking"`
}

type evidenceCommon struct {
	SchemaVersion   int      `json:"schemaVersion"`
	Kind            string   `json:"kind"`
	CandidateCommit string   `json:"candidateCommit"`
	Status          string   `json:"status"`
	StartedAt       string   `json:"startedAt"`
	FinishedAt      string   `json:"finishedAt"`
	Command         []string `json:"command"`
}

type systemEvidence struct {
	evidenceCommon
	Environment machineEnvironment `json:"environment"`
	Node        nodeArtifact       `json:"node"`
	RWCore      rwCoreArtifact     `json:"rwCore"`
	Checks      systemChecks       `json:"checks"`
}

type panelEvidence struct {
	evidenceCommon
	PanelVersion       string                  `json:"panelVersion"`
	Targets            []string                `json:"targets"`
	Artifacts          []panelArtifactIdentity `json:"artifacts"`
	RoutesTotal        int                     `json:"routesTotal"`
	RoutesPassed       int                     `json:"routesPassed"`
	SemanticMismatches *int                    `json:"semanticMismatches"`
	Checks             panelChecks             `json:"checks"`
}

type panelArtifactIdentity struct {
	Target             string `json:"target"`
	Arch               string `json:"arch"`
	NodeBinarySHA256   string `json:"nodeBinarySha256"`
	RWCoreBinarySHA256 string `json:"rwCoreBinarySha256"`
}

type resourceFaultEvidence struct {
	evidenceCommon
	Environment machineEnvironment   `json:"environment"`
	Node        nodeArtifact         `json:"node"`
	RWCore      rwCoreArtifact       `json:"rwCore"`
	Metrics     resourceFaultMetrics `json:"metrics"`
	Checks      resourceFaultChecks  `json:"checks"`
}

type machineEnvironment struct {
	OSID      string `json:"osId"`
	OSVersion string `json:"osVersion"`
	Init      string `json:"init"`
	Arch      string `json:"arch"`
	Kernel    string `json:"kernel"`
	MemoryMiB int    `json:"memoryMiB"`
	CPUCount  int    `json:"cpuCount"`
	DiskMiB   int    `json:"diskMiB"`
}

type nodeArtifact struct {
	VersionOutput string `json:"versionOutput"`
	BinarySHA256  string `json:"binarySha256"`
}

type rwCoreArtifact struct {
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	AssetSHA256  string `json:"assetSha256"`
	BinarySHA256 string `json:"binarySha256"`
}

type systemChecks struct {
	FreshInstall              bool `json:"freshInstall"`
	RepeatInstall             bool `json:"repeatInstall"`
	StartStopRestart          bool `json:"startStopRestart"`
	SuccessfulUpgrade         bool `json:"successfulUpgrade"`
	FailedUpgradeRollback     bool `json:"failedUpgradeRollback"`
	RebootPanelResync         bool `json:"rebootPanelResync"`
	CapabilityBoundary        bool `json:"capabilityBoundary"`
	UninstallIsolation        bool `json:"uninstallIsolation"`
	NFTNamespace              bool `json:"nftNamespace"`
	SocketKillNamespace       bool `json:"socketKillNamespace"`
	RWCoreProcessGroupCleanup bool `json:"rwCoreProcessGroupCleanup"`
}

type panelChecks struct {
	NodeRegistration             bool `json:"nodeRegistration"`
	XrayLifecycle                bool `json:"xrayLifecycle"`
	Stats                        bool `json:"stats"`
	UserMutations                bool `json:"userMutations"`
	PluginSync                   bool `json:"pluginSync"`
	LifecyclePluginSerialization bool `json:"lifecyclePluginSerialization"`
}

type resourceFaultMetrics struct {
	Users              int  `json:"users"`
	SoakSeconds        int  `json:"soakSeconds"`
	PeakMemoryMiB      int  `json:"peakMemoryMiB"`
	OOMKills           *int `json:"oomKills"`
	ProjectDiskPeakMiB int  `json:"projectDiskPeakMiB"`
}

type resourceFaultChecks struct {
	NoSwap                  bool `json:"noSwap"`
	CoreKillRecovery        bool `json:"coreKillRecovery"`
	NodeRestartRecovery     bool `json:"nodeRestartRecovery"`
	PanelDisconnectRecovery bool `json:"panelDisconnectRecovery"`
	NFTFailureRetry         bool `json:"nftFailureRetry"`
	LogFaultStormBounded    bool `json:"logFaultStormBounded"`
	FailedUpgradeRollback   bool `json:"failedUpgradeRollback"`
}

type gitRepository struct {
	root string
}

type evidenceTiming struct {
	Started  time.Time
	Finished time.Time
}

type validatedEvidence struct {
	Kind     string
	Timing   evidenceTiming
	System   *systemEvidence
	Panel    *panelEvidence
	Resource *resourceFaultEvidence
}

func validateReleaseEvidence(ctx context.Context, repoDir, manifestPath, releaseTag string) (validationResult, error) {
	return validateReleaseEvidenceAt(ctx, repoDir, manifestPath, releaseTag, time.Now().UTC())
}

func validateReleaseEvidenceAt(
	ctx context.Context,
	repoDir, manifestPath, releaseTag string,
	now time.Time,
) (validationResult, error) {
	if releaseTag != expectedReleaseTag {
		return validationResult{}, fmt.Errorf("tag %q does not match %s", releaseTag, expectedReleaseTag)
	}

	repo, err := openGitRepository(ctx, repoDir)
	if err != nil {
		return validationResult{}, err
	}
	if err := repo.validateAcceptanceFileSet(ctx); err != nil {
		return validationResult{}, err
	}
	manifestAbs, manifestRel, err := repo.resolveRepositoryFile(manifestPath)
	if err != nil {
		return validationResult{}, fmt.Errorf("manifest: %w", err)
	}
	if manifestRel != manifestRepositoryPath {
		return validationResult{}, fmt.Errorf("manifest path is %q, want %q", manifestRel, manifestRepositoryPath)
	}
	manifestRaw, err := repo.requireTrackedRegularFile(ctx, manifestAbs, manifestRel)
	if err != nil {
		return validationResult{}, fmt.Errorf("manifest: %w", err)
	}
	var manifest acceptanceManifest
	if err := decodeStrictJSON(manifestRaw, &manifest); err != nil {
		return validationResult{}, fmt.Errorf("decode manifest: %w", err)
	}
	headTime, err := repo.commitTime(ctx, "HEAD")
	if err != nil {
		return validationResult{}, fmt.Errorf("read HEAD commit time: %w", err)
	}
	acceptedAt, err := validateManifest(&manifest, releaseTag, now, headTime)
	if err != nil {
		return validationResult{}, err
	}

	candidateTime, err := repo.validateCandidate(ctx, manifest.CandidateCommit, manifest.CandidateTree)
	if err != nil {
		return validationResult{}, err
	}
	if err := repo.validatePostCandidateChanges(ctx, manifest.CandidateCommit); err != nil {
		return validationResult{}, err
	}

	evidenceByKind := make(map[string]validatedEvidence, len(manifest.Evidence))
	latestEvidenceFinish := time.Time{}
	seenKinds := make(map[string]struct{}, len(manifest.Evidence))
	for _, reference := range manifest.Evidence {
		if _, duplicate := seenKinds[reference.Kind]; duplicate {
			return validationResult{}, fmt.Errorf("duplicate evidence kind %q", reference.Kind)
		}
		seenKinds[reference.Kind] = struct{}{}
		validated, err := repo.validateEvidence(
			ctx,
			reference,
			manifest.CandidateCommit,
			candidateTime,
			now,
			headTime,
			manifest.Policy,
		)
		if err != nil {
			return validationResult{}, fmt.Errorf("evidence %s: %w", reference.Kind, err)
		}
		evidenceByKind[reference.Kind] = validated
		if validated.Timing.Finished.After(latestEvidenceFinish) {
			latestEvidenceFinish = validated.Timing.Finished
		}
	}

	systemsByTarget := make(map[string]systemEvidence, 2)
	systemsByArch := make(map[string]systemEvidence, 2)
	architectures := make(map[string]struct{}, 2)
	for _, target := range []string{"systemd", "openrc"} {
		record := evidenceByKind[target]
		if record.System == nil {
			return validationResult{}, fmt.Errorf("missing validated %s evidence", target)
		}
		system := *record.System
		systemsByTarget[target] = system
		systemsByArch[system.Environment.Arch] = system
		architectures[system.Environment.Arch] = struct{}{}
	}
	if !sameStringSet(architectures, []string{"amd64", "arm64"}) {
		return validationResult{}, fmt.Errorf("system evidence architectures must cover amd64 and arm64, got %v", sortedKeys(architectures))
	}
	panel := evidenceByKind["panel"].Panel
	if panel == nil {
		return validationResult{}, errors.New("missing validated panel evidence")
	}
	if err := validatePanelArtifactClosure(*panel, systemsByTarget); err != nil {
		return validationResult{}, fmt.Errorf("Panel artifact identity: %w", err)
	}
	resource := evidenceByKind["resource-fault"].Resource
	if resource == nil {
		return validationResult{}, errors.New("missing validated resource-fault evidence")
	}
	if err := validateResourceArtifactClosure(*resource, systemsByArch); err != nil {
		return validationResult{}, fmt.Errorf("resource-fault artifact identity: %w", err)
	}
	if acceptedAt.Before(latestEvidenceFinish) {
		return validationResult{}, fmt.Errorf("acceptedAt %s is before evidence completion %s", acceptedAt.Format(time.RFC3339), latestEvidenceFinish.Format(time.RFC3339))
	}

	nodeSHAByArch := make(map[string]string, len(systemsByArch))
	for arch, system := range systemsByArch {
		nodeSHAByArch[arch] = system.Node.BinarySHA256
	}
	return validationResult{
		ReleaseTag:       releaseTag,
		CandidateCommit:  manifest.CandidateCommit,
		NodeSHA256ByArch: nodeSHAByArch,
	}, nil
}

func validateReleaseArtifacts(directory string, result validationResult) error {
	for _, arch := range []string{"amd64", "arm64"} {
		expectedSHA, ok := result.NodeSHA256ByArch[arch]
		if !ok {
			return fmt.Errorf("evidence has no Node SHA-256 for %s", arch)
		}
		path := filepath.Join(directory, "remnanode-lite_linux_"+arch)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("stat %s artifact: %w", arch, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s artifact %s is not a regular file", arch, path)
		}
		actualSHA, err := sha256File(path)
		if err != nil {
			return fmt.Errorf("hash %s artifact: %w", arch, err)
		}
		if actualSHA != expectedSHA {
			return fmt.Errorf("%s artifact SHA-256=%s, want %s", arch, actualSHA, expectedSHA)
		}
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func validateManifest(manifest *acceptanceManifest, releaseTag string, now, headTime time.Time) (time.Time, error) {
	if manifest.SchemaVersion != expectedSchemaVersion {
		return time.Time{}, fmt.Errorf("manifest schemaVersion=%d, want %d", manifest.SchemaVersion, expectedSchemaVersion)
	}
	if manifest.ReleaseVersion != expectedReleaseVersion || manifest.ReleaseTag != releaseTag {
		return time.Time{}, fmt.Errorf("manifest release=%q tag=%q, want %s and %s", manifest.ReleaseVersion, manifest.ReleaseTag, expectedReleaseVersion, releaseTag)
	}
	if !isLowerHex(manifest.CandidateCommit, 40) {
		return time.Time{}, errors.New("manifest candidateCommit must be 40 lowercase hexadecimal characters")
	}
	if !isLowerHex(manifest.CandidateTree, 40) {
		return time.Time{}, errors.New("manifest candidateTree must be 40 lowercase hexadecimal characters")
	}
	acceptedAt, err := parseRFC3339("manifest acceptedAt", manifest.AcceptedAt)
	if err != nil {
		return time.Time{}, err
	}
	if err := validateRecordedTime("manifest acceptedAt", acceptedAt, now, headTime); err != nil {
		return time.Time{}, err
	}
	if manifest.Decision != "pass" {
		return time.Time{}, fmt.Errorf("manifest decision=%q, want pass", manifest.Decision)
	}
	if manifest.OfficialNode.Version != expectedOfficialNodeVersion || manifest.OfficialNode.Commit != expectedOfficialNodeCommit {
		return time.Time{}, fmt.Errorf("official Node must be %s@%s", expectedOfficialNodeVersion, expectedOfficialNodeCommit)
	}
	if manifest.PanelTarget.Version != expectedPanelVersion {
		return time.Time{}, fmt.Errorf("Panel target=%q, want %s", manifest.PanelTarget.Version, expectedPanelVersion)
	}
	if manifest.RWCore.Version != expectedRWCoreVersion || manifest.RWCore.Commit != expectedRWCoreCommit {
		return time.Time{}, fmt.Errorf("rw-core must be %s@%s", expectedRWCoreVersion, expectedRWCoreCommit)
	}
	if manifest.RWCore.SHA256.AMD64 != expectedAMD64AssetSHA || manifest.RWCore.SHA256.ARM64 != expectedARM64AssetSHA {
		return time.Time{}, errors.New("rw-core architecture SHA-256 pins do not match the audited assets")
	}
	if err := validatePolicy(manifest.Policy); err != nil {
		return time.Time{}, err
	}
	if len(manifest.Evidence) != 4 {
		return time.Time{}, fmt.Errorf("manifest evidence count=%d, want 4", len(manifest.Evidence))
	}
	if manifest.Risks == nil {
		return time.Time{}, errors.New("manifest risks must be an array, use [] when there are no risks")
	}
	wantedKinds := map[string]struct{}{"systemd": {}, "openrc": {}, "panel": {}, "resource-fault": {}}
	for _, reference := range manifest.Evidence {
		if _, ok := wantedKinds[reference.Kind]; !ok {
			return time.Time{}, fmt.Errorf("unsupported evidence kind %q", reference.Kind)
		}
		if reference.Status != "pass" {
			return time.Time{}, fmt.Errorf("evidence %s status=%q, want pass", reference.Kind, reference.Status)
		}
		expectedPath := acceptanceDirectory + "/" + reference.Kind + ".json"
		if reference.Path != expectedPath {
			return time.Time{}, fmt.Errorf("evidence %s path=%q, want %q", reference.Kind, reference.Path, expectedPath)
		}
		if !isLowerHex(reference.SHA256, 64) {
			return time.Time{}, fmt.Errorf("evidence %s SHA-256 must be 64 lowercase hexadecimal characters", reference.Kind)
		}
	}
	if err := validateRisks(manifest.Risks); err != nil {
		return time.Time{}, err
	}
	return acceptedAt, nil
}

func validatePolicy(policy acceptancePolicy) error {
	if policy.WholeMachineMemoryMiB != expectedWholeMachineMemoryMiB ||
		policy.ServiceMemoryMaxMiB != expectedServiceMemoryMaxMiB ||
		policy.CPUCount != expectedCPUCount ||
		policy.DiskMiB != expectedDiskMiB ||
		policy.Users != expectedUsers || policy.Swap == nil || *policy.Swap || policy.SoakSeconds < minimumSoakSeconds {
		return fmt.Errorf(
			"acceptance policy must be 512 MiB whole-machine, 448 MiB service, 1 CPU, 2048 MiB disk, 50000 users, no swap, and soak >= %d seconds",
			minimumSoakSeconds,
		)
	}
	return nil
}

func validateRisks(risks []releaseRisk) error {
	seen := make(map[string]struct{}, len(risks))
	for _, risk := range risks {
		if strings.TrimSpace(risk.ID) == "" {
			return errors.New("risk id must not be empty")
		}
		if _, duplicate := seen[risk.ID]; duplicate {
			return fmt.Errorf("duplicate risk id %q", risk.ID)
		}
		seen[risk.ID] = struct{}{}
		if risk.Severity != "P1" && risk.Severity != "P2" && risk.Severity != "P3" {
			return fmt.Errorf("risk %s has unsupported severity %q", risk.ID, risk.Severity)
		}
		if risk.Status != "open" && risk.Status != "closed" {
			return fmt.Errorf("risk %s has unsupported status %q", risk.ID, risk.Status)
		}
		if strings.TrimSpace(risk.Summary) == "" || strings.TrimSpace(risk.Mitigation) == "" {
			return fmt.Errorf("risk %s summary and mitigation must not be empty", risk.ID)
		}
		if risk.ReleaseBlocking == nil {
			return fmt.Errorf("risk %s releaseBlocking is required", risk.ID)
		}
		if *risk.ReleaseBlocking {
			return fmt.Errorf("risk %s is release-blocking", risk.ID)
		}
		if (risk.Severity == "P1" || risk.Severity == "P2") && risk.Status != "closed" {
			return fmt.Errorf("risk %s is an unclosed %s", risk.ID, risk.Severity)
		}
	}
	return nil
}

func (repo gitRepository) validateEvidence(
	ctx context.Context,
	reference evidenceReference,
	candidateCommit string,
	candidateTime time.Time,
	now time.Time,
	headTime time.Time,
	policy acceptancePolicy,
) (validatedEvidence, error) {
	abs, rel, err := repo.resolveRepositoryFile(reference.Path)
	if err != nil {
		return validatedEvidence{}, err
	}
	if rel != reference.Path {
		return validatedEvidence{}, fmt.Errorf("path resolves to %q", rel)
	}
	raw, err := repo.requireTrackedRegularFile(ctx, abs, rel)
	if err != nil {
		return validatedEvidence{}, err
	}
	digest := sha256.Sum256(raw)
	if got := hex.EncodeToString(digest[:]); got != reference.SHA256 {
		return validatedEvidence{}, fmt.Errorf("SHA-256=%s, want %s", got, reference.SHA256)
	}

	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return validatedEvidence{}, fmt.Errorf("decode kind: %w", err)
	}
	if discriminator.Kind != reference.Kind {
		return validatedEvidence{}, fmt.Errorf("kind=%q, want %q", discriminator.Kind, reference.Kind)
	}

	switch reference.Kind {
	case "systemd", "openrc":
		var evidence systemEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return validatedEvidence{}, fmt.Errorf("decode: %w", err)
		}
		timing, err := validateCommonEvidence(evidence.evidenceCommon, reference.Kind, candidateCommit, candidateTime, now, headTime)
		if err != nil {
			return validatedEvidence{}, err
		}
		if err := validateSystemEvidence(evidence, reference.Kind, policy); err != nil {
			return validatedEvidence{}, err
		}
		return validatedEvidence{Kind: reference.Kind, Timing: timing, System: &evidence}, nil
	case "panel":
		var evidence panelEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return validatedEvidence{}, fmt.Errorf("decode: %w", err)
		}
		timing, err := validateCommonEvidence(evidence.evidenceCommon, reference.Kind, candidateCommit, candidateTime, now, headTime)
		if err != nil {
			return validatedEvidence{}, err
		}
		if err := validatePanelEvidence(evidence); err != nil {
			return validatedEvidence{}, err
		}
		return validatedEvidence{Kind: reference.Kind, Timing: timing, Panel: &evidence}, nil
	case "resource-fault":
		var evidence resourceFaultEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return validatedEvidence{}, fmt.Errorf("decode: %w", err)
		}
		timing, err := validateCommonEvidence(evidence.evidenceCommon, reference.Kind, candidateCommit, candidateTime, now, headTime)
		if err != nil {
			return validatedEvidence{}, err
		}
		if err := validateResourceFaultEvidence(evidence, policy, timing); err != nil {
			return validatedEvidence{}, err
		}
		return validatedEvidence{Kind: reference.Kind, Timing: timing, Resource: &evidence}, nil
	default:
		return validatedEvidence{}, fmt.Errorf("unsupported kind %q", reference.Kind)
	}
}

func validateCommonEvidence(
	common evidenceCommon,
	kind, candidateCommit string,
	candidateTime, now, headTime time.Time,
) (evidenceTiming, error) {
	if common.SchemaVersion != expectedSchemaVersion {
		return evidenceTiming{}, fmt.Errorf("schemaVersion=%d, want %d", common.SchemaVersion, expectedSchemaVersion)
	}
	if common.Kind != kind {
		return evidenceTiming{}, fmt.Errorf("kind=%q, want %q", common.Kind, kind)
	}
	if common.CandidateCommit != candidateCommit {
		return evidenceTiming{}, fmt.Errorf("candidateCommit=%q, want %s", common.CandidateCommit, candidateCommit)
	}
	if common.Status != "pass" {
		return evidenceTiming{}, fmt.Errorf("status=%q, want pass", common.Status)
	}
	started, err := parseRFC3339(kind+" startedAt", common.StartedAt)
	if err != nil {
		return evidenceTiming{}, err
	}
	if started.Before(candidateTime) {
		return evidenceTiming{}, fmt.Errorf("startedAt predates candidate commit: %s < %s", started.Format(time.RFC3339), candidateTime.Format(time.RFC3339))
	}
	if err := validateRecordedTime(kind+" startedAt", started, now, headTime); err != nil {
		return evidenceTiming{}, err
	}
	finished, err := parseRFC3339(kind+" finishedAt", common.FinishedAt)
	if err != nil {
		return evidenceTiming{}, err
	}
	if finished.Before(started) {
		return evidenceTiming{}, fmt.Errorf("finishedAt %s is before startedAt %s", common.FinishedAt, common.StartedAt)
	}
	if err := validateRecordedTime(kind+" finishedAt", finished, now, headTime); err != nil {
		return evidenceTiming{}, err
	}
	if len(common.Command) == 0 {
		return evidenceTiming{}, errors.New("command must not be empty")
	}
	for _, argument := range common.Command {
		if strings.TrimSpace(argument) == "" {
			return evidenceTiming{}, errors.New("command arguments must not be empty")
		}
	}
	return evidenceTiming{Started: started, Finished: finished}, nil
}

func validateSystemEvidence(evidence systemEvidence, kind string, policy acceptancePolicy) error {
	wantOSID, wantOSVersion, wantInit := "ubuntu", "24.04", "systemd"
	if kind == "openrc" {
		wantOSID, wantOSVersion, wantInit = "alpine", "3.22", "openrc"
	}
	if evidence.Environment.OSID != wantOSID || evidence.Environment.OSVersion != wantOSVersion || evidence.Environment.Init != wantInit {
		return fmt.Errorf("environment must be %s %s/%s", wantOSID, wantOSVersion, wantInit)
	}
	if err := validateMachineEnvironment(evidence.Environment, policy); err != nil {
		return err
	}
	if err := validateArtifacts(evidence.Environment.Arch, evidence.Node, evidence.RWCore); err != nil {
		return err
	}
	checks := evidence.Checks
	if !checks.RWCoreProcessGroupCleanup {
		return errors.New("system check rwCoreProcessGroupCleanup must be true")
	}
	if !checks.FreshInstall || !checks.RepeatInstall || !checks.StartStopRestart ||
		!checks.SuccessfulUpgrade || !checks.FailedUpgradeRollback || !checks.RebootPanelResync ||
		!checks.CapabilityBoundary || !checks.UninstallIsolation || !checks.NFTNamespace ||
		!checks.SocketKillNamespace {
		return errors.New("all system acceptance checks must pass")
	}
	return nil
}

func validatePanelEvidence(evidence panelEvidence) error {
	if evidence.PanelVersion != expectedPanelVersion {
		return fmt.Errorf("panelVersion=%q, want %s", evidence.PanelVersion, expectedPanelVersion)
	}
	if !exactStringSliceSet(evidence.Targets, []string{"systemd", "openrc"}) {
		return fmt.Errorf("panel targets must be systemd and openrc, got %v", evidence.Targets)
	}
	if len(evidence.Artifacts) != 2 {
		return fmt.Errorf("Panel artifacts count=%d, want 2", len(evidence.Artifacts))
	}
	seenTargets := make(map[string]struct{}, len(evidence.Artifacts))
	for _, artifact := range evidence.Artifacts {
		if artifact.Target != "systemd" && artifact.Target != "openrc" {
			return fmt.Errorf("Panel artifact has unsupported target %q", artifact.Target)
		}
		if _, duplicate := seenTargets[artifact.Target]; duplicate {
			return fmt.Errorf("Panel artifact target %q is duplicated", artifact.Target)
		}
		seenTargets[artifact.Target] = struct{}{}
		if _, ok := expectedAssetSHAByArch[artifact.Arch]; !ok {
			return fmt.Errorf("Panel artifact target %s has unsupported architecture %q", artifact.Target, artifact.Arch)
		}
		if !isLowerHex(artifact.NodeBinarySHA256, 64) || !isLowerHex(artifact.RWCoreBinarySHA256, 64) {
			return fmt.Errorf("Panel artifact target %s binary SHA-256 values must be 64 lowercase hexadecimal characters", artifact.Target)
		}
	}
	if evidence.SemanticMismatches == nil {
		return errors.New("Panel semanticMismatches is required")
	}
	if evidence.RoutesTotal != 26 || evidence.RoutesPassed != 26 || *evidence.SemanticMismatches != 0 {
		return fmt.Errorf(
			"Panel contract result must be 26 total, 26 passed, 0 semantic mismatches; got %d/%d/%d",
			evidence.RoutesTotal,
			evidence.RoutesPassed,
			*evidence.SemanticMismatches,
		)
	}
	checks := evidence.Checks
	if !checks.LifecyclePluginSerialization {
		return errors.New("Panel check lifecyclePluginSerialization must be true")
	}
	if !checks.NodeRegistration || !checks.XrayLifecycle || !checks.Stats || !checks.UserMutations || !checks.PluginSync {
		return errors.New("all Panel acceptance checks must pass")
	}
	return nil
}

func validateResourceFaultEvidence(evidence resourceFaultEvidence, policy acceptancePolicy, timing evidenceTiming) error {
	if strings.TrimSpace(evidence.Environment.OSID) == "" || strings.TrimSpace(evidence.Environment.OSVersion) == "" || strings.TrimSpace(evidence.Environment.Init) == "" {
		return errors.New("resource environment osId, osVersion, and init must not be empty")
	}
	if err := validateMachineEnvironment(evidence.Environment, policy); err != nil {
		return err
	}
	if err := validateArtifacts(evidence.Environment.Arch, evidence.Node, evidence.RWCore); err != nil {
		return err
	}
	metrics := evidence.Metrics
	if metrics.OOMKills == nil {
		return errors.New("resource oomKills is required")
	}
	if metrics.Users != policy.Users || metrics.SoakSeconds < policy.SoakSeconds ||
		metrics.PeakMemoryMiB <= 0 || metrics.PeakMemoryMiB > policy.ServiceMemoryMaxMiB ||
		*metrics.OOMKills != 0 || metrics.ProjectDiskPeakMiB <= 0 || metrics.ProjectDiskPeakMiB > policy.DiskMiB {
		return fmt.Errorf(
			"resource metrics must have %d users, soak >= %d, peak memory 1..%d MiB, zero OOM kills, and project disk 1..%d MiB",
			policy.Users,
			policy.SoakSeconds,
			policy.ServiceMemoryMaxMiB,
			policy.DiskMiB,
		)
	}
	elapsedSeconds := int64(timing.Finished.Sub(timing.Started) / time.Second)
	if elapsedSeconds < int64(metrics.SoakSeconds) || elapsedSeconds < minimumSoakSeconds {
		return fmt.Errorf(
			"resource evidence wall-clock duration=%d seconds, want at least soakSeconds=%d and %d seconds",
			elapsedSeconds,
			metrics.SoakSeconds,
			minimumSoakSeconds,
		)
	}
	checks := evidence.Checks
	if !checks.NoSwap || !checks.CoreKillRecovery || !checks.NodeRestartRecovery ||
		!checks.PanelDisconnectRecovery || !checks.NFTFailureRetry || !checks.LogFaultStormBounded ||
		!checks.FailedUpgradeRollback {
		return errors.New("all resource and fault-recovery checks must pass")
	}
	return nil
}

func validatePanelArtifactClosure(evidence panelEvidence, systemsByTarget map[string]systemEvidence) error {
	for _, artifact := range evidence.Artifacts {
		system, ok := systemsByTarget[artifact.Target]
		if !ok {
			return fmt.Errorf("target %q has no matching system evidence", artifact.Target)
		}
		if artifact.Arch != system.Environment.Arch {
			return fmt.Errorf("target %s arch=%q, want %q", artifact.Target, artifact.Arch, system.Environment.Arch)
		}
		if artifact.NodeBinarySHA256 != system.Node.BinarySHA256 {
			return fmt.Errorf("target %s nodeBinarySha256 does not match system evidence", artifact.Target)
		}
		if artifact.RWCoreBinarySHA256 != system.RWCore.BinarySHA256 {
			return fmt.Errorf("target %s rwCoreBinarySha256 does not match system evidence", artifact.Target)
		}
	}
	return nil
}

func validateResourceArtifactClosure(evidence resourceFaultEvidence, systemsByArch map[string]systemEvidence) error {
	system, ok := systemsByArch[evidence.Environment.Arch]
	if !ok {
		return fmt.Errorf("arch %q has no matching system evidence", evidence.Environment.Arch)
	}
	if evidence.Node.BinarySHA256 != system.Node.BinarySHA256 {
		return fmt.Errorf("node binarySha256 for %s does not match system evidence", evidence.Environment.Arch)
	}
	if evidence.RWCore.BinarySHA256 != system.RWCore.BinarySHA256 {
		return fmt.Errorf("rw-core binarySha256 for %s does not match system evidence", evidence.Environment.Arch)
	}
	return nil
}

func validateMachineEnvironment(environment machineEnvironment, policy acceptancePolicy) error {
	if _, ok := expectedAssetSHAByArch[environment.Arch]; !ok {
		return fmt.Errorf("unsupported architecture %q", environment.Arch)
	}
	if strings.TrimSpace(environment.Kernel) == "" {
		return errors.New("environment kernel must not be empty")
	}
	if environment.MemoryMiB != policy.WholeMachineMemoryMiB || environment.CPUCount != policy.CPUCount || environment.DiskMiB != policy.DiskMiB {
		return fmt.Errorf("environment limits must be %d MiB, %d CPU, and %d MiB disk", policy.WholeMachineMemoryMiB, policy.CPUCount, policy.DiskMiB)
	}
	return nil
}

func validateArtifacts(arch string, node nodeArtifact, core rwCoreArtifact) error {
	if node.VersionOutput != expectedVersionOutput {
		return fmt.Errorf("node versionOutput=%q, want %q", node.VersionOutput, expectedVersionOutput)
	}
	if !isLowerHex(node.BinarySHA256, 64) {
		return errors.New("node binarySha256 must be 64 lowercase hexadecimal characters")
	}
	wantAssetSHA, ok := expectedAssetSHAByArch[arch]
	if !ok {
		return fmt.Errorf("unsupported architecture %q", arch)
	}
	if core.Version != expectedRWCoreVersion || core.Commit != expectedRWCoreCommit {
		return fmt.Errorf("rw-core must be %s@%s", expectedRWCoreVersion, expectedRWCoreCommit)
	}
	if core.AssetSHA256 != wantAssetSHA {
		return fmt.Errorf("rw-core assetSha256=%q, want %s for %s", core.AssetSHA256, wantAssetSHA, arch)
	}
	if !isLowerHex(core.BinarySHA256, 64) {
		return errors.New("rw-core binarySha256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func openGitRepository(ctx context.Context, repoDir string) (gitRepository, error) {
	if repoDir == "" {
		repoDir = "."
	}
	command := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--show-toplevel")
	output, err := command.CombinedOutput()
	if err != nil {
		return gitRepository{}, fmt.Errorf("find Git repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return gitRepository{}, errors.New("find Git repository: empty root")
	}
	return gitRepository{root: root}, nil
}

func (repo gitRepository) validateAcceptanceFileSet(ctx context.Context) error {
	raw, err := repo.gitBytes(ctx, "ls-tree", "-r", "-z", "--name-only", "HEAD", "--", acceptanceDirectory)
	if err != nil {
		return fmt.Errorf("list acceptance files in HEAD: %w", err)
	}
	actual := splitNUL(raw)
	actualSet := make(map[string]struct{}, len(actual))
	for _, path := range actual {
		actualSet[path] = struct{}{}
	}
	if !sameStringSet(actualSet, expectedAcceptancePaths) {
		sort.Strings(actual)
		return fmt.Errorf("acceptance files in HEAD must be exactly %v, got %v", expectedAcceptancePaths, actual)
	}
	return nil
}

func (repo gitRepository) validateCandidate(ctx context.Context, candidateCommit, candidateTree string) (time.Time, error) {
	commit, err := repo.gitOutput(ctx, "rev-parse", "--verify", candidateCommit+"^{commit}")
	if err != nil {
		return time.Time{}, fmt.Errorf("verify candidate commit: %w", err)
	}
	if commit != candidateCommit {
		return time.Time{}, fmt.Errorf("candidate commit resolves to %s, want %s", commit, candidateCommit)
	}
	tree, err := repo.gitOutput(ctx, "rev-parse", candidateCommit+"^{tree}")
	if err != nil {
		return time.Time{}, fmt.Errorf("read candidate tree: %w", err)
	}
	if tree != candidateTree {
		return time.Time{}, fmt.Errorf("candidate tree=%s, want %s", tree, candidateTree)
	}
	if _, err := repo.gitOutput(ctx, "merge-base", "--is-ancestor", candidateCommit, "HEAD"); err != nil {
		return time.Time{}, fmt.Errorf("candidate %s is not an ancestor of HEAD: %w", candidateCommit, err)
	}
	committedAt, err := repo.commitTime(ctx, candidateCommit)
	if err != nil {
		return time.Time{}, fmt.Errorf("read candidate commit time: %w", err)
	}
	return committedAt, nil
}

func (repo gitRepository) validatePostCandidateChanges(ctx context.Context, candidateCommit string) error {
	commitsRaw, err := repo.gitOutput(ctx, "rev-list", "--reverse", candidateCommit+"..HEAD")
	if err != nil {
		return fmt.Errorf("list post-candidate commits: %w", err)
	}
	for _, commit := range strings.Fields(commitsRaw) {
		parentsRaw, err := repo.gitOutput(ctx, "show", "-s", "--format=%P", commit)
		if err != nil {
			return fmt.Errorf("read parents for post-candidate commit %s: %w", commit, err)
		}
		parents := strings.Fields(parentsRaw)
		if len(parents) != 1 {
			return fmt.Errorf("post-candidate commit %s is a merge with %d parents; merges are not allowed during release finalization", commit, len(parents))
		}
		changedRaw, err := repo.gitBytes(
			ctx,
			"diff",
			"--no-renames",
			"--name-only",
			"-z",
			parents[0],
			commit,
			"--",
		)
		if err != nil {
			return fmt.Errorf("list changes for post-candidate commit %s: %w", commit, err)
		}
		for _, path := range splitNUL(changedRaw) {
			if !isAllowedPostCandidatePath(path) {
				return fmt.Errorf("post-candidate commit %s changes %q outside the release-finalization allowlist", commit, path)
			}
		}
	}
	return nil
}

func isAllowedPostCandidatePath(path string) bool {
	switch path {
	case "README.md",
		"docs/CHANGELOG.md",
		"docs/development/roadmap.md",
		"docs/releases/v0.1.0.md",
		manifestRepositoryPath,
		acceptanceDirectory + "/systemd.json",
		acceptanceDirectory + "/openrc.json",
		acceptanceDirectory + "/panel.json",
		acceptanceDirectory + "/resource-fault.json":
		return true
	default:
		return false
	}
}

func (repo gitRepository) resolveRepositoryFile(path string) (string, string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(repo.root, filepath.FromSlash(path))
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(repo.root, abs)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("path %q is outside repository", path)
	}
	return abs, rel, nil
}

func (repo gitRepository) requireTrackedRegularFile(ctx context.Context, abs, rel string) ([]byte, error) {
	if err := repo.requireRegularPathWithoutSymlinks(abs, rel); err != nil {
		return nil, err
	}

	headRaw, err := repo.gitBytes(ctx, "ls-tree", "-z", "HEAD", "--", rel)
	if err != nil {
		return nil, fmt.Errorf("read %s from HEAD tree: %w", rel, err)
	}
	headEntry, err := parseHEADTreeEntry(headRaw, rel)
	if err != nil {
		return nil, err
	}
	if headEntry.Mode != "100644" {
		return nil, fmt.Errorf("%s has HEAD mode %s, want 100644", rel, headEntry.Mode)
	}
	if headEntry.Type != "blob" {
		return nil, fmt.Errorf("%s has HEAD type %s, want blob", rel, headEntry.Type)
	}

	indexRaw, err := repo.gitBytes(ctx, "ls-files", "--stage", "-z", "--", rel)
	if err != nil {
		return nil, fmt.Errorf("read %s from Git index: %w", rel, err)
	}
	indexEntry, err := parseIndexEntry(indexRaw, rel)
	if err != nil {
		return nil, err
	}
	if indexEntry.Stage != "0" {
		return nil, fmt.Errorf("%s has non-stage-0 index entry %s", rel, indexEntry.Stage)
	}
	if strings.Trim(indexEntry.Object, "0") == "" {
		return nil, fmt.Errorf("%s is intent-to-add in the Git index", rel)
	}
	if indexEntry.Mode != headEntry.Mode || indexEntry.Object != headEntry.Object {
		return nil, fmt.Errorf("%s index entry does not match HEAD", rel)
	}

	worktreeInfo, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s worktree file: %w", rel, err)
	}
	if worktreeInfo.Mode().Perm()&0o111 != 0 {
		return nil, fmt.Errorf("%s worktree mode is executable, want non-executable JSON", rel)
	}
	if worktreeInfo.Size() > maximumAcceptanceFileBytes {
		return nil, fmt.Errorf("%s worktree size %d exceeds %d bytes", rel, worktreeInfo.Size(), maximumAcceptanceFileBytes)
	}
	headSizeRaw, err := repo.gitOutput(ctx, "cat-file", "-s", headEntry.Object)
	if err != nil {
		return nil, fmt.Errorf("read %s HEAD blob size: %w", rel, err)
	}
	headSize, err := strconv.ParseInt(headSizeRaw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse %s HEAD blob size %q: %w", rel, headSizeRaw, err)
	}
	if headSize > maximumAcceptanceFileBytes {
		return nil, fmt.Errorf("%s HEAD blob size %d exceeds %d bytes", rel, headSize, maximumAcceptanceFileBytes)
	}

	worktreeRaw, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s from worktree: %w", rel, err)
	}
	headBlob, err := repo.gitBytes(ctx, "cat-file", "blob", headEntry.Object)
	if err != nil {
		return nil, fmt.Errorf("read %s HEAD blob: %w", rel, err)
	}
	if !bytes.Equal(worktreeRaw, headBlob) {
		return nil, fmt.Errorf("%s worktree bytes do not match HEAD blob", rel)
	}
	return worktreeRaw, nil
}

type headTreeEntry struct {
	Mode   string
	Type   string
	Object string
}

type indexEntry struct {
	Mode   string
	Object string
	Stage  string
}

func (repo gitRepository) requireRegularPathWithoutSymlinks(abs, rel string) error {
	expectedAbs, err := filepath.Abs(filepath.Join(repo.root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if filepath.Clean(abs) != expectedAbs {
		return fmt.Errorf("path %q resolves to unexpected location %q", rel, abs)
	}

	current := repo.root
	parts := strings.Split(filepath.FromSlash(rel), string(filepath.Separator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s contains symlink component %s", rel, filepath.ToSlash(current))
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("%s ancestor %s is not a directory", rel, filepath.ToSlash(current))
		}
		if index == len(parts)-1 && !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
	}
	return nil
}

func parseHEADTreeEntry(raw []byte, rel string) (headTreeEntry, error) {
	entries := splitNUL(raw)
	if len(entries) != 1 {
		return headTreeEntry{}, fmt.Errorf("%s must have exactly one entry in HEAD, got %d", rel, len(entries))
	}
	metadata, path, ok := strings.Cut(entries[0], "\t")
	if !ok || path != rel {
		return headTreeEntry{}, fmt.Errorf("malformed HEAD tree entry for %s", rel)
	}
	fields := strings.Fields(metadata)
	if len(fields) != 3 {
		return headTreeEntry{}, fmt.Errorf("malformed HEAD tree metadata for %s", rel)
	}
	return headTreeEntry{Mode: fields[0], Type: fields[1], Object: fields[2]}, nil
}

func parseIndexEntry(raw []byte, rel string) (indexEntry, error) {
	entries := splitNUL(raw)
	if len(entries) != 1 {
		return indexEntry{}, fmt.Errorf("%s must have exactly one Git index entry, got %d", rel, len(entries))
	}
	metadata, path, ok := strings.Cut(entries[0], "\t")
	if !ok || path != rel {
		return indexEntry{}, fmt.Errorf("malformed Git index entry for %s", rel)
	}
	fields := strings.Fields(metadata)
	if len(fields) != 3 {
		return indexEntry{}, fmt.Errorf("malformed Git index metadata for %s", rel)
	}
	return indexEntry{Mode: fields[0], Object: fields[1], Stage: fields[2]}, nil
}

func (repo gitRepository) commitTime(ctx context.Context, revision string) (time.Time, error) {
	raw, err := repo.gitOutput(ctx, "show", "-s", "--format=%cI", revision)
	if err != nil {
		return time.Time{}, err
	}
	return parseRFC3339(revision+" commit time", raw)
}

func (repo gitRepository) gitBytes(ctx context.Context, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repo.root}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", err, detail)
	}
	return output, nil
}

func (repo gitRepository) gitOutput(ctx context.Context, args ...string) (string, error) {
	output, err := repo.gitBytes(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func decodeStrictJSON(raw []byte, target any) error {
	if err := rejectDuplicateJSONFields(raw); err != nil {
		return err
	}
	if err := validateExactJSONKeys(raw, target); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func validateExactJSONKeys(raw []byte, target any) error {
	targetType := reflect.TypeOf(target)
	if targetType == nil || targetType.Kind() != reflect.Pointer {
		return errors.New("strict JSON target must be a non-nil pointer")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return validateExactJSONValue(value, targetType.Elem(), "$")
}

func validateExactJSONValue(value any, targetType reflect.Type, path string) error {
	for targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	if value == nil {
		return nil
	}

	switch targetType.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		fields := exactJSONStructFields(targetType)
		for key, member := range object {
			fieldType, ok := fields[key]
			if !ok {
				return fmt.Errorf("unknown field %q at %s", key, path)
			}
			if err := validateExactJSONValue(member, fieldType, path+"."+key); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		array, ok := value.([]any)
		if !ok {
			return nil
		}
		for index, member := range array {
			if err := validateExactJSONValue(member, targetType.Elem(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		for key, member := range object {
			if err := validateExactJSONValue(member, targetType.Elem(), path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func exactJSONStructFields(targetType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for index := 0; index < targetType.NumField(); index++ {
		field := targetType.Field(index)
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			embeddedType := field.Type
			for embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			if embeddedType.Kind() == reflect.Struct {
				for embeddedName, embeddedFieldType := range exactJSONStructFields(embeddedType) {
					fields[embeddedName] = embeddedFieldType
				}
				continue
			}
		}
		if field.PkgPath != "" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func rejectDuplicateJSONFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := scanJSONValue(decoder, "$"); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("trailing JSON: %w", err)
		}
		return fmt.Errorf("multiple JSON values are not allowed, found %v", token)
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object at %s ended with %v", path, closing)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := scanJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array at %s ended with %v", path, closing)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}

func parseRFC3339(field, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return parsed, nil
}

func validateRecordedTime(field string, value, now, headTime time.Time) error {
	if value.After(now.Add(maximumClockSkew)) {
		return fmt.Errorf("%s %s is later than current time plus %s", field, value.Format(time.RFC3339), maximumClockSkew)
	}
	if value.After(headTime.Add(maximumClockSkew)) {
		return fmt.Errorf("%s %s is later than HEAD commit time plus %s", field, value.Format(time.RFC3339), maximumClockSkew)
	}
	return nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func exactStringSliceSet(values, expected []string) bool {
	if len(values) != len(expected) {
		return false
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, duplicate := set[value]; duplicate {
			return false
		}
		set[value] = struct{}{}
	}
	return sameStringSet(set, expected)
}

func sameStringSet(values map[string]struct{}, expected []string) bool {
	if len(values) != len(expected) {
		return false
	}
	for _, value := range expected {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func splitNUL(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	parts := bytes.Split(raw, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			result = append(result, string(part))
		}
	}
	return result
}
