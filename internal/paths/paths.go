// Package paths holds every name and fixed location the manager uses, so a
// rename touches only this file.
package paths

const (
	// AppName is the name shown to people.
	AppName = "Shiver Server Manager"
	// Cmd is the binary, package and log name.
	Cmd = "shiver-server-manager"
	// AdminGroup is the OS group allowed to use the manager.
	AdminGroup = "shiver-admins"
	// Marker begins every file the manager generates.
	Marker = "Managed by " + Cmd

	ConfigDir       = "/etc/" + Cmd
	InstanceEnvDir  = ConfigDir + "/instances"
	StateDir        = "/var/lib/" + Cmd
	StateFile       = StateDir + "/state.json"
	HealthFile      = StateDir + "/health.json"
	LoginsFile      = StateDir + "/logins.json"
	VaultDir        = StateDir + "/vault"
	VersionLinkDir  = StateDir + "/bin"
	UpdatesDir      = StateDir + "/updates"
	UploadsDir      = StateDir + "/uploads"
	UpdateTokenFile = ConfigDir + "/github-token"
	UpdateCacheFile = StateDir + "/cache/manager-release.json"
	LockFile        = "/run/" + Cmd + ".lock"
	LogFile         = "/var/log/" + Cmd + ".log"
	BinaryPath      = "/usr/bin/" + Cmd
	PAMService      = Cmd
	SudoersFile     = "/etc/sudoers.d/" + Cmd
	CaddyGlobalDir  = "/etc/caddy/" + Cmd
	CaddyGlobalFile = CaddyGlobalDir + "/global.caddy"
	CaddySitesDir   = "/etc/caddy/sites"

	// Sharkord's own things keep Sharkord's name.
	SharkordOptDir        = "/opt/sharkord"
	DefaultDataLocation   = "/var/lib/sharkord"
	DefaultBackupLocation = "/var/backups/sharkord"
	FirewallLabelPrefix   = "sharkord:"
	InstanceUserPrefix    = "sharkord-"
	InstanceUnit          = "sharkord@.service"
)
