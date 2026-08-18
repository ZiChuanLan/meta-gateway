package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/store"
)

const (
	marketArtifactMaxBytes = 128 << 20 // 128 MiB per plugin package
	marketArchiveMaxFiles  = 512
	marketArchiveMaxEntry  = 64 << 20
)

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	APIURL             string `json:"url"`
}

func (s *Service) installPackagedMarket(ctx context.Context, entry MarketEntry, requestedVersion string) (*store.PluginRecord, error) {
	version := normalizeMarketVersion(requestedVersion)
	plan := entry.Install
	if version != "" && entry.InstallType() == marketInstallDirect {
		var ok bool
		plan, ok = directMarketVersion(entry, version)
		if !ok {
			return nil, fmt.Errorf("plugin_version_not_found")
		}
	}
	if version == "" && entry.InstallType() == marketInstallDirect {
		version = normalizeMarketVersion(entry.Version)
	}
	artifact, checksum, resolvedVersion, err := s.resolveMarketArtifact(ctx, entry, version, plan)
	if err != nil {
		return nil, err
	}
	if resolvedVersion != "" {
		version = resolvedVersion
	}
	archive, err := s.downloadMarketArtifact(ctx, artifact.URL, artifact.Size)
	if err != nil {
		return nil, err
	}
	if expected := strings.TrimSpace(checksum); expected != "" {
		sum := sha256.Sum256(archive)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
			return nil, fmt.Errorf("plugin_artifact_checksum_mismatch")
		}
	}
	if artifact.SHA256 != "" {
		sum := sha256.Sum256(archive)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(artifact.SHA256)) {
			return nil, fmt.Errorf("plugin_artifact_checksum_mismatch")
		}
	}

	pluginDir, err := s.safePluginDir(entry.ID)
	if err != nil {
		return nil, err
	}
	previousRecord, err := s.store.Get(entry.ID)
	if err != nil {
		return nil, err
	}
	if previousRecord != nil && previousRecord.Status == StatusInstalled {
		if err := s.stopManagedPlugin(context.Background(), entry.ID); err != nil {
			return nil, err
		}
	}
	stageDir := pluginDir + ".staging"
	_ = os.RemoveAll(stageDir)
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return nil, fmt.Errorf("plugin_stage_create: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stageDir)
		}
	}()
	manifest, err := extractPluginArchive(archive, stageDir, entry.ID)
	if err != nil {
		return nil, err
	}
	if manifest.Version == "" {
		manifest.Version = firstNonEmpty(version, "1.0.0")
	}
	if version != "" && normalizeMarketVersion(manifest.Version) != version {
		return nil, fmt.Errorf("plugin_manifest_version_mismatch")
	}
	if manifest.Name == "" {
		manifest.Name = entry.Name
	}
	if manifest.ID != entry.ID {
		return nil, fmt.Errorf("plugin_manifest_id_mismatch")
	}
	if err := validatePluginManifestForPackage(&manifest, stageDir); err != nil {
		return nil, err
	}
	apiKey, err := newPluginKey()
	if err != nil {
		return nil, fmt.Errorf("plugin_key_generate: %w", err)
	}
	spec := &SidecarSpec{
		PagePath:        strings.TrimPrefix(strings.TrimSpace(manifest.PagePath), "/"),
		HealthPath:      strings.TrimPrefix(strings.TrimSpace(manifest.HealthPath), "/"),
		APIPrefix:       normalizeAPIPrefix(manifest.APIPrefix),
		ChannelPath:     normalizeChannelPath(manifest.ChannelPath),
		APIKey:          apiKey,
		Managed:         true,
		Entrypoint:      manifest.Entrypoint,
		RunArgs:         append([]string(nil), manifest.RunArgs...),
		MarketSourceID:  entry.Source.ID,
		MarketSourceURL: entry.Source.URL,
		InstallType:     entry.InstallType(),
		ArtifactSHA256:  firstNonEmpty(artifact.SHA256, checksum),
	}
	if spec.PagePath == "" {
		spec.PagePath = "/"
	}
	if spec.HealthPath == "" {
		spec.HealthPath = "healthz"
	}
	if err := validateSidecarSpecPaths(spec); err != nil {
		return nil, err
	}
	hostManifest := Manifest{
		ID:           manifest.ID,
		Version:      manifest.Version,
		Name:         manifest.Name,
		Description:  manifest.Description,
		Capabilities: manifest.Capabilities,
		Permissions:  manifest.Permissions,
		ConfigFields: manifest.ConfigFields,
		Admin: map[string]string{
			"route":     "/" + entry.ID,
			"nav_label": manifest.Name,
		},
		Sidecar: spec,
	}
	manifestBody, err := json.MarshalIndent(hostManifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("plugin_manifest_encode: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stageDir, ".meta-gateway.json"), manifestBody, 0o600); err != nil {
		return nil, fmt.Errorf("plugin_manifest_write: %w", err)
	}

	backupDir, err := replacePluginDir(pluginDir, stageDir)
	if err != nil {
		return nil, err
	}
	cleanup = false
	committed := false
	defer func() {
		if committed {
			if backupDir != "" {
				_ = os.RemoveAll(backupDir)
			}
			return
		}
		_ = s.stopManagedPlugin(context.Background(), entry.ID)
		_ = os.RemoveAll(pluginDir)
		if backupDir != "" {
			_ = os.Rename(backupDir, pluginDir)
		}
		if previousRecord == nil {
			_ = s.store.Delete(entry.ID)
		} else {
			_ = s.store.Upsert(previousRecord)
		}
		_ = s.reloadEnabled()
		if previousRecord != nil && previousRecord.Enabled && previousRecord.MetaJSON != "" {
			var previousManifest Manifest
			if json.Unmarshal([]byte(previousRecord.MetaJSON), &previousManifest) == nil && previousManifest.Sidecar != nil && previousManifest.Sidecar.Managed {
				if s.startManagedPlugin(context.Background(), entry.ID, previousManifest.Sidecar) == nil {
					_ = s.persistManagedSpec(entry.ID, previousManifest.Sidecar)
				}
			}
		}
	}()
	now := time.Now().UTC()
	sum := sha256.Sum256(archive)
	record := &store.PluginRecord{
		ID:          entry.ID,
		Version:     normalizeMarketVersion(manifest.Version),
		Status:      StatusInstalled,
		Enabled:     false,
		Source:      "market:" + entry.Source.ID,
		Checksum:    hex.EncodeToString(sum[:]),
		InstalledAt: &now,
		MetaJSON:    string(manifestBody),
	}
	if old, err := s.store.Get(entry.ID); err != nil {
		return nil, err
	} else if old != nil && old.Enabled {
		record.Enabled = true
		record.EnabledAt = old.EnabledAt
	}
	if err := s.store.Upsert(record); err != nil {
		return nil, err
	}
	if _, err := s.Enable(entry.ID); err != nil {
		return nil, err
	}
	committed = true
	return s.store.Get(entry.ID)
}

