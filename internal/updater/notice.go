package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type cachedCheck struct {
	Repo      string    `json:"repo"`
	Tag       string    `json:"tag"`
	URL       string    `json:"url"`
	CheckedAt time.Time `json:"checked_at"`
}

func (c *Client) cachePath() string {
	dir := os.Getenv("SWITCHBOARD_UPDATE_CACHE_DIR")
	if dir == "" {
		var e error
		dir, e = os.UserCacheDir()
		if e != nil {
			return ""
		}
		dir = filepath.Join(dir, "switchboard")
	}
	hash := sha256.Sum256([]byte(c.Repo))
	return filepath.Join(dir, "update-"+hex.EncodeToString(hash[:6])+".json")
}

// Notice performs an opportunistic daily release check. It never delays a
// command or installs anything. Failure to contact GitHub is silent.
func (c *Client) Notice(ctx context.Context, current string) <-chan Release {
	out := make(chan Release, 1)
	disabled := strings.ToLower(os.Getenv("SWITCHBOARD_NO_UPDATE_CHECK"))
	if disabled == "1" || disabled == "true" || disabled == "yes" || BaseVersion(current) == "" {
		close(out)
		return out
	}
	go func() {
		defer close(out)
		path := c.cachePath()
		var cache cachedCheck
		if b, e := os.ReadFile(path); e == nil {
			json.Unmarshal(b, &cache)
		}
		if cache.Repo == c.Repo && !cache.CheckedAt.IsZero() && time.Since(cache.CheckedAt) < 24*time.Hour && time.Since(cache.CheckedAt) >= 0 {
			if Newer(current, cache.Tag) {
				out <- Release{Tag: cache.Tag, URL: cache.URL}
			}
			return
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		r, e := c.Check(ctx, "")
		if e != nil {
			return
		}
		cache = cachedCheck{Repo: c.Repo, Tag: r.Tag, URL: r.URL, CheckedAt: time.Now()}
		b, _ := json.Marshal(cache)
		if path != "" && os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			f, e := os.CreateTemp(filepath.Dir(path), "update-cache-*")
			if e == nil {
				name := f.Name()
				f.Chmod(0o600)
				_, e = f.Write(b)
				f.Close()
				if e == nil {
					os.Rename(name, path)
				}
				os.Remove(name)
			}
		}
		if Newer(current, r.Tag) {
			out <- r
		}
	}()
	return out
}
