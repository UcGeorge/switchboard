package core

import (
	"context"
	"testing"
	"time"
)

// The CLI changes settings by writing to the database from its own process.
// A running server must notice without a restart.
func TestSettingsChangedByAnotherProcessAreApplied(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := svc.Settings().Lease; got != 120*time.Second {
		t.Fatalf("default lease = %s, want 2m0s", got)
	}

	// A second service over the same database stands in for the CLI process.
	cli, err := New(svc.DB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if err := cli.SetSetting(ctx, KeyLease, "77"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if svc.Settings().Lease == 77*time.Second {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("running server still has lease = %s after another process set it to 77s", svc.Settings().Lease)
}

func TestSettingValidation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	for _, tc := range []struct{ key, value string }{
		{KeyLease, "soon"},
		{KeyLease, "-5"},
		{KeyAcceptAnyModel, "maybe"},
		{KeyPublicURL, "example.com"},
		{"no_such_setting", "1"},
	} {
		if err := svc.SetSetting(ctx, tc.key, tc.value); err == nil {
			t.Errorf("SetSetting(%q, %q) accepted an invalid value", tc.key, tc.value)
		}
	}
	if err := svc.SetSetting(ctx, KeyPublicURL, "https://sb.example.com/"); err != nil {
		t.Fatal(err)
	}
	if got := svc.Settings().PublicURL; got != "https://sb.example.com" {
		t.Errorf("public url = %q, want trailing slash trimmed", got)
	}
	if got := svc.BaseURL("127.0.0.1:8080", false); got != "https://sb.example.com" {
		t.Errorf("BaseURL = %q, want the configured public URL", got)
	}
}

func TestEnvironmentSettingOverridesStoredValue(t *testing.T) {
	t.Setenv("SWITCHBOARD_LEASE_SECONDS", "91")
	svc := newTestService(t)
	if svc.Settings().Lease != 91*time.Second {
		t.Fatal("environment override not applied")
	}
	if err := svc.SetSetting(context.Background(), KeyLease, "66"); err != nil {
		t.Fatal(err)
	}
	if svc.Settings().Lease != 91*time.Second {
		t.Fatal("stored setting replaced environment override")
	}
}
