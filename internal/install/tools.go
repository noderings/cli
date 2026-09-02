package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/noderings/cli/internal/api"
	"github.com/noderings/cli/internal/config"
)

// maxChecksumsFileBytes bounds how much of a release checksums list is read.
const maxChecksumsFileBytes = 1 << 20

// helmDownloadBase is the official helm release host, which publishes a
// "<artifact>.sha256sum" sidecar next to every tarball.
const helmDownloadBase = "https://get.helm.sh/"

// ToolBootstrapper downloads and installs CLI-managed tools (helm, liqoctl).
type ToolBootstrapper struct {
	binDir          string
	verifyChecksums bool
	logger          Logger
	httpClient      *http.Client
}

// NewToolBootstrapper creates a bootstrapper that installs into ~/.nr/bin.
func NewToolBootstrapper(verifyChecksums bool, logger Logger) (*ToolBootstrapper, error) {
	binDir, err := EnsureCLIBinDir()
	if err != nil {
		return nil, err
	}
	return &ToolBootstrapper{
		binDir:          binDir,
		verifyChecksums: verifyChecksums,
		logger:          logger,
		httpClient:      &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// EnsureCLIBinDir creates ~/.nr/bin and prepends it to PATH for this process.
func EnsureCLIBinDir() (string, error) {
	binDir := filepath.Join(config.GetConfigDir(), "bin")
	//nolint:gosec // G301: ~/.nr/bin must be user-executable on PATH
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return "", fmt.Errorf("create CLI bin dir: %w", err)
	}
	PrependPath(binDir)
	return binDir, nil
}

// PrependPath puts dir at the front of PATH if not already present.
func PrependPath(dir string) {
	if dir == "" {
		return
	}
	current := os.Getenv("PATH")
	parts := filepath.SplitList(current)
	for _, p := range parts {
		if p == dir {
			return
		}
	}
	if current == "" {
		_ = os.Setenv("PATH", dir)
		return
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+current)
}

// BootstrapTools ensures helm and liqoctl match the given platform pins.
func (t *ToolBootstrapper) BootstrapTools(ctx context.Context, pins *api.PlatformPins) error {
	if pins == nil {
		return fmt.Errorf("platform pins are required")
	}

	for _, name := range []string{api.ComponentHelm, api.ComponentLiqoctl} {
		pin, ok := pins.Get(name)
		if !ok || pin.Version == "" {
			t.logger.Warnf("Server did not pin %s; skipping bootstrap for this tool", name)
			continue
		}
		if err := t.ensureTool(ctx, pin); err != nil {
			return fmt.Errorf("bootstrap %s: %w", name, err)
		}
	}
	return nil
}

func (t *ToolBootstrapper) ensureTool(ctx context.Context, pin api.ComponentPin) error {
	dest := filepath.Join(t.binDir, pin.Name)

	if satisfies, current := toolSatisfiesVersion(ctx, pin.Name, pin.Version, ""); satisfies {
		t.logger.Infof("%s already satisfies required version (have %s, want %s)", pin.Name, current, pin.Version)
		return nil
	}

	url, err := DeriveToolDownloadURL(pin.Name, pin.Version, runtime.GOOS, runtime.GOARCH, pin.DownloadURL)
	if err != nil {
		return err
	}

	t.logger.Infof("Downloading %s %s from %s", pin.Name, pin.Version, url)

	var tmpPath string
	if strings.HasPrefix(url, "oci://") {
		tmpPath, err = t.orasPullToTemp(ctx, url)
	} else {
		tmpPath, err = t.downloadToTemp(ctx, url)
	}
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpPath) }()

	if t.verifyChecksums {
		if err := t.verifyDownload(ctx, pin, url, tmpPath); err != nil {
			return err
		}
	}

	if err := t.installDownloaded(pin.Name, tmpPath, dest); err != nil {
		return err
	}

	t.logger.Infof("✓ Installed %s %s to %s", pin.Name, pin.Version, dest)
	return nil
}

