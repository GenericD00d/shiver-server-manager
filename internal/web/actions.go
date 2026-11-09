package web

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// Params are an action's inputs, as the browser sent them.
type Params map[string]string

// errs collects the first problem with the inputs.
type checker struct {
	p   Params
	err error
}

func (c *checker) fail(name, msg string) string {
	if c.err == nil {
		c.err = fmt.Errorf("%s: %s", name, msg)
	}
	return ""
}

func (c *checker) raw(name string) string { return strings.TrimSpace(c.p[name]) }

// line is one line of text with no control characters, not starting with
// "-" (so it can never become a flag).
func (c *checker) line(name string, required bool) string {
	v := c.raw(name)
	switch {
	case v == "" && required:
		return c.fail(name, "required")
	case strings.HasPrefix(v, "-"):
		return c.fail(name, "can't start with -")
	case strings.IndexFunc(v, unicode.IsControl) >= 0:
		return c.fail(name, "has control characters")
	case len(v) > 1024:
		return c.fail(name, "too long")
	}
	return v
}

func (c *checker) id(name string) string {
	v := c.raw(name)
	if err := validate.InstanceID(v); err != nil {
		return c.fail(name, err.Error())
	}
	return v
}

// idOrAll accepts a server ID or "--all".
func (c *checker) idOrAll(name string) string {
	if c.raw(name) == "--all" {
		return "--all"
	}
	return c.id(name)
}

func (c *checker) domain(name string) string {
	v, err := validate.Domain(c.raw(name))
	if err != nil {
		return c.fail(name, err.Error())
	}
	return v
}

func (c *checker) absPath(name string, required bool) string {
	v := c.raw(name)
	if v == "" && !required {
		return ""
	}
	if !path.IsAbs(v) || strings.ContainsAny(v, "\x00\n\r") {
		return c.fail(name, "an absolute path, starting with /")
	}
	return path.Clean(v)
}

func (c *checker) number(name string) string {
	v := c.raw(name)
	if v == "" {
		v = "0"
	}
	if n, err := strconv.Atoi(v); err != nil || n < 0 || n > 1<<20 {
		return c.fail(name, "a whole number")
	}
	return v
}

func (c *checker) flag(name string) bool { return c.raw(name) == "true" }

func (c *checker) enum(name string, options ...string) string {
	v := c.raw(name)
	for _, o := range options {
		if v == o {
			return v
		}
	}
	return c.fail(name, "one of "+strings.Join(options, ", "))
}

func (c *checker) username(name string) string {
	v := c.raw(name)
	if err := validate.Username(v); err != nil {
		return c.fail(name, err.Error())
	}
	return v
}

// secret is a passphrase or token: one line, any characters but newlines.
func (c *checker) secret(name string, required bool) string {
	v := c.p[name]
	if strings.ContainsAny(v, "\r\n") {
		return c.fail(name, "can't contain line breaks")
	}
	if v == "" && required {
		return c.fail(name, "required")
	}
	return v
}

// Action is one thing the web page can do.
type Action struct {
	// Build returns the command's arguments and its stdin answers, in the
	// order the command asks for them.
	Build func(c *checker) (args, stdin []string)
	// Reauth asks for the admin's password again first.
	Reauth bool
	Title  string
}

func settingName(c *checker, name string) string {
	v := strings.ToUpper(c.raw(name))
	if !strings.HasPrefix(v, "SHARKORD_") {
		v = "SHARKORD_" + v
	}
	if _, ok := sharkord.FindSetting(v); !ok {
		return c.fail(name, "not a setting the manager offers")
	}
	return v
}

func when(cond bool, args ...string) []string {
	if cond {
		return args
	}
	return nil
}

