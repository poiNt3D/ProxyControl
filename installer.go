package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// installer.go — download proxy binaries from GitHub and install them
// either to the user's bin directory (~/.local/bin) or system-wide
// (/usr/local/bin, via pkexec/sudo).

// --- GitHub release API types ---------------------------------------------

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type githubRelease struct {
	TagName string          `json:"tag_name"`
	Assets  []githubAsset   `json:"assets"`
}

// FetchLatestRelease queries the GitHub API for the latest release of
// the service's upstream project.  It tries /releases/latest first
// (excludes pre-releases); if that returns nothing it falls back to the
// /releases list and takes the first entry.
func (s *ServiceDef) FetchLatestRelease() (*githubRelease, error) {
	slug := s.RepoSlug()
	urls := []string{
		fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", slug),
		fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=1", slug),
	}
	client := &http.Client{Timeout: 30 * time.Second}
	// An optional GITHUB_TOKEN env var raises the rate limit from 60 to
	// 5000 req/hour — useful on shared IPs.  Not required for normal use.
	token := os.Getenv("GITHUB_TOKEN")
	for _, u := range urls {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		// The list endpoint returns a JSON array; latest returns an object.
		trimmed := strings.TrimSpace(string(body))
		if strings.HasPrefix(trimmed, "[") {
			var list []githubRelease
			if json.Unmarshal(body, &list) == nil && len(list) > 0 {
				return &list[0], nil
			}
			continue
		}
		var rel githubRelease
		if json.Unmarshal(body, &rel) == nil && len(rel.Assets) > 0 {
			return &rel, nil
		}
	}
	return nil, fmt.Errorf(T("err.fetch_release"), s.RepoSlug())
}

// MatchAsset finds the release asset matching the running platform.
// It looks for a .tar.gz or .zip whose name contains the service's arch
// keyword.
func (s *ServiceDef) MatchAsset(rel *githubRelease) (*githubAsset, error) {
	arch := s.AssetArch()
	for i := range rel.Assets {
		a := &rel.Assets[i]
		if strings.Contains(a.Name, arch) && (strings.HasSuffix(a.Name, ".tar.gz") || strings.HasSuffix(a.Name, ".zip")) {
			return a, nil
		}
	}
	return nil, fmt.Errorf(T("err.arch_not_found"),
		arch, assetNames(rel.Assets))
}

func assetNames(assets []githubAsset) string {
	var names []string
	for _, a := range assets {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

// --- download + extract ----------------------------------------------------

// progressReader wraps an io.Reader and reports cumulative bytes read.
type progressReader struct {
	r     io.Reader
	total int64
	done  int64
	fn    func(done, total int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.done += int64(n)
	if pr.fn != nil {
		pr.fn(pr.done, pr.total)
	}
	return n, err
}

// DownloadAndExtract downloads the matching asset from rel and extracts
// the service's binary to a temp file.  Returns the temp file path —
// the caller MUST os.Remove it when done.  onProgress receives the
// download progress (bytes done / total).  Both .tar.gz (wireproxy,
// byedpi) and .zip (nodpi) archives are supported.
func (s *ServiceDef) DownloadAndExtract(rel *githubRelease, onProgress func(done, total int64)) (string, error) {
	asset, err := s.MatchAsset(rel)
	if err != nil {
		return "", err
	}
	archive, err := downloadAsset(asset, onProgress)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	if strings.HasSuffix(asset.Name, ".zip") {
		return extractBinaryFromZip(archive, s.ArchiveBinaryName())
	}
	return extractBinary(archive, s.ArchiveBinaryName())
}

func downloadAsset(asset *githubAsset, onProgress func(done, total int64)) (string, error) {
	resp, err := http.Get(asset.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "proxycontrol-download-*")
	if err != nil {
		return "", err
	}
	pr := &progressReader{r: resp.Body, total: asset.Size, fn: onProgress}
	_, err = io.Copy(tmp, pr)
	tmp.Close()
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// extractBinary pulls the executable out of a .tar.gz into a new temp
// file and returns its path.  namePrefix is matched as a prefix on the
// base name so it handles both "wireproxy" (exact) and "ciadpi-x86_64"
// (prefix) — the byedpi archive ships its binary as ciadpi-<arch>, not
// byedpi.
func extractBinary(tarballPath, namePrefix string) (string, error) {
	f, err := os.Open(tarballPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		base := filepath.Base(hdr.Name)
		if strings.HasPrefix(base, namePrefix) && hdr.Typeflag == tar.TypeReg {
			out, err := os.CreateTemp("", "proxycontrol-bin-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				_ = os.Remove(out.Name())
				return "", err
			}
			out.Close()
			_ = os.Chmod(out.Name(), 0o755)
			return out.Name(), nil
		}
	}
	return "", fmt.Errorf(T("err.binary_not_in_arch"), namePrefix)
}

// extractBinaryFromZip pulls the executable out of a .zip archive into
// a new temp file and returns its path.  Used for NoDPI, whose release
// ships as a PyInstaller bundle inside a .zip (with a top-level
// directory like nodpi_v2.3_linux_x64/nodpi).  namePrefix is matched as
// a prefix on the base name so "nodpi" matches "nodpi" exactly.
func extractBinaryFromZip(zipPath, namePrefix string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if strings.HasPrefix(base, namePrefix) && !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			out, err := os.CreateTemp("", "proxycontrol-bin-*")
			if err != nil {
				rc.Close()
				return "", err
			}
			if _, err := io.Copy(out, rc); err != nil {
				rc.Close()
				out.Close()
				_ = os.Remove(out.Name())
				return "", err
			}
			rc.Close()
			out.Close()
			_ = os.Chmod(out.Name(), 0o755)
			return out.Name(), nil
		}
	}
	return "", fmt.Errorf(T("err.binary_not_in_arch"), namePrefix)
}

// --- install ---------------------------------------------------------------

// UserBinDir returns ~/.local/bin — the per-user binary directory.
func UserBinDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".local", "bin")
}

