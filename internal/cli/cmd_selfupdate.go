package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/selfupdate"
)

const (
	selfUpdateUnit  = paths.Cmd + "-self-update"
	updateTokenFile = paths.UpdateTokenFile
	updatesDir      = paths.UpdatesDir
)

func (a *App) updateClient() *selfupdate.Client {
	return selfupdate.NewClient(paths.Cmd+"/"+a.Version, updateTokenFile, paths.UpdateCacheFile)
}

// UpdateCheck is what `self-update --check` reports.
type UpdateCheck struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	HasToken  bool   `json:"hasToken"`
}

func (a *App) selfUpdateCmd() *cobra.Command {
	var check, background bool
	c := &cobra.Command{
		Use:   "self-update",
		Short: "Update the manager itself to its latest release",
		Long: "Downloads the latest release's package for this machine from GitHub, checks it against the release's SHA256SUMS, and installs it with apt. " +
			"Servers keep running; the web page restarts on the new version. While the repository is private, save a GitHub token first with: self-update token",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.login(); err != nil {
				return err
			}
			cl := a.updateClient()
			_, tokErr := os.Stat(updateTokenFile)
			res := UpdateCheck{Current: a.Version, HasToken: tokErr == nil}
			rel, err := cl.Latest(cmd.Context(), true)
			if err == nil {
				res.Latest = rel.Version
				res.Available = selfupdate.Newer(rel.Version, a.Version)
			}
			if check {
				if err != nil {
					res.Error = err.Error()
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(res)
				}
				switch {
				case err != nil:
					return err
				case res.Available:
					a.say("Version %s is out; this is %s. Update with: sudo %s self-update", res.Latest, res.Current, paths.Cmd)
				default:
					a.say("✓ %s is the latest version.", res.Current)
				}
				return nil
			}
			return a.logged2("self-update", func() error {
				if err != nil {
					return err
				}
				if !selfupdate.ValidVersion(a.Version) {
					return fmt.Errorf("this is a development build (%s); install a release package by hand", a.Version)
				}
				if !res.Available {
					a.say("✓ %s is the latest version.", a.Version)
					return nil
				}
				name := selfupdate.PackageName(paths.Cmd, rel.Version, runtime.GOARCH)
				a.say("Downloading %s...", name)
				_ = os.RemoveAll(updatesDir) // earlier downloads
				pkg, err := cl.Download(cmd.Context(), rel, name, updatesDir)
				if err != nil {
					return err
				}
				a.say("✓ It matches the release's SHA256SUMS.")
				args := selfupdate.InstallArgs(selfUpdateUnit, pkg, !background)
				if background {
					// From the web page: the install outlives this job, since
					// it restarts the web service.
					if _, err := a.runner.Run(cmd.Context(), run.Cmd{Name: args[0], Args: args[1:]}); err != nil {
						return fmt.Errorf("start the install: %w", err)
					}
					a.say("✓ Installing %s. The web page restarts on the new version in a minute; this page reloads by itself.", rel.Version)
					return nil
				}
				a.say("Installing %s...", rel.Version)
				out, err := a.runner.Run(cmd.Context(), run.Cmd{Name: args[0], Args: args[1:], Timeout: 15 * time.Minute})
				if err != nil {
					fmt.Fprint(a.Err, string(out.Stdout))
					return fmt.Errorf("install %s: %w", name, err)
				}
				a.say("✓ Updated to %s. The web page restarted on it; servers kept running.", rel.Version)
				return nil
			})
		},
	}
	c.Flags().BoolVar(&check, "check", false, "only look for a newer version")
	c.Flags().BoolVar(&background, "background", false, "start the install and return (used by the web page)")
	_ = c.Flags().MarkHidden("background")

	var clear bool
	token := &cobra.Command{
		Use:   "token",
		Short: "Save a GitHub token for updates while the repository is private",
		Long: "Make a fine-grained token at github.com/settings/personal-access-tokens with read-only \"Contents\" access to the manager's repository only, and paste it here. " +
			"It's kept in " + updateTokenFile + ", readable by root only. --clear removes it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.login(); err != nil {
				return err
			}
			return a.logged2("self-update token", func() error {
				if clear {
					if err := os.Remove(updateTokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
					a.say("✓ Token removed.")
					return nil
				}
				tok, err := a.term.passphrase("GitHub token: ")
				if err != nil {
					return err
				}
				tok = strings.TrimSpace(tok)
				if tok == "" || strings.ContainsAny(tok, " \t") {
					return errors.New("that isn't a token")
				}
				if err := fsutil.WriteFileAtomic(updateTokenFile, []byte(tok+"\n"), 0o600, nil); err != nil {
					return err
				}
				if rel, err := a.updateClient().Latest(cmd.Context(), true); err != nil {
					a.warn("Saved, but GitHub refused it: %v", err)
				} else {
					a.say("✓ Saved. The latest release is %s.", rel.Version)
				}
				return nil
			})
		},
	}
	token.Flags().BoolVar(&clear, "clear", false, "remove the saved token")
	c.AddCommand(token)
	return c
}

// logged2 writes a command without a server to the action log.
func (a *App) logged2(action string, fn func() error) error {
	err := fn()
	a.log.Action(action, err)
	return err
}
