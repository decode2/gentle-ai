package upgrade

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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gentleman-programming/gentle-ai/internal/system"
	"github.com/gentleman-programming/gentle-ai/internal/update"
)

// httpClient is the HTTP client used for asset downloads.
// Package-level var for testability.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// lookPathFn resolves the binary path. Package-level var for testability.
var lookPathFn = exec.LookPath

// renameFn performs a filesystem rename. Package-level var for testability —
// it lets tests simulate rename outcomes (including a rename that reports an
// error despite actually completing on disk) without touching the real
// filesystem semantics of os.Rename.
var renameFn = os.Rename

// resolveAssetURLFn and resolveChecksumURLFn build download URLs.
// Package-level vars for testability.
var resolveAssetURLFn = resolveAssetURL
var resolveChecksumURLFn = resolveChecksumURL

// Download downloads the GitHub release binary for the given tool, verifies its
// SHA256 checksum against the release's checksums.txt, and replaces the installed
// binary atomically.
//
// Checksum verification is mandatory: the install fails if checksums.txt is
// unavailable, if the archive is not listed, or if the digest does not match.
//
// This function is not called on Windows — callers (strategy.go) gate it via
// platform check and return a manual fallback error instead.
func Download(ctx context.Context, r update.UpdateResult, profile system.PlatformProfile) error {
	if profile.OS == "windows" {
		hint := r.UpdateHint
		if hint == "" {
			hint = fmt.Sprintf("Download from https://github.com/%s/%s/releases", r.Tool.Owner, r.Tool.Repo)
		}
		return fmt.Errorf("upgrade %q on Windows requires manual update — %s", r.Tool.Name, hint)
	}

	// Resolve the current binary path from PATH.
	binaryPath, err := lookPathFn(r.Tool.Name)
	if err != nil {
		return fmt.Errorf("locate %q binary: %w", r.Tool.Name, err)
	}

	archiveName := resolveArchiveName(r.Tool.Repo, r.LatestVersion, profile.OS, runtime.GOARCH)
	assetURL := resolveAssetURLFn(r.Tool.Owner, r.Tool.Repo, r.LatestVersion, profile.OS, runtime.GOARCH)
	checksumURL := resolveChecksumURLFn(r.Tool.Owner, r.Tool.Repo, r.LatestVersion)

	// Download archive to a temp directory so we can verify before extracting.
	tmpDir, err := os.MkdirTemp("", "gentle-ai-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, archiveName)
	actualDigest, err := downloadToFile(ctx, assetURL, archivePath)
	if err != nil {
		return fmt.Errorf("download %s: %w", r.Tool.Name, err)
	}

	// Verify checksum — fail closed if checksums.txt is unavailable or mismatched.
	checksumsContent, err := fetchChecksums(ctx, checksumURL)
	if err != nil {
		return fmt.Errorf("checksum verification failed: checksums.txt unavailable: %w", err)
	}
	expectedDigest, err := expectedChecksumFor(checksumsContent, archiveName)
	if err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}
	if actualDigest != expectedDigest {
		return fmt.Errorf("checksum mismatch for %s:\n  expected: %s\n  got:      %s",
			archiveName, expectedDigest, actualDigest)
	}

	// Extract the verified binary.
	tmpBinaryPath := binaryPath + ".new"
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	if err := extractBinaryFromTarGz(f, r.Tool.Name, tmpBinaryPath); err != nil {
		_ = os.Remove(tmpBinaryPath)
		return fmt.Errorf("extract %s: %w", r.Tool.Name, err)
	}

	// Hash the extracted binary BEFORE replacing so we know exactly what
	// "correct" means once the replace step runs (see replaceAndVerify).
	wantHash, err := hashFile(tmpBinaryPath)
	if err != nil {
		_ = os.Remove(tmpBinaryPath)
		return fmt.Errorf("hash extracted %s binary: %w", r.Tool.Name, err)
	}

	// Replace the installed binary and positively verify the result.
	if err := replaceAndVerify(tmpBinaryPath, binaryPath, wantHash); err != nil {
		_ = os.Remove(tmpBinaryPath)
		return fmt.Errorf("replace %q: %w", binaryPath, err)
	}

	return nil
}

// resolveArchiveName returns the GoReleaser archive filename for the given
// repo/version/os/arch combination.
//
// Convention: {repo}_{version}_{os}_{arch}.tar.gz
func resolveArchiveName(repo, version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", repo, version, goos, goarch)
}

// resolveAssetURL constructs the GitHub Releases asset download URL.
func resolveAssetURL(owner, repo, version, goos, goarch string) string {
	filename := resolveArchiveName(repo, version, goos, goarch)
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/%s",
		owner, repo, version, filename)
}

