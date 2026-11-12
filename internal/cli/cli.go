// Package cli is the command tree. Commands parse flags and call
// internal/ops; the menu calls the same functions.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/actionlog"
	"github.com/GenericD00d/shiver-server-manager/internal/auth"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/sysenv"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitAuth    = 3 // login or vault unlock needed
	ExitLocked  = 4 // another run holds the lock
	ExitPartial = 5 // stopped partway; the operation is saved for repair
)

// ExitError carries an exit code with an error.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string { return e.Err.Error() }
func (e *ExitCodeError) Unwrap() error { return e.Err }

func usageErr(format string, a ...any) error {
	return &ExitCodeError{Code: ExitUsage, Err: fmt.Errorf(format, a...)}
}

// Globals are the flags every command takes.
type Globals struct {
	JSON            bool
	Yes             bool
	PasswordStdin   bool
	PassphraseStdin bool
	// ActingUser is the admin a web request runs for (hidden; honoured only
	// when root runs the manager directly, as the web service does).
	ActingUser string
}

// App is what commands share.
type App struct {
	Version string
	Env     sysenv.Info
	Globals Globals
	Out     io.Writer
	Err     io.Writer
	In      *os.File // stdin; prompts read it as a terminal or line by line

	log     *actionlog.Logger
	runner  run.Runner
	session *auth.Session
	term    *termIO
}

// Main runs the manager and returns its exit code.
func Main(version string, args []string) int {
	env := sysenv.Setup()
	app := &App{Version: version, Env: env, Out: os.Stdout, Err: os.Stderr, In: os.Stdin}
	return app.Execute(args)
}

func (a *App) Execute(args []string) int {
	root := a.newRoot()
	root.SetArgs(args)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	code := exitCode(err)
	if a.Globals.JSON {
		json.NewEncoder(a.Err).Encode(map[string]any{"error": err.Error(), "exitCode": code})
	} else {
		fmt.Fprintln(a.Err, "Error:", err)
	}
	return code
}

func exitCode(err error) int {
	var ce *ExitCodeError
	var held *state.HeldError
	var throttled *auth.ThrottledError
	switch {
	case errors.As(err, &ce):
		return ce.Code
	case errors.As(err, &held):
		return ExitLocked
	case ops.IsPartial(err):
		return ExitPartial
	case errors.Is(err, auth.ErrBadLogin), errors.Is(err, auth.ErrNotAdmin), errors.As(err, &throttled),
		errors.Is(err, vault.ErrWrongPassphrase), errors.Is(err, vault.ErrWrongRecoveryKey),
		errors.Is(err, vault.ErrBadRecoveryFormat), errors.Is(err, errNoTerminal):
		return ExitAuth
	// cobra's own argument and command errors have no type to match.
	case strings.HasPrefix(err.Error(), "unknown command"),
		strings.HasPrefix(err.Error(), "accepts "),
		strings.HasPrefix(err.Error(), "requires at least"),
		strings.HasPrefix(err.Error(), "unknown flag"),
		strings.HasPrefix(err.Error(), "unknown shorthand flag"):
		return ExitUsage
	}
	return ExitError
}

// noRootNeeded lists commands that work as any user and on any OS. The
// internal ones go further and refuse root: see refuseRoot.
var noRootNeeded = map[string]bool{"version": true, "help": true, "completion": true, "internal-vacuum": true, "internal-enable-plugins": true, "internal-set-token": true}

// isRoot is sysenv.IsRoot, swappable in tests.
var isRoot = sysenv.IsRoot

// refuseRoot keeps the hidden internal-* commands out of reach through the
// sudoers rule. The manager only starts them itself, as a server's own user
// (ops.runSelfAs); run as root, they would read and write any path the
// caller names, without the admin login.
func refuseRoot() error {
	if isRoot() {
		return usageErr("internal commands only run as a server's own user, started by %s itself", paths.Cmd)
	}
	return nil
}

func (a *App) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           paths.Cmd,
		Short:         paths.AppName + ": set up and run Sharkord servers",
		Long:          paths.AppName + " sets up, runs, backs up, imports, exports and deletes Sharkord servers on this machine.\nRun it with no arguments to open the menu.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if noRootNeeded[cmd.Name()] {
				return nil
			}
			if runtime.GOOS != "linux" {
				return fmt.Errorf("%s only runs on Linux", paths.Cmd)
			}
			if !sysenv.IsRoot() {
				return fmt.Errorf("%s needs root; run it with sudo: sudo %s", paths.Cmd, paths.Cmd)
			}
			if err := sysenv.CheckOwnership(); err != nil {
				return err
			}
			a.log = actionlog.New(paths.LogFile)
			if a.session != nil { // a command run from the menu
				a.log.User, a.log.SSHUser = a.session.User, a.session.SSHUser
			}
			exec := &run.Exec{Log: a.log.Line}
			a.runner = exec
			a.term = newTermIO(a, exec)
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runMenu()
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitCodeError{Code: ExitUsage, Err: err}
	})
	pf := root.PersistentFlags()
	pf.BoolVar(&a.Globals.JSON, "json", false, "machine-readable output, no colour")
	pf.BoolVar(&a.Globals.Yes, "yes", false, "skip confirmations (never the login or the password check before showing a token)")
	pf.BoolVar(&a.Globals.PasswordStdin, "password-stdin", false, "read the login password from stdin")
	pf.BoolVar(&a.Globals.PassphraseStdin, "passphrase-stdin", false, "read the vault or export passphrase from stdin")
	pf.StringVar(&a.Globals.ActingUser, "acting-user", "", "the logged-in web admin, for the log")
	_ = pf.MarkHidden("acting-user")

	root.AddCommand(a.versionCmd())
	a.addCommands(root)
	return root
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show the manager's version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := map[string]string{
				"version": a.Version,
				"go":      runtime.Version(),
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
			}
			if a.Globals.JSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s, %s/%s)\n", paths.Cmd, a.Version, info["go"], info["os"], info["arch"])
			return err
		},
	}
}
