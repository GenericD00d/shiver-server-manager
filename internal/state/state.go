// Package state holds the manager's records: what should exist on the system.
// Repair changes the system to match these records, never the reverse.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
)

// Schema is the record format this build reads and writes.
const Schema = 1

type Status string

const (
	StatusCreating Status = "creating"
	StatusActive   Status = "active"
	StatusLocked   Status = "locked"
	StatusDeleting Status = "deleting"
	StatusFailed   Status = "failed"
)

type TLS string

const (
	TLSLetsEncrypt TLS = "letsencrypt"
	TLSSelfSigned  TLS = "selfsigned"
)

// PortRange is an inclusive [low, high] range.
type PortRange [2]int

func (r PortRange) Contains(p int) bool { return p >= r[0] && p <= r[1] }

type Location struct {
	Path    string `json:"path"`
	Default bool   `json:"default,omitempty"`
	FSUUID  string `json:"fsUUID,omitempty"`
}

type Settings struct {
	HTTPPorts        PortRange  `json:"httpPorts"`
	WebRTCPorts      PortRange  `json:"webrtcPorts"`
	PublicIPv4       string     `json:"publicIPv4,omitempty"`
	FixedPublicIPv4  bool       `json:"fixedPublicIPv4,omitempty"`
	Firewall         string     `json:"firewall,omitempty"`
	Locations        []Location `json:"locations"`
	BackupLocation   string     `json:"backupLocation"`
	BackupKeep       int        `json:"backupKeep"`
	ContainerSizeGiB int        `json:"containerSizeGiB"`
	// Email is given to Let's Encrypt (and lets Caddy fall back to ZeroSSL).
	Email string `json:"email,omitempty"`
	// ACMECA and ACMECARoot point Caddy at a test CA (Pebble); hidden setting.
	ACMECA     string `json:"acmeCA,omitempty"`
	ACMECARoot string `json:"acmeCARoot,omitempty"`
}

// Operation is a create, delete, update, switch, move, unlock, restore or
// import that is running or stopped partway, so repair can resume it.
type Operation struct {
	Name       string    `json:"name"`
	LastStep   string    `json:"lastStep,omitempty"`   // last step that finished
	FailedStep string    `json:"failedStep,omitempty"` // set when a step failed
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
}

type Instance struct {
	Status          Status            `json:"status"`
	Domain          string            `json:"domain"`
	TLS             TLS               `json:"tls"`
	TestMode        bool              `json:"testMode"`
	SharkordVersion string            `json:"sharkordVersion"`
	Location        string            `json:"location"`
	DataPath        string            `json:"dataPath"`
	HTTPPort        int               `json:"httpPort"`
	WebRTCPort      int               `json:"webrtcPort"`
	Encrypted       bool              `json:"encrypted"`
	Settings        map[string]string `json:"settings,omitempty"`
	// Stopped is set when an admin stops the instance, so repair and the
	// health check don't start it again.
	Stopped   bool       `json:"stopped,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	Operation *Operation `json:"operation"`
}

type State struct {
	Schema    int                  `json:"schema"`
	Settings  Settings             `json:"settings"`
	Instances map[string]*Instance `json:"instances"`
}

// Default is the state of a machine the manager hasn't set up yet.
func Default() *State {
	return &State{
		Schema: Schema,
		Settings: Settings{
			HTTPPorts:        PortRange{5000, 5999},
			WebRTCPorts:      PortRange{40000, 40999},
			Locations:        []Location{{Path: paths.DefaultDataLocation, Default: true}},
			BackupLocation:   paths.DefaultBackupLocation,
			BackupKeep:       14, // two a day (00:00 and 12:00), so one week
			ContainerSizeGiB: 10,
		},
		Instances: map[string]*Instance{},
	}
}

// ErrNewerSchema means the file was written by a newer manager.
var ErrNewerSchema = errors.New("the records were written by a newer version of " + paths.Cmd)

// Load reads the records at path. A missing file gives Default().
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read records: %w", err)
	}
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("read records %s: %w", path, err)
	}
	if head.Schema > Schema {
		return nil, fmt.Errorf("%w (schema %d, this build reads %d)", ErrNewerSchema, head.Schema, Schema)
	}
	if head.Schema < 1 {
		return nil, fmt.Errorf("read records %s: missing or invalid schema", path)
	}
	st := &State{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(st); err != nil {
		return nil, fmt.Errorf("read records %s: %w", path, err)
	}
	if st.Instances == nil {
		st.Instances = map[string]*Instance{}
	}
	return st, nil
}

// Save writes the records atomically, root-only. Call it under an exclusive lock.
func Save(path string, st *State) error {
	st.Schema = Schema
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, append(data, '\n'), 0o600, nil)
}

// DefaultLocation returns the location marked default, or the first one.
func (s *State) DefaultLocation() string {
	for _, l := range s.Settings.Locations {
		if l.Default {
			return l.Path
		}
	}
	if len(s.Settings.Locations) > 0 {
		return s.Settings.Locations[0].Path
	}
	return paths.DefaultDataLocation
}
