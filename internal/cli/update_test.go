package cli

import (
	"github.com/spf13/cobra"
	"testing"
)

func TestUpdateIsLocalWithRemoteConfiguration(t *testing.T) {
	old := g
	defer func() { g = old }()
	g.url = "https://remote.example.com"
	g.databaseURL = "postgresql://localhost/app"
	if e := remotePreRun(&cobra.Command{Use: "update"}, nil); e != nil {
		t.Fatal(e)
	}
}
