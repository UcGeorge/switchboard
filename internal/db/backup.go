package db

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// Backup creates a SQLite snapshot or a PostgreSQL custom-format dump. PostgreSQL
// credentials are supplied as environment variables, never process arguments.
func Backup(ctx context.Context, d *sql.DB, source, destination string) error {
	if !IsPostgres(d) {
		_, e := d.ExecContext(ctx, "VACUUM INTO ?", destination)
		return e
	}
	cfg, e := pgx.ParseConfig(source)
	if e != nil {
		return fmt.Errorf("invalid PostgreSQL backup URL")
	}
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom")
	vars := map[string]string{"PGHOST": cfg.Host, "PGPORT": fmt.Sprint(cfg.Port), "PGDATABASE": cfg.Database, "PGUSER": cfg.User}
	vars["PGPASSWORD"] = cfg.Password
	if vars["PGPORT"] == "" {
		vars["PGPORT"] = "5432"
	}
	u, _ := url.Parse(source)
	for k, v := range u.Query() {
		switch k {
		case "sslmode", "sslrootcert", "sslcert", "sslkey":
			vars["PG"+strings.ToUpper(k)] = v[0]
		}
	}
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if !strings.HasPrefix(k, "PG") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return e
	}
	var stderr bytes.Buffer
	cmd.Stdout = f
	cmd.Stderr = &stderr
	e = cmd.Run()
	closeErr := f.Close()
	if e != nil {
		os.Remove(destination)
		msg := strings.ReplaceAll(stderr.String(), source, "[database]")
		if pw := vars["PGPASSWORD"]; pw != "" {
			msg = strings.ReplaceAll(msg, pw, "[redacted]")
		}
		return fmt.Errorf("PostgreSQL backup (use pg_dump matching the server major version): %w: %s", e, strings.TrimSpace(msg))
	}
	return closeErr
}
