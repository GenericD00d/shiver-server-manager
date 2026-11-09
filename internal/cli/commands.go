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
	instance := a.instanceCmd()
	instance.AddCommand(a.instanceUpdate(), a.instanceMove(), a.instanceGrow(), a.deleteCmd())

	root.AddCommand(a.setupCmd(), instance, a.tokenCmd(), a.shiverCmd(), a.certCmd(), a.backupCmd(), a.restoreCmd(),
		a.exportCmd(), a.importCmd(), a.storageCmd(), a.firewallCmd(), a.vaultCmd(), a.adminsCmd(), a.healthCmd(),
		a.repairCmd(), a.internalVacuumCmd(), a.internalEnablePluginsCmd())
}
