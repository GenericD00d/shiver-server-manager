package cli

import (
	"github.com/spf13/cobra"
)

// stub makes a command whose behaviour comes in a later milestone.
func stub(use, short string, args cobra.PositionalArgs) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  args,
		RunE:  func(*cobra.Command, []string) error { return errNotBuilt },
	}
}

func group(use, short string, children ...*cobra.Command) *cobra.Command {
	c := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
	c.AddCommand(children...)
	return c
}

func addCommands(root *cobra.Command) {
	one, none := cobra.ExactArgs(1), cobra.NoArgs

	setup := stub("setup", "First-time setup: check the system and install what's needed", none)

	create := stub("create", "Create a server", none)
	create.Flags().String("id", "", "server ID: lowercase letters, digits and hyphens, starting with a letter, up to 20 characters")
	create.Flags().String("domain", "", "the domain members will use")
	create.Flags().String("tls", "letsencrypt", "certificate type: letsencrypt or selfsigned")
	create.Flags().Bool("test-mode", false, "use Let's Encrypt's staging server")
	create.Flags().String("location", "", "storage location (default: the default location)")
	create.Flags().Bool("no-encrypt", false, "don't put the data folder in an encrypted container")
	create.Flags().String("version", "", "Sharkord version (default: the newest tested one)")
	_ = create.MarkFlagRequired("id")
	_ = create.MarkFlagRequired("domain")

	logs := stub("logs <id>", "Show a server's logs", one)
	logs.Flags().BoolP("follow", "f", false, "keep showing new lines")

	update := stub("update <id>", "Update a server's Sharkord version", one)
	update.Flags().String("version", "", "Sharkord version (default: the newest tested one)")

	move := stub("move <id>", "Move a server's data to another storage location", one)
	move.Flags().String("to", "", "the storage location to move to")
	_ = move.MarkFlagRequired("to")

	instance := group("instance", "Servers: list, create, start, stop, settings, update, move, delete",
		stub("list", "List servers", none),
		stub("show <id>", "Show one server", one),
		create,
		stub("start <id>", "Start a server", one),
		stub("stop <id>", "Stop a server", one),
		stub("restart <id>", "Restart a server", one),
		logs,
		stub("set <id> KEY=VALUE...", "Change a server's settings, then restart it", cobra.MinimumNArgs(2)),
		update,
		move,
		stub("delete <id>", "Delete a server and all its data", one),
	)

	token := group("token", "Secret tokens (owner tokens)",
		stub("show <id>", "Show a server's secret token", one),
		stub("save <id>", "Check and save a server's secret token", one),
	)

	shiverUpdate := stub("update [<id>]", "Update the Shiver plugin", cobra.MaximumNArgs(1))
	shiverUpdate.Flags().Bool("all", false, "update every server, one at a time")
	shiver := group("shiver", "The Shiver plugin",
		stub("status", "Show installed and latest Shiver versions", none),
		stub("install <id>", "Install Shiver on a server", one),
		shiverUpdate,
	)

	certSwitch := stub("switch <id>", "Switch a server's certificate type", one)
	certSwitch.Flags().String("tls", "", "letsencrypt or selfsigned")
	_ = certSwitch.MarkFlagRequired("tls")
	certRoot := stub("root", "Show or save the self-signed root certificate", none)
	certRoot.Flags().String("out", "", "save the root certificate to this file")
	cert := group("cert", "Certificates",
		stub("status [<id>]", "Show certificate status and expiry", cobra.MaximumNArgs(1)),
		certSwitch,
		stub("retry <id>", "Recheck DNS and retry getting a certificate now", one),
		certRoot,
	)

	backup := stub("backup [<id>]", "Back up one server, or all of them", cobra.MaximumNArgs(1))
	backup.Flags().Bool("all", false, "back up every server")
	backup.Flags().Bool("unattended", false, "run from the timer: no login, no prompts")

	restore := stub("restore <id> <file>", "Restore a server from a backup", cobra.ExactArgs(2))

	export := stub("export <id>", "Export a server to an encrypted file", one)
	export.Flags().String("out", "", "the file to write")
	export.Flags().Bool("include-token", false, "include the secret token inside the encrypted file")
	_ = export.MarkFlagRequired("out")

	imp := stub("import <file|folder>", "Import a server from an export, a Sharkord data folder or a Docker volume", one)
	imp.Flags().String("id", "", "the new server's ID")
	imp.Flags().String("domain", "", "the new server's domain")
	imp.Flags().String("location", "", "storage location (default: the default location)")
	_ = imp.MarkFlagRequired("id")
	_ = imp.MarkFlagRequired("domain")

	storage := group("storage", "Storage locations",
		stub("list", "List storage locations", none),
		stub("add <path>", "Add a storage location", one),
		stub("mount <device>", "Mount a formatted drive permanently", one),
		stub("remove <path>", "Remove an unused storage location", one),
		stub("default <path>", "Make a location the default for new servers", one),
	)

	firewall := group("firewall", "Firewall rules",
		stub("status", "Show the manager's firewall rules", none),
		stub("repair", "Add missing rules and remove leftovers", none),
	)

	vault := group("vault", "The vault for secrets",
		stub("unlock", "Unlock the vault and any encrypted servers", none),
		stub("change-passphrase", "Change the vault passphrase", none),
		stub("recover", "Open the vault with the recovery key and set a new passphrase", none),
	)

	admins := group("admins", "Who can use the manager",
		stub("list", "List admins", none),
		stub("add <user>", "Add an existing OS user as an admin", one),
		stub("remove <user>", "Remove an admin", one),
	)

	health := stub("health", "Run the health check", none)
	health.Flags().Bool("unattended", false, "run from the timer: no login, no prompts")

	repair := stub("repair", "Make the system match the records", none)
	repair.Flags().Bool("dry-run", false, "only print what would change")

	root.AddCommand(setup, instance, token, shiver, cert, backup, restore, export, imp,
		storage, firewall, vault, admins, health, repair)
}
