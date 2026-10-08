// Package updater discovers release updates and verifies/install binaries.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const maxArchive = 128 << 20

var sourceVersion = regexp.MustCompile(`^(v?[0-9]+\.[0-9]+\.[0-9]+)-[0-9]+-g[0-9a-f]+(?:-dirty)?$`)
var validRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	URL        string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}
type Client struct {
	HTTP *http.Client
	API  string
	Repo string
	OS   string
	Arch string
}

func New() *Client {
	repo := os.Getenv("SWITCHBOARD_REPO")
	if repo == "" {
		repo = "UcGeorge/switchboard"
	}
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, API: "https://api.github.com", Repo: repo, OS: runtime.GOOS, Arch: runtime.GOARCH}
}
func BaseVersion(v string) string {
	v = strings.TrimSpace(v)
	if m := sourceVersion.FindStringSubmatch(v); m != nil {
		v = m[1]
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}
func Newer(current, latest string) bool {
	a, b := BaseVersion(current), BaseVersion(latest)
	return a != "" && b != "" && semver.Compare(b, a) > 0
}
func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "switchboard-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	r, e := c.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("release request returned HTTP %d", r.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("release download exceeds size limit")
	}
	return b, nil
}
func (c *Client) Check(ctx context.Context, tag string) (Release, error) {
	if !validRepo.MatchString(c.Repo) {
		return Release{}, fmt.Errorf("invalid SWITCHBOARD_REPO")
	}
	endpoint := c.API + "/repos/" + c.Repo + "/releases/latest"
	if tag != "" {
		if !strings.HasPrefix(tag, "v") {
			tag = "v" + tag
		}
		if !semver.IsValid(tag) {
			return Release{}, fmt.Errorf("invalid release version")
		}
		endpoint = c.API + "/repos/" + c.Repo + "/releases/tags/" + tag
	}
	b, e := c.get(ctx, endpoint, 1<<20)
	if e != nil {
		return Release{}, e
	}
	var r Release
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if BaseVersion(r.Tag) == "" || r.Draft {
		return r, fmt.Errorf("invalid release metadata")
	}
	if tag == "" && r.Prerelease {
		return r, fmt.Errorf("latest release is marked prerelease")
	}
	return r, nil
}
func (c *Client) Download(ctx context.Context, r Release) ([]byte, error) {
	ext := "tar.gz"
	if c.OS == "windows" {
		ext = "zip"
	}
	name := "switchboard_" + strings.TrimPrefix(r.Tag, "v") + "_" + c.OS + "_" + c.Arch + "." + ext
	var archiveURL, checksumsURL string
	for _, a := range r.Assets {
		if a.Name == name {
			if archiveURL != "" {
				return nil, fmt.Errorf("duplicate release asset")
			}
			archiveURL = a.URL
		}
		if a.Name == "checksums.txt" {
			checksumsURL = a.URL
		}
	}
	if archiveURL == "" || checksumsURL == "" {
		return nil, fmt.Errorf("release %s has no checksummed asset for %s/%s", r.Tag, c.OS, c.Arch)
	}
	manifest, e := c.get(ctx, checksumsURL, 1<<20)
	if e != nil {
		return nil, e
	}
	expected := ""
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			if expected != "" {
				return nil, fmt.Errorf("duplicate checksum entry")
			}
			expected = fields[0]
		}
	}
	digest, e := hex.DecodeString(expected)
	if e != nil || len(digest) != 32 {
		return nil, fmt.Errorf("release checksum missing or invalid")
	}
	archive, e := c.get(ctx, archiveURL, maxArchive)
	if e != nil {
		return nil, e
	}
	actual := sha256.Sum256(archive)
	if !bytes.Equal(digest, actual[:]) {
		return nil, fmt.Errorf("checksum mismatch: executable left unchanged")
	}
	binary := "switchboard"
	if c.OS == "windows" {
		binary += ".exe"
	}
	if c.OS == "windows" {
		z, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if e != nil {
			return nil, e
		}
		var data []byte
		for _, f := range z.File {
			if f.Name != binary {
				continue
			}
			if data != nil || !f.Mode().IsRegular() {
				return nil, fmt.Errorf("invalid binary entry")
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			data, e = io.ReadAll(io.LimitReader(r, maxArchive+1))
			r.Close()
			if e != nil {
				return nil, e
			}
		}
		if len(data) == 0 || len(data) > maxArchive {
			return nil, fmt.Errorf("release binary missing or too large")
		}
		return data, nil
	}
	gz, e := gzip.NewReader(bytes.NewReader(archive))
	if e != nil {
		return nil, e
	}
	defer gz.Close()
	tarReader := tar.NewReader(gz)
	var data []byte
	for {
		h, e := tarReader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if h.Name != binary {
			continue
		}
		if data != nil || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > maxArchive {
			return nil, fmt.Errorf("invalid release binary")
		}
		data, e = io.ReadAll(io.LimitReader(tarReader, maxArchive+1))
		if e != nil {
			return nil, e
		}
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("release binary missing")
	}
	return data, nil
}

// Install writes a candidate next to the real executable before replacing it.
// Unix replacement is atomic. Windows keeps a rollback copy through the swap.
func Install(executable string, data []byte) error {
	target, e := filepath.EvalSymlinks(executable)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(target), ".switchboard-update-*")
	if e != nil {
		return fmt.Errorf("cannot write executable directory; use a user-writable install location: %w", e)
	}
	candidate := f.Name()
	defer os.Remove(candidate)
	if e = f.Chmod(0o755); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if runtime.GOOS != "windows" {
		return os.Rename(candidate, target)
	}
	backup := target + ".old-" + strconv.Itoa(os.Getpid())
	if e = os.Rename(target, backup); e != nil {
		return fmt.Errorf("cannot move running executable: %w", e)
	}
	if e = os.Rename(candidate, target); e != nil {
		if restore := os.Rename(backup, target); restore != nil {
			return fmt.Errorf("update failed (%v); restore failed (%v); original is at %s", e, restore, backup)
		}
		return e
	}
	// Windows may retain the running original until this process exits.
	_ = os.Remove(backup)
	return nil
}
