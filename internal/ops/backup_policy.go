package ops

import (
	"fmt"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func schedule(p state.BackupPolicy) backup.Schedule {
	return backup.Schedule{Times: p.Times, Days: p.Days}
}

func (s *Sys) retention(p state.BackupPolicy) backup.Retention {
	r := backup.Retention{Count: p.KeepCount, Days: p.KeepDays}
	switch {
	case p.KeepCount == 0:
		r.Count = s.St.Settings.BackupKeep
	case p.KeepCount < 0:
		r.Count = 0
	}
	return r
}

// BackupPolicyView is a server's backup policy with the defaults filled in.
type BackupPolicyView struct {
	Instance  string     `json:"instance"`
	Off       bool       `json:"off"`
	Times     []string   `json:"times"`
	Days      []string   `json:"days"`      // empty: every day
	KeepCount int        `json:"keepCount"` // 0: no limit
	KeepDays  int        `json:"keepDays"`  // 0: no limit
	Default   bool       `json:"default"`   // nothing changed from the defaults
	Next      *time.Time `json:"next,omitempty"`
	Last      *time.Time `json:"last,omitempty"`
	Backups   int        `json:"backups"`
}

func (s *Sys) BackupPolicy(id string) (BackupPolicyView, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return BackupPolicyView{}, err
	}
	p := inst.Backups
	r := s.retention(p)
	v := BackupPolicyView{Instance: id, Off: p.Off, Times: p.Times, Days: p.Days, KeepCount: r.Count, KeepDays: r.Days,
		Default: !p.Off && len(p.Times) == 0 && len(p.Days) == 0 && p.KeepCount == 0 && p.KeepDays == 0}
	if len(v.Times) == 0 {
		v.Times = backup.DefaultTimes
	}
	if v.Days == nil {
		v.Days = []string{}
	}
	if !p.Off {
		if next, ok := schedule(p).NextSlot(s.Now()); ok {
			v.Next = &next
		}
	}
	if list, err := s.Backups(id); err == nil {
		v.Backups = len(list)
		if len(list) > 0 {
			last := list[len(list)-1].CreatedAt
			v.Last = &last
		}
	}
	return v, nil
}

// PlanBackupPolicy lists the backups a new policy would delete at once.
func (s *Sys) PlanBackupPolicy(id string, p state.BackupPolicy) ([]string, error) {
	if _, err := s.Instance(id); err != nil {
		return nil, err
	}
	return backup.PlanPrune(s.BackupDir(id), s.retention(p), s.Now())
}

// SetBackupPolicy saves a server's backup policy and applies its retention
// at once. It returns the backups it deleted.
func (s *Sys) SetBackupPolicy(id string, p state.BackupPolicy) ([]string, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return nil, err
	}
	if p.KeepCount < -1 || p.KeepDays < 0 {
		return nil, fmt.Errorf("keep counts can't be negative")
	}
	inst.Backups = p
	if err := s.Save(); err != nil {
		return nil, err
	}
	return backup.Prune(s.BackupDir(id), s.retention(p), s.Now())
}

// DeleteBackup deletes one of a server's backups.
func (s *Sys) DeleteBackup(id, file string) error {
	if _, err := s.Instance(id); err != nil {
		return err
	}
	return backup.Delete(s.BackupDir(id), file)
}

// DueBackups lists the servers whose schedule owes a backup.
func (s *Sys) DueBackups() []string {
	var due []string
	for _, id := range s.IDs() {
		inst := s.St.Instances[id]
		if inst.Backups.Off || inst.Status != state.StatusActive || inst.Operation != nil {
			continue
		}
		var newest time.Time
		if list, err := s.Backups(id); err == nil && len(list) > 0 {
			newest = list[len(list)-1].CreatedAt
		}
		if schedule(inst.Backups).Due(s.Now(), newest, inst.CreatedAt) {
			due = append(due, id)
		}
	}
	return due
}
