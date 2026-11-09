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
	none := cobra.NoArgs

	instance := a.instanceCmd()
	instance.AddCommand(a.instanceUpdate(), a.instanceMove(), a.instanceGrow(), a.deleteCmd())

	health := stub("health", "Run the health check", none)
	health.Flags().Bool("unattended", false, "run from the timer: no login, no prompts")

	root.AddCommand(a.setupCmd(), instance, a.tokenCmd(), a.shiverCmd(), a.certCmd(), a.backupCmd(), a.restoreCmd(),
		a.exportCmd(), a.importCmd(), a.storageCmd(), a.firewallCmd(), a.vaultCmd(), a.adminsCmd(), health,
		a.repairCmd(), a.internalVacuumCmd(), a.internalEnablePluginsCmd())
}
