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

func (a *App) addCommands(root *cobra.Command) {
	one, none := cobra.ExactArgs(1), cobra.NoArgs

	setup := a.setupCmd()

	update := stub("update <id>", "Update a server's Sharkord version", one)
	update.Flags().String("version", "", "Sharkord version (default: the newest tested one)")

	move := stub("move <id>", "Move a server's data to another storage location", one)
	move.Flags().String("to", "", "the storage location to move to")
	_ = move.MarkFlagRequired("to")

	instance := a.instanceCmd()
	instance.AddCommand(update, move, a.deleteCmd())

	token := a.tokenCmd()

	shiverUpdate := stub("update [<id>]", "Update the Shiver plugin", cobra.MaximumNArgs(1))
	shiverUpdate.Flags().Bool("all", false, "update every server, one at a time")
	shiver := group("shiver", "The Shiver plugin",
		stub("status", "Show installed and latest Shiver versions", none),
		stub("install <id>", "Install Shiver on a server", one),
		shiverUpdate,
	)

	cert := a.certCmd()

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

	firewall := a.firewallCmd()

	vault := a.vaultCmd()

	admins := a.adminsCmd()

	health := stub("health", "Run the health check", none)
	health.Flags().Bool("unattended", false, "run from the timer: no login, no prompts")

	repair := a.repairCmd()

	root.AddCommand(setup, instance, token, shiver, cert, backup, restore, export, imp,
		storage, firewall, vault, admins, health, repair)
}