// verifyDownload checks the artifact at tmpPath against the best digest available.
//
// The control plane pins one checksum per component, but download URLs are derived per
// OS/arch, so it cannot pin a digest that is correct on every platform and in practice
// leaves it empty. Rather than refuse to install, fall back to the digest the upstream
// project publishes next to the artifact itself.
func (t *ToolBootstrapper) verifyDownload(ctx context.Context, pin api.ComponentPin, url, tmpPath string) error {
	if pin.ChecksumSHA256 != "" {
		return VerifySHA256File(tmpPath, pin.ChecksumSHA256)
	}

	// OCI artifacts are content-addressed: oras validates every layer against the digests
	// in the manifest while pulling, so there is no separate checksum to fetch.
	if strings.HasPrefix(url, "oci://") {
		t.logger.Infof("%s pulled from an OCI registry; integrity verified by content digest", pin.Name)
		return nil
	}

	checksumURL := upstreamChecksumURL(pin.Name, url)
	if checksumURL == "" {
		return fmt.Errorf("checksum verification is enabled but the server provided no checksum for %s "+
			"and no published checksum is available for %s; "+
			"set downloads.verify_checksums=false to install unverified binaries", pin.Name, url)
	}

	t.logger.Infof("Server pinned no checksum for %s; verifying against %s", pin.Name, checksumURL)
	expected, err := t.fetchReleaseSHA256(ctx, checksumURL, path.Base(url))
	if err != nil {
		return fmt.Errorf("resolve upstream %s checksum: %w", pin.Name, err)
	}
	return VerifySHA256File(tmpPath, expected)
}

// upstreamChecksumURL returns the checksum published alongside artifactURL, or "" when the
// source publishes none. Limited to known official hosts: a mirror or operator-supplied
// override cannot be assumed to serve a trustworthy sidecar.
func upstreamChecksumURL(name, artifactURL string) string {
	if name == api.ComponentHelm && strings.HasPrefix(artifactURL, helmDownloadBase) {
		return artifactURL + ".sha256sum"
	}
	return ""
}

func (t *ToolBootstrapper) downloadToTemp(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NodeRings-CLI/1.0")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "nr-tool-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = tmp.Close() }()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

func (t *ToolBootstrapper) installDownloaded(name, src, dest string) error {
	switch name {
	case api.ComponentHelm:
		return extractHelmBinary(src, dest, runtime.GOOS, runtime.GOARCH)
	case api.ComponentLiqoctl:
		if strings.HasSuffix(src, ".tar.gz") || strings.HasSuffix(src, ".tgz") || isGzipFile(src) {
			return extractLiqoctlBinary(src, dest)
		}
		return installBinaryFile(src, dest)
	default:
		return installBinaryFile(src, dest)
	}
}

func isGzipFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var hdr [2]byte
	n, err := f.Read(hdr[:])
	//nolint:gosec // G602: n==2 guarantees both indices are in range
	return err == nil && n == 2 && hdr[0] == 0x1f && hdr[1] == 0x8b
}

func extractLiqoctlBinary(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open liqoctl gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read liqoctl archive: %w", err)
		}
		base := filepath.Base(hdr.Name)
		if base != "liqoctl" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		//nolint:gosec // G302: liqoctl must be executable
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		//nolint:gosec // G110: archive is a trusted Harbor OCI artifact
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("liqoctl binary not found in archive")
}

// orasPullToTemp pulls an OCI artifact (e.g. liqoctl tarball) into a temp file.
func (t *ToolBootstrapper) orasPullToTemp(ctx context.Context, ociURL string) (string, error) {
	ref := strings.TrimPrefix(ociURL, "oci://")
	orasPath, err := t.ensureOras(ctx)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "nr-oras-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cmd := exec.CommandContext(ctx, orasPath, "pull", ref, "-o", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("oras pull %s: %w\n%s", ref, err, strings.TrimSpace(string(out)))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var artifact string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz") || name == "liqoctl" {
			artifact = filepath.Join(dir, name)
			break
		}
	}
	if artifact == "" {
		return "", fmt.Errorf("oras pull %s produced no binary/tarball in %s", ref, dir)
	}

	tmp, err := os.CreateTemp("", "nr-tool-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	_ = tmp.Close()

	data, err := os.ReadFile(artifact)
	if err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	// Preserve .tar.gz suffix so installDownloaded can detect archive format.
	if strings.HasSuffix(artifact, ".tar.gz") || strings.HasSuffix(artifact, ".tgz") {
		_ = os.Remove(tmpName)
		tmpName = tmpName + ".tar.gz"
	}
	//nolint:gosec // G306: temp download path before install
	if err := os.WriteFile(tmpName, data, 0600); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}

func (t *ToolBootstrapper) ensureOras(ctx context.Context) (string, error) {
	if path, err := exec.LookPath("oras"); err == nil {
		return path, nil
	}

	dest := filepath.Join(t.binDir, "oras")
	if st, err := os.Stat(dest); err == nil && !st.IsDir() {
		return dest, nil
	}

	version := config.DefaultOrasVersion
	semver := strings.TrimPrefix(version, "v")
	arch := normalizeArch(runtime.GOARCH)
	asset := fmt.Sprintf("oras_%s_%s_%s.tar.gz", semver, runtime.GOOS, arch)
	baseURL := fmt.Sprintf("https://github.com/oras-project/oras/releases/download/%s/", version)

	t.logger.Infof("Downloading oras %s from %s", version, baseURL+asset)
	archive, err := t.downloadToTemp(ctx, baseURL+asset)
	if err != nil {
		return "", fmt.Errorf("download oras: %w", err)
	}
	defer func() { _ = os.Remove(archive) }()

	// oras is used to pull other tools/charts, so its own integrity must be checked
	// against the checksums published alongside the release.
	expected, err := t.fetchReleaseSHA256(ctx, baseURL+fmt.Sprintf("oras_%s_checksums.txt", semver), asset)
	if err != nil {
		return "", fmt.Errorf("resolve oras checksum: %w", err)
	}
	if err := VerifySHA256File(archive, expected); err != nil {
		return "", fmt.Errorf("verify oras download: %w", err)
	}

	if err := extractNamedBinaryFromTarGz(archive, "oras", dest); err != nil {
		return "", err
	}
	return dest, nil
}