// SystemBinDir returns /usr/local/bin — the system-wide binary directory.
func SystemBinDir() string { return "/usr/local/bin" }

// InstallBinary copies the temp binary at srcPath to destPath (full path
// including the file name).  When systemWide is true, pkexec (or sudo as
// a fallback) is used to gain root for the copy + chmod.
func InstallBinary(srcPath, destPath string, systemWide bool) error {
	if !systemWide {
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		return copyFile(srcPath, destPath, 0o755)
	}
	return privilegedCopy(srcPath, destPath)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// privilegedCopy uses pkexec (GUI password prompt) or, if absent, sudo
// to copy the file as root and set executable permissions.  The paths
// are controlled (a temp file and a known /usr/local/bin destination),
// so single-quoting them is safe.
func privilegedCopy(src, dst string) error {
	shellCmd := fmt.Sprintf("cp '%s' '%s' && chmod 755 '%s'", src, dst, dst)
	if _, err := exec.LookPath("pkexec"); err == nil {
		return exec.Command("pkexec", "sh", "-c", shellCmd).Run()
	}
	if _, err := exec.LookPath("sudo"); err == nil {
		return exec.Command("sudo", "sh", "-c", shellCmd).Run()
	}
	return fmt.Errorf(T("err.no_privilege"))
}

// FinalizeInstall persists the binary path to the config, updates the
// systemd unit's ExecStart to point at it, and daemon-reloads.  Call
// this after InstallBinary succeeds.
func (s *ServiceDef) FinalizeInstall(destPath string) {
	s.SetBinaryPath(destPath)
	UpdateUnitBinaryPath(s)
}

// --- version parsing ------------------------------------------------------

// versionRe matches a dotted version number like "1.0.18" or "17.3".
var versionRe = regexp.MustCompile(`\d+(\.\d+)*`)

// extractVersionString pulls the first dotted-number sequence out of a
// binary's --version output.  Handles "wireproxy, version 1.0.18"
// → "1.0.18" and bare "17.3" → "17.3".
func extractVersionString(output string) string {
	return versionRe.FindString(output)
}

// normalizeVersion strips a leading "v" and removes leading zero
// components so that tags and binary output can be compared despite
// different conventions.  Examples:
//
//	"v0.17.3" → "17.3"   (byedpi tag has a 0. prefix the binary drops)
//	"v1.0.18" → "1.0.18" (wireproxy tag and binary agree)
//	"17.3"    → "17.3"
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	for len(parts) > 1 && parts[0] == "0" {
		parts = parts[1:]
	}
	return strings.Join(parts, ".")
}