// Actions are everything the web page can run.
var Actions = map[string]Action{
	"server.create": {Title: "Create a server", Build: func(c *checker) ([]string, []string) {
		args := []string{"instance", "create", "--id", c.id("id"), "--domain", c.domain("domain"), "--tls", c.enum("tls", "letsencrypt", "selfsigned")}
		args = append(args, when(!c.flag("encrypt"), "--no-encrypt")...)
		args = append(args, when(c.flag("testMode"), "--test-mode")...)
		if loc := c.absPath("location", false); loc != "" {
			args = append(args, "--location", loc)
		}
		return args, nil
	}},
	"server.import": {Title: "Import a server", Build: func(c *checker) ([]string, []string) {
		args := []string{"import", c.line("source", true), "--id", c.id("id"), "--domain", c.domain("domain")}
		args = append(args, when(!c.flag("encrypt"), "--no-encrypt")...)
		if t := c.raw("tls"); t != "" {
			args = append(args, "--tls", c.enum("tls", "letsencrypt", "selfsigned"))
		}
		if loc := c.absPath("location", false); loc != "" {
			args = append(args, "--location", loc)
		}
		// The archive's passphrase (or the vault's), then the old token.
		return args, []string{c.secret("passphrase", false), c.secret("token", false)}
	}},
	"server.export": {Title: "Export a server", Build: func(c *checker) ([]string, []string) {
		withToken := c.flag("includeToken")
		args := []string{"export", c.id("id"), "--out", c.absPath("out", true)}
		args = append(args, when(withToken, "--include-token")...)
		stdin := when(withToken, c.secret("passphrase", true))
		return args, append(stdin, c.secret("exportPassphrase", true))
	}},
	"server.start":   {Title: "Start", Build: func(c *checker) ([]string, []string) { return []string{"instance", "start", c.idOrAll("id")}, nil }},
	"server.stop":    {Title: "Stop", Build: func(c *checker) ([]string, []string) { return []string{"instance", "stop", c.idOrAll("id")}, nil }},
	"server.restart": {Title: "Restart", Build: func(c *checker) ([]string, []string) { return []string{"instance", "restart", c.idOrAll("id")}, nil }},
	"server.set": {Title: "Change a setting", Build: func(c *checker) ([]string, []string) {
		return []string{"instance", "set", c.id("id"), settingName(c, "key") + "=" + c.line("value", false)}, nil
	}},
	"server.update": {Title: "Update Sharkord", Build: func(c *checker) ([]string, []string) {
		return []string{"instance", "update", c.id("id")}, []string{c.secret("passphrase", true)}
	}},
	"server.backup": {Title: "Back up", Build: func(c *checker) ([]string, []string) { return []string{"backup", c.idOrAll("id")}, nil }},
	"server.restore": {Title: "Restore a backup", Reauth: true, Build: func(c *checker) ([]string, []string) {
		return []string{"restore", c.id("id"), c.absPath("file", true)}, []string{c.secret("passphrase", true)}
	}},
	"server.domain": {Title: "Change domain", Build: func(c *checker) ([]string, []string) {
		return append([]string{"instance", "domain", c.id("id"), c.domain("domain")}, when(c.flag("force"), "--force")...), nil
	}},
	"server.limits": {Title: "Limit memory and CPU", Build: func(c *checker) ([]string, []string) {
		return []string{"instance", "limits", c.id("id"), "--memory", c.number("memory"), "--cpu", c.number("cpu")}, nil
	}},
	"server.grow": {Title: "Grow storage", Build: func(c *checker) ([]string, []string) {
		return []string{"instance", "grow", c.id("id"), "--size", c.number("size")}, []string{c.secret("passphrase", true)}
	}},
	"server.move": {Title: "Move a server", Build: func(c *checker) ([]string, []string) {
		return []string{"instance", "move", c.id("id"), "--to", c.absPath("to", true)}, []string{c.secret("passphrase", false)}
	}},
	"server.delete": {Title: "Delete a server", Reauth: true, Build: func(c *checker) ([]string, []string) {
		id := c.id("id")
		if c.raw("confirmId") != id {
			c.fail("confirmId", "type the server's ID to confirm")
		}
		return []string{"instance", "delete", id}, []string{id}
	}},
	"shiver.install": {Title: "Install Shiver", Build: func(c *checker) ([]string, []string) { return []string{"shiver", "install", c.id("id")}, nil }},
	"shiver.update":  {Title: "Update Shiver", Build: func(c *checker) ([]string, []string) { return []string{"shiver", "update", c.idOrAll("id")}, nil }},
	"token.save": {Title: "Save a secret token", Build: func(c *checker) ([]string, []string) {
		return []string{"token", "save", c.id("id")}, []string{c.secret("token", true)}
	}},
	"cert.switch": {Title: "Switch certificate", Build: func(c *checker) ([]string, []string) {
		return append([]string{"cert", "switch", c.id("id"), "--tls", c.enum("tls", "letsencrypt", "selfsigned")}, when(c.flag("testMode"), "--test-mode")...), nil
	}},
	"cert.retry":      {Title: "Retry certificate", Build: func(c *checker) ([]string, []string) { return []string{"cert", "retry", c.id("id")}, nil }},
	"firewall.repair": {Title: "Repair firewall rules", Build: func(*checker) ([]string, []string) { return []string{"firewall", "repair"}, nil }},
	"firewall.upnp": {Title: "Router port mapping", Build: func(c *checker) ([]string, []string) {
		return []string{"firewall", "upnp", c.enum("mode", "on", "off", "status")}, nil
	}},
	"admins.add":    {Title: "Add an admin", Build: func(c *checker) ([]string, []string) { return []string{"admins", "add", c.username("user")}, nil }},
	"admins.remove": {Title: "Remove an admin", Build: func(c *checker) ([]string, []string) { return []string{"admins", "remove", c.username("user")}, nil }},
	"vault.unlock": {Title: "Unlock the vault", Build: func(c *checker) ([]string, []string) {
		return []string{"vault", "unlock"}, []string{c.secret("passphrase", true)}
	}},
	"vault.change": {Title: "Change vault passphrase", Reauth: true, Build: func(c *checker) ([]string, []string) {
		return []string{"vault", "change-passphrase"}, []string{c.secret("passphrase", true), c.secret("newPassphrase", true)}
	}},
	"vault.recover": {Title: "Recover the vault", Reauth: true, Build: func(c *checker) ([]string, []string) {
		return []string{"vault", "recover"}, []string{c.secret("recoveryKey", true), c.secret("newPassphrase", true)}
	}},
	"storage.add": {Title: "Add a location", Build: func(c *checker) ([]string, []string) { return []string{"storage", "add", c.absPath("path", true)}, nil }},
	"storage.mount": {Title: "Add a drive", Build: func(c *checker) ([]string, []string) {
		return []string{"storage", "mount", c.absPath("device", true)}, nil
	}},
	"storage.remove": {Title: "Remove a location", Build: func(c *checker) ([]string, []string) {
		return []string{"storage", "remove", c.absPath("path", true)}, nil
	}},
	"storage.default": {Title: "Default location", Build: func(c *checker) ([]string, []string) {
		return []string{"storage", "default", c.absPath("path", true)}, nil
	}},
	"storage.backups": {Title: "Backup location", Build: func(c *checker) ([]string, []string) {
		return []string{"storage", "backup-location", c.absPath("path", true)}, nil
	}},
	"system.health": {Title: "Health check", Build: func(*checker) ([]string, []string) { return []string{"health"}, nil }},
	"system.repair": {Title: "Repair", Build: func(*checker) ([]string, []string) { return []string{"repair"}, nil }},
	"system.setup":  {Title: "First-time setup", Build: func(*checker) ([]string, []string) { return []string{"setup"}, nil }},
	"system.removeAll": {Title: "Remove everything", Reauth: true, Build: func(c *checker) ([]string, []string) {
		if c.raw("confirm") != "remove everything" {
			c.fail("confirm", `type "remove everything" to confirm`)
		}
		return []string{"remove-everything"}, []string{"remove everything"}
	}},
	"alerts.set": {Title: "Health alerts", Build: func(c *checker) ([]string, []string) {
		if c.flag("off") {
			return []string{"health", "alerts", "--off"}, nil
		}
		args := []string{"health", "alerts"}
		if w := c.line("webhook", false); w != "" {
			if !strings.HasPrefix(w, "https://") && !strings.HasPrefix(w, "http://") {
				c.fail("webhook", "an http or https URL")
			}
			args = append(args, "--webhook", w)
		}
		var stdin []string
		if to := c.line("emailTo", false); to != "" {
			args = append(args, "--email-to", to, "--smtp", c.line("smtp", true))
			if u := c.line("smtpUser", false); u != "" {
				args = append(args, "--smtp-user", u)
			}
			if f := c.line("emailFrom", false); f != "" {
				args = append(args, "--email-from", f)
			}
			if pw := c.secret("smtpPassword", false); pw != "" {
				args = append(args, "--smtp-password-stdin")
				stdin = append(stdin, pw)
			}
		}
		return args, stdin
	}},
	"alerts.test": {Title: "Send a test alert", Build: func(*checker) ([]string, []string) { return []string{"health", "alerts", "--test"}, nil }},
}

// Build checks the inputs and returns the command line and stdin.
func Build(name string, p Params) (Action, []string, []string, error) {
	a, ok := Actions[name]
	if !ok {
		return a, nil, nil, errors.New("unknown action")
	}
	c := &checker{p: p}
	args, stdin := a.Build(c)
	if c.err != nil {
		return a, nil, nil, c.err
	}
	return a, args, stdin, nil
}