func (s *Service) resolveMarketArtifact(ctx context.Context, entry MarketEntry, version string, plan MarketInstallPlan) (MarketArtifact, string, string, error) {
	if entry.InstallType() == marketInstallDirect {
		artifact, ok := selectMarketArtifact(plan.Artifacts, runtime.GOOS, runtime.GOARCH)
		if !ok {
			return MarketArtifact{}, "", "", fmt.Errorf("plugin_artifact_unavailable")
		}
		return artifact, artifact.SHA256, normalizeMarketVersion(entry.Version), nil
	}
	owner, repo, err := githubRepositoryParts(entry.Repository)
	if err != nil {
		return MarketArtifact{}, "", "", err
	}
	releaseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", url.PathEscape(owner), url.PathEscape(repo))
	if version != "" {
		releaseURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/v%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(version))
	}
	var release releaseInfo
	if err := s.fetchJSON(ctx, releaseURL, &release, 2<<20); err != nil {
		return MarketArtifact{}, "", "", fmt.Errorf("plugin_release_fetch: %w", err)
	}
	version = normalizeMarketVersion(release.TagName)
	if !validMarketVersion(version) {
		return MarketArtifact{}, "", "", fmt.Errorf("plugin_release_version_invalid")
	}
	pattern := strings.TrimSpace(plan.ArtifactPattern)
	if pattern == "" {
		pattern = "{id}_{version}_{goos}_{goarch}.zip"
	}
	wantName := strings.NewReplacer(
		"{id}", entry.ID,
		"{version}", version,
		"{goos}", normalizeMarketGOOS(runtime.GOOS),
		"{goarch}", normalizeMarketGOARCH(runtime.GOARCH),
	).Replace(pattern)
	checksumName := firstNonEmpty(plan.ChecksumAsset, "checksums.txt")
	var archive releaseAsset
	var checksumAsset releaseAsset
	for _, asset := range release.Assets {
		if asset.Name == wantName {
			archive = asset
		}
		if asset.Name == checksumName {
			checksumAsset = asset
		}
	}
	if archive.Name == "" || checksumAsset.Name == "" {
		return MarketArtifact{}, "", "", fmt.Errorf("plugin_release_asset_unavailable")
	}
	checksumData, err := s.downloadMarketAsset(ctx, checksumAsset)
	if err != nil {
		return MarketArtifact{}, "", "", err
	}
	checksums, err := parseMarketChecksums(checksumData)
	if err != nil {
		return MarketArtifact{}, "", "", err
	}
	checksum := checksums[archive.Name]
	if checksum == "" {
		return MarketArtifact{}, "", "", fmt.Errorf("plugin_release_checksum_unavailable")
	}
	return MarketArtifact{GOOS: normalizeMarketGOOS(runtime.GOOS), GOARCH: normalizeMarketGOARCH(runtime.GOARCH), URL: firstNonEmpty(archive.BrowserDownloadURL, archive.APIURL), SHA256: checksum}, checksum, version, nil
}