// fetchReleaseSHA256 returns the digest for asset from a published checksum document, in
// either the "<sha256>  <filename>" list form or the bare "<sha256>" per-artifact form.
func (t *ToolBootstrapper) fetchReleaseSHA256(ctx context.Context, checksumsURL, asset string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NodeRings-CLI/1.0")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: HTTP %d", checksumsURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksumsFileBytes))
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(body))
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && path.Base(fields[1]) == asset {
			return fields[0], nil
		}
	}
	// Per-artifact sidecars carry only the digest, with no filename to match against.
	if fields := strings.Fields(content); len(fields) == 1 && isHexSHA256(fields[0]) {
		return fields[0], nil
	}
	return "", fmt.Errorf("no checksum for %s in %s", asset, checksumsURL)
}

func extractNamedBinaryFromTarGz(archivePath, binaryName, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) != binaryName || hdr.Typeflag != tar.TypeReg {
			continue
		}
		//nolint:gosec // G302: tool binary must be executable
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		//nolint:gosec // G110: trusted GitHub release archive
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	return fmt.Errorf("%s not found in archive", binaryName)
}

func installBinaryFile(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	//nolint:gosec // G306: installed CLI tools must be executable (0755)
	if err := os.WriteFile(dest, data, 0755); err != nil {
		return fmt.Errorf("write binary: %w", err)
	}
	return nil
}

func extractHelmBinary(archivePath, dest, goos, goarch string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open helm gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	wantSuffix := filepath.ToSlash(filepath.Join(fmt.Sprintf("%s-%s", goos, goarch), "helm"))

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read helm archive: %w", err)
		}
		name := filepath.ToSlash(hdr.Name)
		if name != wantSuffix && !strings.HasSuffix(name, "/"+wantSuffix) && name != "helm" {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		//nolint:gosec // G302: helm binary must be executable (0755)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}
		//nolint:gosec // G110: archive is a trusted upstream helm release tarball
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}

	return fmt.Errorf("helm binary not found in archive (expected %s)", wantSuffix)
}

// DeriveToolDownloadURL builds a download URL for a tool, or uses override when set.
func DeriveToolDownloadURL(name, version, goos, goarch, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if version == "" {
		return "", fmt.Errorf("%s version is required to derive download URL", name)
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}

	arch := normalizeArch(goarch)
	osName := goos

	switch name {
	case api.ComponentHelm:
		return fmt.Sprintf("%shelm-%s-%s-%s.tar.gz", helmDownloadBase, version, osName, arch), nil
	case api.ComponentLiqoctl:
		// NodeRings fork: Harbor OCI artifact (tar.gz containing liqoctl).
		return fmt.Sprintf("oci://%s/liqoctl-%s-%s:%s", config.DefaultLiqoctlOCIRepo, osName, arch, version), nil
	default:
		return "", fmt.Errorf("no download URL derivation for tool %q", name)
	}
}

func normalizeArch(goarch string) string {
	switch goarch {
	case "aarch64":
		return "arm64"
	case "x86_64":
		return "amd64"
	default:
		return goarch
	}
}

func isHexSHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// VerifySHA256File checks that path has the expected hex SHA-256 digest.
func VerifySHA256File(path, expectedHex string) error {
	expectedHex = strings.ToLower(strings.TrimSpace(expectedHex))
	if expectedHex == "" {
		return fmt.Errorf("empty checksum")
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: got %s, want %s", actual, expectedHex)
	}
	return nil
}

// CompareSemverLoose compares versions after normalizing common prefixes/suffixes.
// Returns -1, 0, 1 like semver Compare, or error if unparsable.
func CompareSemverLoose(current, required string) (int, error) {
	vc := NewVersionChecker()
	cur, err := vc.ExtractVersion(current)
	if err != nil {
		cur = strings.TrimPrefix(strings.TrimSpace(current), "v")
	}
	req, err := vc.ExtractVersion(required)
	if err != nil {
		req = strings.TrimPrefix(strings.TrimSpace(required), "v")
	}
	return vc.CompareVersions(cur, req)
}

