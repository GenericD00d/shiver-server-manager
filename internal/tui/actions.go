package tui

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/GenericD00d/shiver-server-manager/internal/help"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// Action is one menu entry.
type Action struct {
	Title, Desc string
	// Form asks for the action's inputs and returns the command's arguments;
	// ok is false when the admin backed out.
	Form func(Snapshot) (args []string, ok bool, err error)
	// Run replaces Form for actions that aren't a single command.
	Run func(Host, Snapshot)
	// Confirm is shown before running (with the args for %s); Destructive
	// shows it in red.
	Confirm     string
	Destructive bool
	// PassYes adds --yes once the menu has confirmed.
	PassYes bool
	// Reauth asks for the password again first (tokens, deletes, restores).
	Reauth bool
}

type Section struct {
	Title, Summary string
	Actions        []Action
}

func theme() huh.Theme { return huh.ThemeFunc(huh.ThemeBase) }

func run(fields ...huh.Field) (bool, error) {
	err := huh.NewForm(huh.NewGroup(fields...)).WithTheme(theme()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	return err == nil, err
}

func confirm(text string, destructive bool) bool {
	if destructive {
		text = redSty.Render(text)
	}
	ok := false
	if _, err := run(huh.NewConfirm().Title(text).Affirmative("Yes").Negative("No").Value(&ok)); err != nil {
		return false
	}
	return ok
}

// pickServer asks which server; all adds an "every server" choice.
func pickServer(snap Snapshot, title string, all bool) (string, bool, error) {
	if len(snap.Servers) == 0 {
		return "", false, errors.New("there are no servers yet")
	}
	var opts []huh.Option[string]
	if all {
		opts = append(opts, huh.NewOption("Every server", "--all"))
	}
	for _, s := range snap.Servers {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%s  (%s, %s)", s.ID, s.Domain, s.Running), s.ID))
	}
	id := ""
	ok, err := run(huh.NewSelect[string]().Title(title).Options(opts...).Value(&id))
	return id, ok, err
}

func input(title, desc string, value *string, check func(string) error) huh.Field {
	in := huh.NewInput().Title(title).Value(value)
	if desc != "" {
		in = in.Description(desc)
	}
	if check != nil {
		in = in.Validate(check)
	}
	return in
}

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func domainCheck(s string) error { _, err := validate.Domain(s); return err }

var pluginRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func pluginID(s string) error {
	if !pluginRE.MatchString(s) || strings.Contains(s, "..") {
		return errors.New("lowercase letters, digits, dots, dashes and underscores")
	}
	return nil
}

func positive(s string) error {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err != nil || n < 0 {
		return errors.New("a whole number")
	}
	return nil
}

// pickVersion asks which Sharkord release to run, newest first and chosen by
// default, offering only those newer than newerThan when it is set. With
// nothing to choose it asks nothing, and the manager's default applies.
func pickVersion(snap Snapshot, newerThan string) ([]string, bool, error) {
	var opts []huh.Option[string]
	for _, v := range snap.Versions {
		if newerThan != "" && sharkord.CompareVersions(v, newerThan) <= 0 {
			continue
		}
		label := v
		if len(opts) == 0 {
			label += " (newest)"
		}
		opts = append(opts, huh.NewOption(label, v))
	}
	if len(opts) == 0 {
		return nil, true, nil
	}
	version := opts[0].Value
	ok, err := run(huh.NewSelect[string]().Title("Sharkord version").Options(opts...).Value(&version))
	if !ok || err != nil {
		return nil, ok, err
	}
	return []string{"--version", version}, true, nil
}

// serverCmd makes an action that only needs a server picked.
func serverCmd(title, desc string, all bool, args ...string) Action {
	return Action{Title: title, Desc: desc, Form: func(snap Snapshot) ([]string, bool, error) {
		id, ok, err := pickServer(snap, title, all)
		if !ok || err != nil {
			return nil, ok, err
		}
		return append(append([]string{}, args...), id), true, nil
	}}
}

// plain makes an action with fixed arguments.
func plain(title, desc string, args ...string) Action {
	return Action{Title: title, Desc: desc, Form: func(Snapshot) ([]string, bool, error) { return args, true, nil }}
}

// Sections are the menu, as laid out in the checklist.
var Sections = []Section{
	{"Servers", "create, import, export, start/stop, settings, logs, update, back up, restore, delete", []Action{
		{Title: "Create a server", Desc: "a name, a domain and a certificate type", Form: func(snap Snapshot) ([]string, bool, error) {
			var id, domain, tls = "", "", "letsencrypt"
			ok, err := run(
				input("Server ID", "lowercase letters, digits and hyphens, starting with a letter; up to 20", &id, validate.InstanceID),
				input("Domain", "the address members will use; its A record must point at this server (see Help: Point a domain at the server)", &domain, domainCheck),
				huh.NewSelect[string]().Title("Certificate").Options(
					huh.NewOption("Let's Encrypt (trusted everywhere; needs ports 80 and 443 open)", "letsencrypt"),
					huh.NewOption("Self-signed (members install a root certificate)", "selfsigned")).Value(&tls),
			)
			if !ok || err != nil {
				return nil, ok, err
			}
			args := []string{"instance", "create", "--id", id, "--domain", domain, "--tls", tls}
			version, ok, err := pickVersion(snap, "")
			if !ok || err != nil {
				return nil, ok, err
			}
			more, ok, err := placeAndProxy(snap, tls != "selfsigned")
			return append(append(args, version...), more...), ok, err
		}, Confirm: "Create the server (%[4]s at %[6]s)?"},
		{Title: "Import a server", Desc: "from an export, a backup, a Sharkord data folder, or docker:<container>", Form: func(snap Snapshot) ([]string, bool, error) {
			var src, id, domain, tls = "", "", "", "same"
			ok, err := run(
				input("Import from", "an export or backup file, a data folder such as /home/me/.config/sharkord, or docker:<container>", &src, nonEmpty),
				input("New server ID", "", &id, validate.InstanceID),
				input("Domain", "", &domain, domainCheck),
				huh.NewSelect[string]().Title("Certificate").Options(
					huh.NewOption("The same as the export (or Let's Encrypt)", "same"),
					huh.NewOption("Let's Encrypt", "letsencrypt"),
					huh.NewOption("Self-signed (members install a root certificate)", "selfsigned")).Value(&tls),
			)
			if !ok || err != nil {
				return nil, ok, err
			}
			args := []string{"import", src, "--id", id, "--domain", domain}
			if tls != "same" {
				args = append(args, "--tls", tls)
			}
			version, ok, err := pickVersion(snap, "")
			if !ok || err != nil {
				return nil, ok, err
			}
			more, ok, err := placeAndProxy(snap, tls != "selfsigned")
			return append(append(args, version...), more...), ok, err
		}},
		{Title: "Export a server", Desc: "to an encrypted file, to move it elsewhere", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Export which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			out := fmt.Sprintf("/var/backups/sharkord/%s-export-%s.tar.zst.age", id, time.Now().Format("20060102-1504"))
			withToken := false
			ok, err = run(input("Save to", "", &out, nonEmpty),
				huh.NewConfirm().Title("Include the secret token (inside the encrypted file)?").Value(&withToken))
			args := []string{"export", id, "--out", out}
			if withToken {
				args = append(args, "--include-token")
			}
			return args, ok, err
		}},
		serverCmd("Start", "start a server, or all", true, "instance", "start"),
		serverCmd("Stop", "stop a server, or all", true, "instance", "stop"),
		serverCmd("Restart", "restart a server, or all", true, "instance", "restart"),
		{Title: "Change settings", Desc: "Sharkord's settings (the server restarts)", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			var key, value string
			opts := []huh.Option[string]{}
			for _, s := range snap.Settings {
				opts = append(opts, huh.NewOption(s, s))
			}
			ok, err = run(huh.NewSelect[string]().Title("Setting").Options(opts...).Value(&key),
				input("Value", "empty goes back to the default; lists are comma-separated", &value, nil))
			return []string{"instance", "set", id, key + "=" + value}, ok, err
		}},
		serverCmd("Show logs", "the service's journal and Sharkord's error log", false, "instance", "logs"),
		{Title: "Update Sharkord", Desc: "back up, switch version, roll back if it doesn't start", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Update Sharkord", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			current := ""
			for _, s := range snap.Servers {
				if s.ID == id {
					current = s.Version
				}
			}
			version, ok, err := pickVersion(snap, current)
			return append([]string{"instance", "update", id}, version...), ok, err
		}},
		serverCmd("Back up now", "an encrypted backup of a server, or all", true, "backup"),
		{Title: "Restore a backup", Desc: "replace a server's data with a backup", Destructive: true, Reauth: true, PassYes: true,
			Confirm: "Replace %[2]s's data with this backup? Everything since it was made is lost.",
			Form: func(snap Snapshot) ([]string, bool, error) {
				id, ok, err := pickServer(snap, "Restore which server?", false)
				if !ok || err != nil {
					return nil, ok, err
				}
				files := snap.Backups[id]
				if len(files) == 0 {
					return nil, false, fmt.Errorf("%s has no backups yet", id)
				}
				file := files[len(files)-1]
				var opts []huh.Option[string]
				for i := len(files) - 1; i >= 0; i-- {
					opts = append(opts, huh.NewOption(files[i], files[i]))
				}
				ok, err = run(huh.NewSelect[string]().Title("Which backup?").Options(opts...).Value(&file))
				return []string{"restore", id, file}, ok, err
			}},
		{Title: "Change domain", Desc: "move a server to a new address", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			var d string
			ok, err = run(input("New domain", "point it at this server first; members will need the new address", &d, domainCheck))
			return []string{"instance", "domain", id, d}, ok, err
		}, Confirm: "Move %[3]s to %[4]s?"},
		{Title: "Limit memory and CPU", Desc: "so one busy community can't slow the others", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			mem, cpu := "0", "0"
			ok, err = run(input("Memory cap in MiB", "0 = none", &mem, positive), input("CPU cap, % of one core", "200 = two cores; 0 = none", &cpu, positive))
			return []string{"instance", "limits", id, "--memory", mem, "--cpu", cpu}, ok, err
		}},
		{Title: "Delete a server", Desc: "erase a server and all its data", Destructive: true, Reauth: true,
			Confirm: "Delete %[3]s and all its data, for good?",
			Form: func(snap Snapshot) ([]string, bool, error) {
				id, ok, err := pickServer(snap, "Delete which server?", false)
				return []string{"instance", "delete", id}, ok, err
			}},
	}},
	{"Owner tokens", "save, show, make a new one", []Action{
		{Title: "Show a secret token", Desc: "asks for your password again", Reauth: true, Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Whose secret token?", false)
			return []string{"token", "show", id}, ok, err
		}},
		serverCmd("Save a secret token", "for imported servers, or one you have", false, "token", "save"),
		{Title: "Make a new secret token", Desc: "the old one stops working; everyone logs in again", Destructive: true, Reauth: true, PassYes: true,
			Confirm: "Give %[3]s a new secret token? The old one stops working, everyone on the server has to log in again, and the server restarts.",
			Form: func(snap Snapshot) ([]string, bool, error) {
				id, ok, err := pickServer(snap, "Which server?", false)
				return []string{"token", "new", id}, ok, err
			}},
	}},
	{"Shiver plugins", "the Shiver plugins on offer: install, update", []Action{
		plain("Shiver plugins on offer", "with their latest versions and what each server has", "plugin", "catalog"),
		{Title: "Install or update a Shiver plugin", Desc: "from its latest release", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "On which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			plugin := ""
			ok, err = run(input("Plugin ID", "as 'Shiver plugins on offer' lists it, such as shiver or temp-share", &plugin, pluginID))
			return []string{"plugin", "add", id, plugin}, ok, err
		}},
		plain("Plugins on each server", "everything installed, Shiver's or not", "plugin", "list"),
	}},
	{"Certificates", "status and expiry, switch type, retry, test mode, root certificate", []Action{
		plain("Certificate status", "and expiry", "cert", "status"),
		{Title: "Switch certificate type", Desc: "Let's Encrypt or self-signed", PassYes: true, Confirm: "Switch %[3]s to %[5]s?", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			tls := "letsencrypt"
			ok, err = run(huh.NewSelect[string]().Title("Certificate").Options(huh.NewOption("Let's Encrypt", "letsencrypt"), huh.NewOption("Self-signed", "selfsigned")).Value(&tls))
			return []string{"cert", "switch", id, "--tls", tls}, ok, err
		}},
		serverCmd("Retry now", "recheck DNS and ask again", false, "cert", "retry"),
		{Title: "Test mode", Desc: "Let's Encrypt's staging server, with far higher limits", PassYes: true, Confirm: "Change %[3]s's test mode?", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			on := true
			ok, err = run(huh.NewConfirm().Title("Use the staging server (certificates aren't trusted)?").Affirmative("Test mode on").Negative("Off").Value(&on))
			args := []string{"cert", "switch", id, "--tls", "letsencrypt"}
			if on {
				args = append(args, "--test-mode")
			}
			return args, ok, err
		}},
		{Title: "Root certificate", Desc: "fingerprint, download link, save to a file", Form: func(Snapshot) ([]string, bool, error) {
			out := ""
			ok, err := run(input("Also save it to (optional)", "", &out, nil))
			args := []string{"cert", "root"}
			if out != "" {
				args = append(args, "--out", out)
			}
			return args, ok, err
		}},
	}},
	{"Firewall", "show rules, repair, router port mapping", []Action{
		plain("Show rules", "the manager's firewall rules", "firewall", "status"),
		plain("Repair rules", "add missing ones, offer to remove leftovers", "firewall", "repair"),
		{Title: "Router port mapping", Desc: "UPnP on a home router", Form: func(Snapshot) ([]string, bool, error) {
			mode := "status"
			ok, err := run(huh.NewSelect[string]().Title("Router port mapping").Options(
				huh.NewOption("Show status", "status"), huh.NewOption("Turn on", "on"), huh.NewOption("Turn off", "off")).Value(&mode))
			return []string{"firewall", "upnp", mode}, ok, err
		}},
		{Title: "Ports to open elsewhere", Desc: "cloud firewalls and home routers", Run: func(Host, Snapshot) {
			showText(help.Section("Open a cloud firewall") + "\n\n" + help.Section("Forward ports on a home router"))
		}},
	}},
	{"Security", "admins, unlock vault, change passphrase, grow storage", []Action{
		plain("Admins", "who can use the manager", "admins", "list"),
		{Title: "Add an admin", Desc: "an existing OS account", Form: func(Snapshot) ([]string, bool, error) {
			u := ""
			ok, err := run(input("OS username", "create it first with: sudo adduser <name>", &u, validate.Username))
			return []string{"admins", "add", u}, ok, err
		}},
		{Title: "Remove an admin", Desc: "", Confirm: "Remove %[3]s as an admin?", Form: func(Snapshot) ([]string, bool, error) {
			u := ""
			ok, err := run(input("OS username", "", &u, validate.Username))
			return []string{"admins", "remove", u}, ok, err
		}},
		plain("Unlock the vault", "and start encrypted servers waiting since a reboot", "vault", "unlock"),
		plain("Change vault passphrase", "the recovery key stays the same", "vault", "change-passphrase"),
		plain("Recover the vault", "with the recovery key, and set a new passphrase", "vault", "recover"),
		{Title: "Grow storage", Desc: "an encrypted server's container", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			size := ""
			ok, err = run(input("New size in GiB", "", &size, positive))
			return []string{"instance", "grow", id, "--size", size}, ok, err
		}},
		plain("Health alerts", "email or webhook (set with: health alerts --help)", "health", "alerts"),
	}},
	{"Storage", "locations, add a drive, move a server, backup location", []Action{
		plain("Storage locations", "drives, file systems and free space", "storage", "list"),
		{Title: "Add a location", Desc: "a folder on a mounted drive", Form: func(Snapshot) ([]string, bool, error) {
			p := ""
			ok, err := run(input("Folder", "for example /srv/disk2/sharkord", &p, nonEmpty))
			return []string{"storage", "add", p}, ok, err
		}},
		{Title: "Add a drive", Desc: "mount a formatted drive permanently", Run: func(h Host, _ Snapshot) {
			h.Run([]string{"storage", "mount"}, false)
			dev := ""
			if ok, _ := run(input("Device to mount (empty to cancel)", "from the list above; see Help: Add a new drive for Sharkord data", &dev, nil)); !ok || dev == "" {
				return
			}
			h.Run([]string{"storage", "mount", dev}, false)
			pause()
		}},
		{Title: "Move a server", Desc: "to another storage location", Form: func(snap Snapshot) ([]string, bool, error) {
			id, ok, err := pickServer(snap, "Move which server?", false)
			if !ok || err != nil {
				return nil, ok, err
			}
			to := ""
			var opts []huh.Option[string]
			for _, l := range snap.Locations {
				opts = append(opts, huh.NewOption(l, l))
			}
			ok, err = run(huh.NewSelect[string]().Title("Move to").Options(opts...).Value(&to))
			return []string{"instance", "move", id, "--to", to}, ok, err
		}},
		{Title: "Backup location", Desc: "where backups and exports go", Form: func(Snapshot) ([]string, bool, error) {
			p := ""
			ok, err := run(input("Folder", "", &p, nonEmpty))
			return []string{"storage", "backup-location", p}, ok, err
		}},
		{Title: "Default location", Desc: "for new servers", Form: func(snap Snapshot) ([]string, bool, error) {
			p := ""
			var opts []huh.Option[string]
			for _, l := range snap.Locations {
				opts = append(opts, huh.NewOption(l, l))
			}
			ok, err := run(huh.NewSelect[string]().Title("Default location").Options(opts...).Value(&p))
			return []string{"storage", "default", p}, ok, err
		}},
	}},
	{"System", "first-time setup, health check, repair, help, update manager", []Action{
		plain("First-time setup", "check the system; safe to run again", "setup"),
		plain("Health check", "everything the timer checks, now", "health"),
		plain("Web page", "is it on, and how to open it", "web", "status"),
		{Title: "Switch on the web page", Desc: "reached through an SSH tunnel, or a domain", Form: func(Snapshot) ([]string, bool, error) {
			domain := ""
			ok, err := run(input("Admin domain (optional)", "leave empty to reach it only through an SSH tunnel; with a domain, Caddy serves it over HTTPS", &domain, func(s string) error {
				if s == "" {
					return nil
				}
				return domainCheck(s)
			}))
			args := []string{"web", "enable"}
			if domain != "" {
				args = append(args, "--domain", domain)
			}
			return args, ok, err
		}},
		{Title: "Repair", Desc: "make the system match the records", Form: func(Snapshot) ([]string, bool, error) {
			return []string{"repair"}, true, nil
		}},
		{Title: "Help", Desc: "steps outside the manager", Run: func(Host, Snapshot) { showText(help.All()) }},
		{Title: "Update the manager", Desc: "how", Run: func(Host, Snapshot) { showText(help.Section("Update the manager")) }},
		{Title: "Remove everything", Desc: "delete every server before uninstalling", Destructive: true, Reauth: true,
			Confirm: "Delete every server and all their data?", Form: func(Snapshot) ([]string, bool, error) {
				return []string{"remove-everything"}, true, nil
			}},
	}},
}