func directMarketVersion(entry MarketEntry, version string) (MarketInstallPlan, bool) {
	if normalizeMarketVersion(entry.Version) == version {
		return entry.Install, true
	}
	for _, candidate := range entry.Versions {
		if normalizeMarketVersion(candidate.Version) == version {
			return candidate.Install, true
		}
	}
	return MarketInstallPlan{}, false
}

func selectMarketArtifact(artifacts []MarketArtifact, goos, goarch string) (MarketArtifact, bool) {
	goos = normalizeMarketGOOS(goos)
	goarch = normalizeMarketGOARCH(goarch)
	for _, artifact := range artifacts {
		if normalizeMarketGOOS(artifact.GOOS) == goos && normalizeMarketGOARCH(artifact.GOARCH) == goarch {
			return artifact, true
		}
	}
	return MarketArtifact{}, false
}

func (s *Service) downloadMarketArtifact(ctx context.Context, rawURL string, declaredSize int64) ([]byte, error) {
	if err := validateMarketURL(rawURL, "artifact"); err != nil {
		return nil, err
	}
	maxSize := int64(marketArtifactMaxBytes)
	if declaredSize > 0 && declaredSize < maxSize {
		maxSize = declaredSize
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("plugin_artifact_download: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("plugin_artifact_download_status_%d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("plugin_artifact_read: %w", err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("plugin_artifact_too_large")
	}
	if declaredSize > 0 && int64(len(data)) != declaredSize {
		return nil, fmt.Errorf("plugin_artifact_size_mismatch")
	}
	return data, nil
}

func (s *Service) downloadMarketAsset(ctx context.Context, asset releaseAsset) ([]byte, error) {
	return s.downloadMarketArtifact(ctx, firstNonEmpty(asset.BrowserDownloadURL, asset.APIURL), 0)
}

func (s *Service) fetchJSON(ctx context.Context, rawURL string, dest any, maxBytes int64) error {
	if err := validateMarketURL(rawURL, "release"); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "meta-gateway-plugin-store")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return errors.New("response too large")
	}
	return json.Unmarshal(data, dest)
}

func githubRepositoryParts(repository string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(repository))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasSuffix(parts[1], ".git") {
		return "", "", fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	return parts[0], parts[1], nil
}

func parseMarketChecksums(data []byte) (map[string]string, error) {
	result := map[string]string{}
	for lineNo, raw := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) < 2 || len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("plugin_release_checksum_invalid_line_%d", lineNo+1)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("plugin_release_checksum_invalid_line_%d", lineNo+1)
		}
		result[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return result, nil
}

