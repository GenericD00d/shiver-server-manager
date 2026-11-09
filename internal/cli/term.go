package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

var errNoTerminal = errors.New("no terminal to ask on; pass --password-stdin and/or --passphrase-stdin and send the answers on stdin, one per line (password first)")

// termIO asks questions on the terminal, or reads answers line by line from
// stdin when --password-stdin or --passphrase-stdin is given. Everything
// typed in secret is registered with the runner for redaction.
type termIO struct {
	a       *App
	in      *bufio.Reader
	secrets *run.Exec
}

func newTermIO(a *App, secrets *run.Exec) *termIO {
	return &termIO{a: a, in: bufio.NewReader(a.In), secrets: secrets}
}

func (t *termIO) isTerminal() bool { return t.a.In != nil && term.IsTerminal(int(t.a.In.Fd())) }

func (t *termIO) readLine() (string, error) {
	line, err := t.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		if errors.Is(err, io.EOF) {
			return "", errors.New("stdin ended before all answers were read")
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// hidden asks without echo. fromStdin reads the next stdin line instead.
func (t *termIO) hidden(msg string, fromStdin bool) (string, error) {
	var s string
	switch {
	case fromStdin:
		line, err := t.readLine()
		if err != nil {
			return "", err
		}
		s = line
	case t.isTerminal():
		fmt.Fprint(t.a.Err, msg)
		b, err := term.ReadPassword(int(t.a.In.Fd()))
		fmt.Fprintln(t.a.Err)
		if err != nil {
			return "", err
		}
		s = string(b)
		clear(b)
	default:
		return "", errNoTerminal
	}
	if t.secrets != nil {
		t.secrets.AddSecret([]byte(s))
	}
	return s, nil
}

func (t *termIO) visible(msg string, fromStdin bool) (string, error) {
	if !fromStdin && !t.isTerminal() {
		return "", errNoTerminal
	}
	if !fromStdin {
		fmt.Fprint(t.a.Err, msg)
	}
	return t.readLine()
}

// passphrase asks for an existing vault passphrase.
func (t *termIO) passphrase(msg string) (string, error) {
	return t.hidden(msg, t.a.Globals.PassphraseStdin)
}

// newPassphrase asks for a new passphrase, twice on a terminal.
func (t *termIO) newPassphrase(what string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		p, err := t.passphrase("New " + what + " (at least 12 characters): ")
		if err != nil {
			return "", err
		}
		if err := vault.CheckPassphrase(p); err != nil {
			if t.a.Globals.PassphraseStdin {
				return "", err
			}
			fmt.Fprintln(t.a.Err, err)
			continue
		}
		if t.a.Globals.PassphraseStdin {
			return p, nil
		}
		again, err := t.passphrase("Type it again: ")
		if err != nil {
			return "", err
		}
		if again == p {
			return p, nil
		}
		fmt.Fprintln(t.a.Err, "The two didn't match.")
	}
	return "", errors.New("no passphrase set")
}

// confirm asks a yes/no question; --yes answers yes.
func (t *termIO) confirm(question string) (bool, error) {
	if t.a.Globals.Yes {
		return true, nil
	}
	if !t.isTerminal() {
		return false, errors.New("confirmation needed: run it in a terminal, or pass --yes")
	}
	fmt.Fprintf(t.a.Err, "%s [y/N] ", question)
	ans, err := t.readLine()
	if err != nil {
		return false, err
	}
	ans = strings.ToLower(strings.TrimSpace(ans))
	return ans == "y" || ans == "yes", nil
}

// clearScreen wipes the terminal and its scrollback, after showing a secret.
func (t *termIO) clearScreen() {
	if t.isTerminal() && term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprint(t.a.Out, "\033[H\033[2J\033[3J")
	}
}

// pamPrompter forwards PAM's questions to the terminal or stdin.
type pamPrompter struct{ t *termIO }

func (p pamPrompter) Hidden(msg string) (string, error) {
	return p.t.hidden(msg, p.t.a.Globals.PasswordStdin)
}
func (p pamPrompter) Visible(msg string) (string, error) {
	return p.t.visible(msg, p.t.a.Globals.PasswordStdin)
}
func (p pamPrompter) Info(msg string)  { fmt.Fprintln(p.t.a.Err, msg) }
func (p pamPrompter) Error(msg string) { fmt.Fprintln(p.t.a.Err, msg) }