// resolveChecksumURL constructs the GitHub Releases URL for checksums.txt.
func resolveChecksumURL(owner, repo, version string) string {
	return fmt.Sprintf("https://github.com/%s/%s/releases/download/v%s/checksums.txt",
		owner, repo, version)
}

// downloadToFile downloads the resource at url to outPath and returns the
// SHA256 hex digest of the downloaded content.
func downloadToFile(ctx context.Context, url string, outPath string) (hexDigest string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", outPath, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", fmt.Errorf("write %s: %w", outPath, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChecksums downloads checksums.txt from url and returns its content.
// Returns an error if the file cannot be fetched or the server returns non-200.
func fetchChecksums(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch checksums.txt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums.txt: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read checksums.txt: %w", err)
	}
	return string(data), nil
}

// expectedChecksumFor parses checksums.txt content and returns the SHA256 hex
// digest for filename. Returns an error if the filename is not listed.
//
// GoReleaser produces BSD-style checksums.txt: "<digest>  <filename>" per line.
func expectedChecksumFor(content, filename string) (string, error) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%q not listed in checksums.txt", filename)
}

// downloadBinary fetches the asset at url, extracts the binary named binaryName
// from the .tar.gz, and writes it to outPath with executable permissions.
//
// Note: this function does not verify checksums. Use Download for a complete,
// checksum-verified upgrade flow.
func downloadBinary(ctx context.Context, url string, binaryName string, outPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	return extractBinaryFromTarGz(resp.Body, binaryName, outPath)
}

// extractBinaryFromTarGz reads a .tar.gz stream and extracts the first file
// whose base name matches binaryName, writing it to outPath.
func extractBinaryFromTarGz(r io.Reader, binaryName string, outPath string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		// Match by base name (handles subdirectory layouts like tool_1.0_os_arch/tool).
		// Only accept regular files — skip symlinks, hardlinks, and special files.
		if filepath.Base(hdr.Name) == binaryName &&
			(hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA) {
			if err := writeExecutable(tr, outPath); err != nil {
				return err
			}
			return nil
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

// writeExecutable writes the content from r to outPath with executable permissions.
func writeExecutable(r io.Reader, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}

	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	return nil
}

// atomicReplace moves src to dst via a single atomic rename.
// On Unix this is atomic: on success dst is the new binary; on a failure dst
// is left untouched (the previous binary, or absent on first install). The
// caller guards against Windows before calling (a running binary cannot be
// renamed over on Windows).
func atomicReplace(src, dst string) error {
	if err := renameFn(src, dst); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", src, dst, err)
	}
	return nil
}

// hashFile returns the SHA256 hex digest of the file at path.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// replaceAndVerify replaces dst with src, then guards against issue #230.
// In some hardened or restricted environments (potentially involving overlayfs,
// immutable distros, or SELinux/AppArmor profiles), a rename error might be
// reported during a running binary's self-replace even if the replacement actually
// completed on disk. When the rename reports success we trust it (the rename
// is atomic). When it reports an error we positively verify dst's on-disk content
// against wantHash — the SHA256 of the extracted binary — and only treat it
// as success if the content provably matches.
//
// Analysis of self-update race:
// A concurrent self-update race occurs if two processes attempt to upgrade the
// same binary to wantHash concurrently. Because renameFn (os.Rename) is atomic,
// if one process succeeds in renaming, the binary on disk is updated fully.
// The other process's rename will fail (e.g., due to file locks or busy text file).
// By verifying the hash, the second process confirms that the correct binary
// is indeed present on disk, ensuring a consistent final state without reporting
// a false failure to the user. Since the check only succeeds if the hash matches
// wantHash exactly, it is impossible to mask a failure to write a different version,
// nor can it result in a half-written or corrupt binary.
//
// Non-masking guarantee: a reported error is converted to success ONLY when
// dst's bytes equal wantHash exactly. On a genuine failure the atomic rename
// leaves dst as the previous binary (never missing/half-written), and the real
// error is returned — a real failure is never reported as success.
//
// Invariant: the success path (nil error from atomicReplace) is trusted
// without re-hashing, because renameFn is os.Rename — a metadata-only
// operation that cannot report nil unless the rename actually completed, so
// dst then holds src's bytes (== wantHash, computed just before). Do NOT wire
// a retrying/best-effort rename into renameFn that could report nil on a
// rename that did not complete; that would reopen a masking hole here.
func replaceAndVerify(src, dst, wantHash string) error {
	if err := atomicReplace(src, dst); err != nil {
		// The rename reported an error, but it may still have landed. Verify.
		gotHash, hashErr := hashFile(dst)
		if hashErr == nil && gotHash == wantHash {
			return nil
		}
		return err
	}
	// Rename reported success: os.Rename is atomic, so dst now holds the
	// extracted binary (== wantHash by construction) — no re-hash needed.
	return nil
}
