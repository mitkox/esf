package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	defaultAPIBase     = "https://api.github.com/repos/mitkox/esf"
	defaultReleaseBase = "https://github.com/mitkox/esf/releases/download"
	maxMetadataSize    = 1 << 20
	maxChecksumsSize   = 1 << 20
	maxArchiveSize     = 100 << 20
	maxBinarySize      = 100 << 20
)

var releaseVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$`)

type Options struct {
	Version     string
	Current     string
	Executable  string
	GOOS        string
	GOARCH      string
	Client      *http.Client
	APIBase     string
	ReleaseBase string
	// VerifyAttestation permits an isolated verifier in tests. Production uses
	// GitHub's signed release-workflow provenance before replacing the binary.
	VerifyAttestation func(context.Context, []byte, string) error
}

type Result struct {
	Version        string
	AlreadyCurrent bool
}

type release struct {
	TagName string `json:"tag_name"`
}

func Update(ctx context.Context, options Options) (Result, error) {
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if options.APIBase == "" {
		options.APIBase = defaultAPIBase
	}
	if options.ReleaseBase == "" {
		options.ReleaseBase = defaultReleaseBase
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.GOARCH == "" {
		options.GOARCH = runtime.GOARCH
	}
	if options.Executable == "" {
		path, err := os.Executable()
		if err != nil {
			return Result{}, fmt.Errorf("find current executable: %w", err)
		}
		options.Executable = path
	}

	version := options.Version
	if version == "" {
		var err error
		version, err = latestVersion(ctx, options.Client, options.APIBase)
		if err != nil {
			return Result{}, err
		}
	}
	if err := validateVersion(version); err != nil {
		return Result{}, err
	}
	if version == options.Current {
		return Result{Version: version, AlreadyCurrent: true}, nil
	}
	if options.GOOS != "linux" && options.GOOS != "darwin" {
		return Result{}, fmt.Errorf("unsupported operating system %q", options.GOOS)
	}
	if options.GOARCH != "amd64" && options.GOARCH != "arm64" {
		return Result{}, fmt.Errorf("unsupported architecture %q", options.GOARCH)
	}

	releaseName := strings.TrimPrefix(version, "v")
	archiveName := fmt.Sprintf("esf_machinist_%s_%s_%s.tar.gz", releaseName, options.GOOS, options.GOARCH)
	baseURL := strings.TrimRight(options.ReleaseBase, "/") + "/" + version
	checksums, err := download(ctx, options.Client, baseURL+"/checksums.txt", maxChecksumsSize)
	if err != nil {
		return Result{}, fmt.Errorf("download checksums: %w", err)
	}
	want, err := checksumFor(checksums, archiveName)
	if err != nil {
		return Result{}, err
	}
	archive, err := download(ctx, options.Client, baseURL+"/"+archiveName, maxArchiveSize)
	if err != nil {
		return Result{}, fmt.Errorf("download release archive: %w", err)
	}
	got := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return Result{}, fmt.Errorf("checksum mismatch for %s", archiveName)
	}
	verify := options.VerifyAttestation
	if verify == nil {
		verify = verifyAttestation
	}
	if err := verify(ctx, archive, version); err != nil {
		return Result{}, fmt.Errorf("release provenance verification failed: %w", err)
	}
	binary, err := extractBinary(archive)
	if err != nil {
		return Result{}, err
	}
	if err := replaceExecutable(options.Executable, binary); err != nil {
		return Result{}, err
	}
	return Result{Version: version}, nil
}

func verifyAttestation(ctx context.Context, archive []byte, version string) error {
	file, err := os.CreateTemp("", "esf-release-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(archive); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "gh", "attestation", "verify", file.Name(), "-R", "mitkox/esf", "--signer-workflow", "mitkox/esf/.github/workflows/release.yml", "--source-ref", "refs/tags/"+version)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh attestation verify: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func latestVersion(ctx context.Context, client *http.Client, apiBase string) (string, error) {
	body, err := download(ctx, client, strings.TrimRight(apiBase, "/")+"/releases/latest", maxMetadataSize)
	if err != nil {
		return "", fmt.Errorf("find latest release: %w", err)
	}
	var metadata release
	if err := json.Unmarshal(body, &metadata); err != nil {
		return "", fmt.Errorf("decode latest release: %w", err)
	}
	if err := validateVersion(metadata.TagName); err != nil {
		return "", fmt.Errorf("latest release: %w", err)
	}
	return metadata.TagName, nil
}

func validateVersion(version string) error {
	if !releaseVersionPattern.MatchString(version) {
		return fmt.Errorf("invalid release version %q; expected vMAJOR.MINOR.PATCH", version)
	}
	return nil
}

func download(ctx context.Context, client *http.Client, endpoint string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "esf-updater")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return nil, fmt.Errorf("%s returned %s: %s", endpoint, response.Status, strings.TrimSpace(string(message)))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", endpoint, limit)
	}
	return body, nil
}

func checksumFor(checksums []byte, archiveName string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == archiveName {
			decoded, err := hex.DecodeString(fields[0])
			if err != nil || len(decoded) != sha256.Size {
				return "", fmt.Errorf("invalid checksum for %s", archiveName)
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksums.txt does not contain %s", archiveName)
}

func extractBinary(archive []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open release archive: %w", err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read release archive: %w", err)
		}
		if header.Name != "machinist" || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if header.Size < 1 || header.Size > maxBinarySize {
			return nil, errors.New("release archive contains an invalid machinist binary size")
		}
		binary, err := io.ReadAll(io.LimitReader(tarReader, maxBinarySize+1))
		if err != nil {
			return nil, fmt.Errorf("read machinist binary: %w", err)
		}
		return binary, nil
	}
	return nil, errors.New("release archive does not contain machinist")
}

func replaceExecutable(path string, binary []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".machinist-update-*")
	if err != nil {
		return fmt.Errorf("create update beside %s: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	mode := info.Mode().Perm()
	if mode&0o111 == 0 {
		mode = 0o755
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set update permissions: %w", err)
	}
	if _, err := temporary.Write(binary); err != nil {
		temporary.Close()
		return fmt.Errorf("write update: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync update: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close update: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
