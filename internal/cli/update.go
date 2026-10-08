package cli

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/ucgeorge/switchboard/internal/updater"
	"github.com/ucgeorge/switchboard/internal/version"
	"os"
)

func updateCmd() *cobra.Command {
	var check, force bool
	var tag string
	cmd := &cobra.Command{Use: "update", Short: "Check for or install a checksum-verified release of this local executable", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		current := version.Effective()
		client := updater.New()
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		r, e := client.Check(ctx, tag)
		if e != nil {
			return e
		}
		known := updater.BaseVersion(current) != ""
		newer := updater.Newer(current, r.Tag)
		if check {
			if g.json {
				return printJSON(map[string]any{"current": current, "available": r.Tag, "update_available": newer, "current_version_known": known, "release_url": r.URL})
			}
			fmt.Printf("Installed: %s\nAvailable: %s\n", current, r.Tag)
			if newer {
				fmt.Println("Run `switchboard update` to install.")
			} else if !known {
				fmt.Println("Local source version is unknown; use --force to replace it with a release.")
			} else {
				fmt.Println("No newer release available.")
			}
			return nil
		}
		if !known && tag == "" && !force {
			return fmt.Errorf("cannot compare this source build to a release; use update --check or explicitly opt in with --force")
		}
		if known && !newer && tag == "" && !force {
			if g.json {
				return printJSON(map[string]any{"updated": false, "current": current, "available": r.Tag})
			}
			fmt.Println("No newer release available; executable unchanged.")
			return nil
		}
		binary, e := client.Download(ctx, r)
		if e != nil {
			return e
		}
		path, e := os.Executable()
		if e != nil {
			return e
		}
		if e = updater.Install(path, binary); e != nil {
			return e
		}
		if g.json {
			return printJSON(map[string]any{"updated": true, "previous": current, "version": r.Tag, "path": path})
		}
		fmt.Printf("Updated %s to %s. Restart any running Switchboard server to use the new binary.\n", path, r.Tag)
		return nil
	}}
	cmd.Flags().BoolVar(&check, "check", false, "check without downloading or replacing the executable")
	cmd.Flags().StringVar(&tag, "version", "", "install a specific release (also allows an explicit downgrade)")
	cmd.Flags().BoolVar(&force, "force", false, "replace a source build or reinstall the selected release")
	return cmd
}