func showHelp() error { showText(help.All()); return nil }

// showText pages long text inside the menu: never a pager or editor.
func showText(text string) {
	_, _ = tea.NewProgram(pager{lines: strings.Split(text, "\n"), height: 20}).Run()
}

type pager struct {
	lines  []string
	top    int
	height int
}

func (p pager) Init() tea.Cmd { return nil }

func (p pager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.height = max(msg.Height-2, 5)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c", "enter":
			return p, tea.Quit
		case "down", "j":
			p.top++
		case "up", "k":
			p.top--
		case "pgdown", "space", "f":
			p.top += p.height
		case "pgup", "b":
			p.top -= p.height
		case "home", "g":
			p.top = 0
		case "end", "G":
			p.top = len(p.lines)
		}
		p.top = max(0, min(p.top, len(p.lines)-p.height))
	}
	return p, nil
}

func (p pager) View() tea.View {
	end := min(p.top+p.height, len(p.lines))
	body := strings.Join(p.lines[p.top:end], "\n")
	v := tea.NewView(body + "\n" + grey.Render(fmt.Sprintf("lines %d-%d of %d · ↑/↓ pgup/pgdn scroll · q back", p.top+1, end, len(p.lines))))
	v.AltScreen = true
	return v
}

// placeAndProxy asks where a new server's data goes (when there's more than
// one storage location) and, for Let's Encrypt, whether its domain points at
// a proxy in front of this server.
func placeAndProxy(snap Snapshot, askProxy bool) ([]string, bool, error) {
	var fields []huh.Field
	loc := snap.DefaultLocation
	if len(snap.Locations) > 1 {
		var opts []huh.Option[string]
		for _, l := range snap.Locations {
			label := l
			if l == snap.DefaultLocation {
				label += " (default)"
			}
			opts = append(opts, huh.NewOption(label, l))
		}
		fields = append(fields, huh.NewSelect[string]().Title("Storage location").Options(opts...).Value(&loc))
	}
	proxy := false
	if askProxy {
		fields = append(fields, huh.NewConfirm().Title("Does the domain point at a proxy in front of this server?").
			Description("If so, the check that its A record holds this server's address is skipped.").Affirmative("Yes").Negative("No").Value(&proxy))
	}
	if len(fields) == 0 {
		return nil, true, nil
	}
	ok, err := run(fields...)
	var args []string
	if loc != "" && loc != snap.DefaultLocation {
		args = append(args, "--location", loc)
	}
	if proxy {
		args = append(args, "--skip-dns-check")
	}
	return args, ok, err
}