func extractPluginArchive(data []byte, destination, expectedID string) (SidecarManifest, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return SidecarManifest{}, fmt.Errorf("plugin_archive_invalid: %w", err)
	}
	if len(reader.File) > marketArchiveMaxFiles {
		return SidecarManifest{}, fmt.Errorf("plugin_archive_too_many_files")
	}
	var manifest SidecarManifest
	var manifestFound bool
	for _, file := range reader.File {
		cleanName, err := cleanArchiveName(file.Name)
		if err != nil {
			return SidecarManifest{}, err
		}
		if cleanName == "" {
			continue
		}
		if file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return SidecarManifest{}, fmt.Errorf("plugin_archive_symlink_rejected")
		}
		if file.UncompressedSize64 > marketArchiveMaxEntry {
			return SidecarManifest{}, fmt.Errorf("plugin_archive_entry_too_large")
		}
		target := filepath.Join(destination, filepath.FromSlash(cleanName))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return SidecarManifest{}, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return SidecarManifest{}, err
		}
		input, err := file.Open()
		if err != nil {
			return SidecarManifest{}, err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if err != nil {
			_ = input.Close()
			return SidecarManifest{}, err
		}
		copied, copyErr := io.Copy(output, io.LimitReader(input, int64(file.UncompressedSize64)+1))
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil {
			return SidecarManifest{}, copyErr
		}
		if copied != int64(file.UncompressedSize64) {
			return SidecarManifest{}, fmt.Errorf("plugin_archive_entry_size_mismatch")
		}
		if closeInputErr != nil || closeOutputErr != nil {
			return SidecarManifest{}, errors.Join(closeInputErr, closeOutputErr)
		}
		if cleanName == "plugin.json" {
			body, err := os.ReadFile(target)
			if err != nil || json.Unmarshal(body, &manifest) != nil {
				return SidecarManifest{}, fmt.Errorf("plugin_manifest_invalid_json")
			}
			manifestFound = true
		}
	}
	if !manifestFound {
		return SidecarManifest{}, fmt.Errorf("plugin_manifest_missing")
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return SidecarManifest{}, fmt.Errorf("plugin_manifest_missing_id")
	}
	if expectedID != "" && manifest.ID != expectedID {
		return SidecarManifest{}, fmt.Errorf("plugin_manifest_id_mismatch")
	}
	return manifest, nil
}

func cleanArchiveName(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "\\") || strings.Contains(raw, ":") || strings.IndexByte(raw, 0) >= 0 || path.IsAbs(raw) {
		return "", fmt.Errorf("plugin_archive_path_invalid")
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("plugin_archive_path_escape")
	}
	return clean, nil
}

func validatePluginManifestForPackage(manifest *SidecarManifest, pluginDir string) error {
	if validatePluginID(manifest.ID) != nil {
		return fmt.Errorf("plugin_manifest_invalid_id")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("plugin_manifest_missing_name")
	}
	if err := validateConfigFields(manifest.ConfigFields); err != nil {
		return fmt.Errorf("plugin_manifest_invalid_config: %w", err)
	}
	if err := validatePermissions(manifest.Permissions); err != nil {
		return fmt.Errorf("plugin_manifest_invalid_permissions: %w", err)
	}
	if strings.TrimSpace(manifest.Entrypoint) == "" {
		return fmt.Errorf("plugin_manifest_missing_entrypoint")
	}
	entrypoint, err := resolvePluginPath(pluginDir, manifest.Entrypoint)
	if err != nil {
		return fmt.Errorf("plugin_manifest_entrypoint_invalid")
	}
	if info, err := os.Stat(entrypoint); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("plugin_manifest_entrypoint_missing")
	}
	if manifest.PagePath == "" {
		manifest.PagePath = "/"
	}
	if manifest.HealthPath == "" {
		manifest.HealthPath = "healthz"
	}
	if err := validateSidecarSpecPaths(&SidecarSpec{PagePath: manifest.PagePath, HealthPath: manifest.HealthPath, APIPrefix: manifest.APIPrefix, ChannelPath: manifest.ChannelPath}); err != nil {
		return err
	}
	return nil
}

func validateSidecarSpecPaths(spec *SidecarSpec) error {
	if spec == nil {
		return errors.New("plugin_spec_invalid")
	}
	if err := validateSidecarPath(spec.PagePath); err != nil {
		return err
	}
	if err := validateSidecarPath(spec.HealthPath); err != nil {
		return err
	}
	if err := validateSidecarPath(spec.ChannelPath); err != nil {
		return err
	}
	if spec.APIPrefix != "" {
		if err := validateAPIPrefix(spec.APIPrefix); err != nil {
			return err
		}
	}
	return nil
}

func replacePluginDir(pluginDir, stageDir string) (string, error) {
	backupDir := pluginDir + ".previous"
	_ = os.RemoveAll(backupDir)
	if _, err := os.Stat(pluginDir); err == nil {
		if err := os.Rename(pluginDir, backupDir); err != nil {
			return "", fmt.Errorf("plugin_replace_backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("plugin_replace_stat: %w", err)
	}
	if err := os.Rename(stageDir, pluginDir); err != nil {
		_ = os.Rename(backupDir, pluginDir)
		return "", fmt.Errorf("plugin_replace: %w", err)
	}
	return backupDir, nil
}
