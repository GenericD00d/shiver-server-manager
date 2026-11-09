// Package ops is everything the manager does to the system, as resumable
// steps. The commands and the menu both call it.
package ops

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/actionlog"
	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// Paths are the folders ops writes to. Tests point them at a temp folder.
type Paths struct {
	StateFile      string
	EnvDir         string
	VersionLinkDir string
	OptDir         string
	SystemdDir     string
	VaultDir       string
	CacheDir       string
}

func DefaultPaths() Paths {
	return Paths{
		StateFile:      paths.StateFile,
		EnvDir:         paths.InstanceEnvDir,
		VersionLinkDir: paths.VersionLinkDir,
		OptDir:         paths.SharkordOptDir,
		SystemdDir:     "/etc/systemd/system",
		VaultDir:       paths.VaultDir,
		CacheDir:       filepath.Join(paths.StateDir, "cache"),
	}
}

// Event reports a step as it runs.
type Event struct {
	Instance string
	Op       string
	Step     string
	Status   string // "run", "done", "skip", "fail", "note"
	Detail   string
}

// Sys holds what operations need. The function fields default to the real
// system and are replaced in tests.
type Sys struct {
	Paths    Paths
	R        run.Runner
	St       *state.State // loaded under the lock by the caller
	Vault    *vault.Vault
	Systemd  systemd.Systemd
	Sharkord *sharkord.Client
	Log      *actionlog.Logger
	Progress func(Event)
	Caddy    *caddy.Caddy
	// FirewallFor picks the firewall backend; nil means the real one.
	FirewallFor func(name string) firewall.Firewall
	DNSCheck    func(ctx context.Context, domain string) netinfo.DNSResult
	CertWait    time.Duration // how long create waits for a certificate
	// EraseContainer locks an encrypted instance and wipes its key slots
	// (set once encrypted data folders are built).
	EraseContainer func(ctx context.Context, id string) error

	FirstStart  func(context.Context, sharkord.FirstStartOpts) (string, error)
	WaitHealthy func(ctx context.Context, port int, timeout time.Duration) error
	PublicIPv4  func(context.Context) (string, error)
	PortFree    func(port int, udpToo bool) bool
	UserIDs     func(name string) (uid, gid int, err error)
	Chown       func(path string, uid, gid int) error
	OwnerOf     func(path string) (uid, gid int, err error)
	Now         func() time.Time
}

// New makes a Sys for the real system.
func New(r run.Runner, st *state.State, log *actionlog.Logger, userAgent string) *Sys {
	p := DefaultPaths()
	s := &Sys{
		Paths:       p,
		R:           r,
		St:          st,
		Vault:       &vault.Vault{Dir: p.VaultDir},
		Systemd:     systemd.Systemd{R: r},
		Sharkord:    sharkord.NewClient(userAgent, filepath.Join(p.CacheDir, "sharkord-releases.json")),
		Log:         log,
		Caddy:       caddy.New(r, paths.CaddyGlobalFile),
		CertWait:    2 * time.Minute,
		FirstStart:  sharkord.FirstStart,
		WaitHealthy: sharkord.WaitHealthy,
		PublicIPv4: func(ctx context.Context) (string, error) {
			return netinfo.PublicIPv4(ctx, nil, netinfo.IPv4Sources)
		},
		PortFree: portFree,
		UserIDs:  osuser.IDs,
		Chown:    os.Lchown,
		OwnerOf:  fsutil.OwnerOf,
		Now:      time.Now,
	}
	s.DNSCheck = func(ctx context.Context, domain string) netinfo.DNSResult {
		return netinfo.CheckDNS(ctx, domain, s.St.Settings.PublicIPv4, netinfo.LocalIPv6())
	}
	return s
}

func (s *Sys) emit(e Event) {
	if s.Progress != nil {
		s.Progress(e)
	}
}

// Save writes the records.
func (s *Sys) Save() error { return state.Save(s.Paths.StateFile, s.St) }

// Instance returns a record or a clear error.
func (s *Sys) Instance(id string) (*state.Instance, error) {
	inst, ok := s.St.Instances[id]
	if !ok {
		return nil, fmt.Errorf("there's no server called %q", id)
	}
	return inst, nil
}

func (s *Sys) envPath(id string) string { return filepath.Join(s.Paths.EnvDir, id+".env") }
func (s *Sys) linkPath(id string) string {
	return filepath.Join(s.Paths.VersionLinkDir, id)
}
func (s *Sys) dropinDir(id string) string { return systemd.DropinDir(s.Paths.SystemdDir, id) }

// portFree checks nothing is using a port: TCP on 127.0.0.1 for HTTP, or
// UDP and TCP on all addresses for WebRTC.
func portFree(port int, webrtc bool) bool {
	if !webrtc {
		l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		l.Close()
		return true
	}
	l, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	u, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return false
	}
	u.Close()
	return true
}

// pickPort takes the lowest port in r not in the records and free now.
func (s *Sys) pickPort(r state.PortRange, used map[int]bool, webrtc bool) (int, error) {
	for p := r[0]; p <= r[1]; p++ {
		if !used[p] && s.PortFree(p, webrtc) {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port left in %d-%d", r[0], r[1])
}

func (s *Sys) usedPorts() (http, webrtc map[int]bool) {
	http, webrtc = map[int]bool{}, map[int]bool{}
	for _, inst := range s.St.Instances {
		http[inst.HTTPPort] = true
		webrtc[inst.WebRTCPort] = true
	}
	return
}
