package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/ucgeorge/switchboard/internal/control"
	"github.com/ucgeorge/switchboard/internal/core"
	"github.com/ucgeorge/switchboard/internal/db/sqlcgen"
	"github.com/ucgeorge/switchboard/internal/secret"
)

func remoteBase() (string, error) {
	u, err := url.Parse(g.url)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("--url must be an HTTP(S) instance base URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
func remoteHTTP(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	base, err := remoteBase()
	if err != nil {
		return nil, err
	}
	if g.adminToken == "" {
		return nil, fmt.Errorf("remote administration requires --admin-token or SWITCHBOARD_ADMIN_TOKEN; run auth create-admin-token on the server")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.adminToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 16 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("remote instance: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}
func remoteCall(ctx context.Context, args []string) (control.Result, error) {
	b, _ := json.Marshal(control.Request{Args: args})
	resp, err := remoteHTTP(ctx, "POST", "/admin/commands", bytes.NewReader(b))
	if err != nil {
		return control.Result{}, err
	}
	defer resp.Body.Close()
	var result control.Result
	err = json.NewDecoder(io.LimitReader(resp.Body, 34<<20)).Decode(&result)
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("remote command failed: %s", strings.TrimSpace(result.Stderr))
	}
	return result, err
}
func remotePreRun(cmd *cobra.Command, args []string) error {
	if g.url == "" {
		return nil
	}
	if _, err := remoteBase(); err != nil {
		return err
	}
	if g.dbPath != "" || g.databaseURL != "" {
		return fmt.Errorf("local database selection (--db/--database-url) cannot be combined with --url")
	}
	path := strings.Fields(cmd.CommandPath())
	path = path[1:]
	if len(path) == 0 || path[0] == "serve" {
		return fmt.Errorf("serve runs locally; omit --url")
	}
	switch path[0] {
	case "mcp", "skill", "version", "completion", "help":
		return nil
	}
	if path[0] == "auth" && path[1] == "create-admin-token" {
		return fmt.Errorf("create-admin-token must run locally on the server")
	}
	if path[0] == "db" && path[1] == "backup" {
		cmd.RunE = func(c *cobra.Command, a []string) error { return remoteBackup(c.Context(), a[0]) }
		return nil
	}
	if path[0] == "events" && path[1] == "tail" {
		cmd.RunE = func(c *cobra.Command, a []string) error { return remoteTail(c.Context()) }
		return nil
	}
	argv := append([]string{}, path...)
	argv = append(argv, args...)
	cmd.Flags().Visit(func(f *pflag.Flag) {
		switch f.Name {
		case "url", "admin-token", "database-url", "db", "addr", "file", "force":
			if f.Name == "force" {
				argv = append(argv, "--force="+f.Value.String())
			}
			return
		}
		argv = append(argv, "--"+f.Name+"="+f.Value.String())
	})
	if path[0] == "requests" && path[1] == "answer" {
		text, _ := cmd.Flags().GetString("text")
		file, _ := cmd.Flags().GetString("file")
		if file != "" {
			b, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			text = string(b)
		} else if text == "" {
			b, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
			if err != nil {
				return err
			}
			text = string(b)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("empty answer")
		}
		if !cmd.Flags().Changed("text") || file != "" {
			argv = append(argv, "--text="+text)
		}
	}
	if path[0] == "auth" && path[1] == "set-password" && !cmd.Flags().Changed("password") {
		return fmt.Errorf("remote password change requires --password")
	}
	if !control.Allowed(argv) {
		return fmt.Errorf("command is not available remotely")
	}
	result, err := remoteCall(cmd.Context(), argv)
	if err != nil {
		return err
	}
	fmt.Fprint(os.Stdout, result.Stdout)
	fmt.Fprint(os.Stderr, result.Stderr)
	cmd.Run = nil
	cmd.RunE = func(*cobra.Command, []string) error { return nil }
	return nil
}
func remoteBackup(ctx context.Context, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(path)
		}
	}()
	resp, err := remoteHTTP(ctx, "GET", "/admin/backup", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err = io.Copy(f, resp.Body); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	success = true
	fmt.Println("backup written to", path)
	return nil
}
func remoteTail(ctx context.Context) error {
	var last int64
	for {
		res, err := remoteCall(ctx, []string{"events", "list", "--json", "--limit=200"})
		if err != nil {
			return err
		}
		var events []sqlcgen.Event
		if err = json.Unmarshal([]byte(res.Stdout), &events); err != nil {
			return err
		}
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			if e.ID > last {
				fmt.Printf("%s  %-5s %-24s %s\n", time.UnixMilli(e.Ts).Format("15:04:05"), e.Level, e.Kind, e.Message)
				last = e.ID
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}
func adminTokenCmd() *cobra.Command {
	return &cobra.Command{Use: "create-admin-token", Short: "Create or rotate the remote administration token (server-local only)", RunE: withService(func(ctx context.Context, svc *core.Service) error {
		plain, hash, _ := secret.NewToken("sbc_", 48)
		if err := svc.Q.SetSetting(ctx, sqlcgen.SetSettingParams{Key: control.TokenHashSetting, Value: hash, UpdatedAt: time.Now().UnixMilli()}); err != nil {
			return err
		}
		if g.json {
			return printJSON(map[string]string{"token": plain})
		}
		fmt.Println("Administration token (shown once; replaces any previous token):\n" + plain)
		return nil
	})}
}