// VersionSatisfies reports whether current meets required (or minVersion when required is empty).
func VersionSatisfies(current, required, minVersion string) bool {
	if current == "" {
		return false
	}
	target := required
	if target == "" {
		target = minVersion
	}
	if target == "" {
		return true
	}
	// Fork pins like v0.0.0-<gitsha> must match exactly (semver "1.1.1 >= 0.0.0" is wrong).
	if isForkPinVersion(target) {
		want := strings.TrimPrefix(strings.TrimSpace(target), "v")
		cur := strings.TrimSpace(current)
		return strings.Contains(cur, want) || strings.Contains(cur, "v"+want)
	}
	cmp, err := CompareSemverLoose(current, target)
	return err == nil && cmp >= 0
}

func isForkPinVersion(version string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return strings.HasPrefix(v, "0.0.0-")
}

func toolSatisfiesVersion(ctx context.Context, name, required, minVersion string) (bool, string) {
	path, err := exec.LookPath(name)
	if err != nil {
		return false, ""
	}

	args := versionArgsForTool(name)
	cmd := exec.CommandContext(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, ""
	}
	current := strings.TrimSpace(string(out))
	return VersionSatisfies(current, required, minVersion), current
}

func versionArgsForTool(name string) []string {
	switch name {
	case api.ComponentLiqoctl:
		return []string{"version", "--client"}
	case api.ComponentHelm:
		return []string{"version", "--short"}
	default:
		return []string{"--version"}
	}
}

// ApplyPlatformPinsToConfig overlays server pins onto local config (server wins).
func ApplyPlatformPinsToConfig(cfg *config.Config, pins *api.PlatformPins, log Logger) {
	if cfg == nil || pins == nil {
		return
	}
	if v := pins.VersionOr(api.ComponentK3s, ""); v != "" {
		if cfg.K3s.Version != "" && cfg.K3s.Version != v && log != nil {
			log.Warnf("Overriding local k3s.version %s with server pin %s", cfg.K3s.Version, v)
		}
		cfg.K3s.Version = v
	}
	if pin, ok := pins.Get(api.ComponentK3s); ok && pin.MinVersion != "" {
		cfg.K3s.MinVersion = pin.MinVersion
	}
	if v := pins.VersionOr(api.ComponentCalico, ""); v != "" {
		if cfg.Calico.Version != "" && cfg.Calico.Version != v && log != nil {
			log.Warnf("Overriding local calico.version %s with server pin %s", cfg.Calico.Version, v)
		}
		cfg.Calico.Version = v
	}
	if pin, ok := pins.Get(api.ComponentCalico); ok && pin.MinVersion != "" {
		cfg.Calico.MinVersion = pin.MinVersion
	}
	if v := pins.VersionOr(api.ComponentLiqo, ""); v != "" {
		if cfg.Liqo.Version != "" && cfg.Liqo.Version != v && log != nil {
			log.Warnf("Overriding local liqo.version %s with server pin %s", cfg.Liqo.Version, v)
		}
		cfg.Liqo.Version = v
		cfg.Liqo.ChartVersion = strings.TrimPrefix(v, "v")
	}
	if pin, ok := pins.Get(api.ComponentLiqo); ok {
		if pin.MinVersion != "" {
			cfg.Liqo.MinVersion = pin.MinVersion
		}
		if pin.DownloadURL != "" {
			ociRef, chartVer := parseOCIChartRef(pin.DownloadURL)
			if ociRef != "" {
				cfg.Liqo.ChartOCI = ociRef
			}
			if chartVer != "" {
				cfg.Liqo.ChartVersion = chartVer
			}
		}
	}
}

// parseOCIChartRef splits oci://host/path:version into repo and version.
func parseOCIChartRef(ref string) (repo, version string) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "oci://") {
		return "", ""
	}
	withoutScheme := strings.TrimPrefix(ref, "oci://")
	// harbor.noderings.com/nrings/liqo:0.0.0-4600ebb8
	if i := strings.LastIndex(withoutScheme, ":"); i > 0 {
		hostPath := withoutScheme[:i]
		ver := withoutScheme[i+1:]
		// avoid splitting digests wrongly; chart versions don't contain /
		if !strings.Contains(ver, "/") {
			return "oci://" + hostPath, ver
		}
	}
	return "oci://" + withoutScheme, ""
}

// VersionsMap returns a flat name→version map for persistence / diagnostics.
func VersionsMap(pins *api.PlatformPins) map[string]string {
	out := map[string]string{}
	if pins == nil {
		return out
	}
	for name, pin := range pins.Components {
		if pin.Version != "" {
			out[name] = pin.Version
		}
	}
	return out
}
