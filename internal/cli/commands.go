package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) addCommands(root *cobra.Command) {
	instance := a.instanceCmd()
	instance.AddCommand(a.instanceUpdate(), a.instanceMove(), a.instanceGrow(), a.instanceDomain(), a.instanceLimits(), a.deleteCmd())

	root.AddCommand(a.setupCmd(), instance, a.tokenCmd(), a.shiverCmd(), a.certCmd(), a.backupCmd(), a.restoreCmd(),
		a.exportCmd(), a.importCmd(), a.storageCmd(), a.firewallCmd(), a.vaultCmd(), a.adminsCmd(), a.healthCmd(),
		a.repairCmd(), a.webCmd(), a.removeEverythingCmd(), a.internalVacuumCmd(), a.internalEnablePluginsCmd(), a.selfUpdateCmd())
}
