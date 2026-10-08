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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func archive(t *testing.T, windows bool, payload []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if windows {
		z := zip.NewWriter(&b)
		w, e := z.Create("switchboard.exe")
		if e != nil {
			t.Fatal(e)
		}
		w.Write(payload)
		z.Close()
	} else {
		gz := gzip.NewWriter(&b)
		a := tar.NewWriter(gz)
		a.WriteHeader(&tar.Header{Name: "switchboard", Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len(payload))})
		a.Write(payload)
		a.Close()
		gz.Close()
	}
	return b.Bytes()
}
func TestVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		current, next string
		want          bool
	}{{"0.1.0", "v0.2.0", true}, {"v0.1.0-5-gabc123-dirty", "v0.1.0", false}, {"v0.1.0-5-gabc123", "v0.2.0", true}, {"v0.2.0-dirty", "v0.2.0", false}, {"dev", "v0.1.0", false}, {"v0.2.0", "v0.1.0", false}, {"v1.0.0-beta.1", "v1.0.0", true}} {
		if Newer(tc.current, tc.next) != tc.want {
			t.Errorf("%s -> %s", tc.current, tc.next)
		}
	}
}
func TestChecksummedPlatformDownloads(t *testing.T) {
	for _, windows := range []bool{false, true} {
		t.Run(fmt.Sprint(windows), func(t *testing.T) {
			payload := []byte("verified executable content")
			blob := archive(t, windows, payload)
			sum := sha256.Sum256(blob)
			osName, ext := "linux", "tar.gz"
			if windows {
				osName = "windows"
				ext = "zip"
			}
			name := "switchboard_0.2.0_" + osName + "_amd64." + ext
			corrupt := false
			var calls atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/test/project/releases/latest":
					calls.Add(1)
					json.NewEncoder(w).Encode(Release{Tag: "v0.2.0", URL: server.URL, Assets: []Asset{{Name: name, URL: server.URL + "/archive"}, {Name: "checksums.txt", URL: server.URL + "/checksums"}}})
				case "/archive":
					w.Write(blob)
				case "/checksums":
					if corrupt {
						fmt.Fprintf(w, "%s  %s\n", strings.Repeat("0", 64), name)
					} else {
						fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
					}
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c := &Client{HTTP: server.Client(), API: server.URL, Repo: "test/project", OS: osName, Arch: "amd64"}
			r, e := c.Check(context.Background(), "")
			if e != nil {
				t.Fatal(e)
			}
			data, e := c.Download(context.Background(), r)
			if e != nil || !bytes.Equal(data, payload) {
				t.Fatalf("download: %v %q", e, data)
			}
			target := filepath.Join(t.TempDir(), "switchboard")
			os.WriteFile(target, []byte("old executable"), 0755)
			if e = Install(target, data); e != nil {
				t.Fatal(e)
			}
			got, _ := os.ReadFile(target)
			if !bytes.Equal(got, payload) {
				t.Fatal("executable was not replaced")
			}
			corrupt = true
			if _, e = c.Download(context.Background(), r); e == nil {
				t.Fatal("corrupt download accepted")
			}
			got, _ = os.ReadFile(target)
			if !bytes.Equal(got, payload) {
				t.Fatal("corruption changed installed executable")
			}
		})
	}
}
func TestDailyCachedNotice(t *testing.T) {
	t.Setenv("SWITCHBOARD_NO_UPDATE_CHECK", "")
	t.Setenv("SWITCHBOARD_UPDATE_CACHE_DIR", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(Release{Tag: "v0.2.0"})
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), API: server.URL, Repo: "test/project"}
	for range 2 {
		r, ok := <-c.Notice(context.Background(), "v0.1.0")
		if !ok || r.Tag != "v0.2.0" {
			t.Fatal("update notice missing")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("cache did not suppress repeated network checks")
	}
	t.Setenv("SWITCHBOARD_NO_UPDATE_CHECK", "1")
	if _, ok := <-c.Notice(context.Background(), "v0.1.0"); ok {
		t.Fatal("disabled checker emitted a notice")
	}
}
func TestInstallRunningExecutable(t *testing.T) {
	source, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "self-update")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	if e = os.WriteFile(target, b, 0755); e != nil {
		t.Fatal(e)
	}
	c := exec.Command(target, "-test.run=^TestRunningUpdateHelper$")
	c.Env = append(os.Environ(), "SWITCHBOARD_HELPER_SELF_UPDATE=1")
	if out, e := c.CombinedOutput(); e != nil {
		t.Fatalf("running executable replacement: %v %s", e, out)
	}
	got, e := os.ReadFile(target)
	if e != nil || string(got) != "replacement" {
		t.Fatalf("replacement missing: %v", e)
	}
}
func TestRunningUpdateHelper(t *testing.T) {
	if os.Getenv("SWITCHBOARD_HELPER_SELF_UPDATE") != "1" {
		return
	}
	target, e := os.Executable()
	if e == nil {
		e = Install(target, []byte("replacement"))
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	os.Exit(0)
}
