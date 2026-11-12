package ops

import (
	"context"
	"fmt"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// NewToken gives a server a new secret (owner) token and saves it in the
// vault; the old one stops working. Sharkord keeps only the token's hash
// and reads it at startup, also signing logins and file links with it, so
// the server is backed up, stopped, given the new hash and started again,
// and everyone on it has to log in again.
func (s *Sys) NewToken(ctx context.Context, id string) error {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return err
	}
	if _, ok := sharkord.FindTested(inst.SharkordVersion); !ok {
		return fmt.Errorf("Sharkord %s isn't on the tested list, so the manager won't change its database", inst.SharkordVersion)
	}
	oldHash, err := sharkord.StoredTokenHash(inst.DataPath)
	if err != nil {
		return err
	}
	tok, err := sharkord.NewToken()
	if err != nil {
		return err
	}
	uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
	if err != nil {
		return err
	}

	s.emit(Event{Instance: id, Step: "backup", Status: "run"})
	if _, err := s.Backup(ctx, id); err != nil {
		return err
	}
	unit := systemd.Unit(id)
	if err := s.Systemd.Stop(ctx, unit); err != nil {
		return err
	}
	db := sharkord.DBPath(inst.DataPath)
	err = s.AsInstance(ctx, uid, gid, sharkord.HashToken(tok), "internal-set-token", db)
	if err == nil {
		if err = s.Vault.PutSecret(id, vault.OwnerToken, []byte(tok)); err != nil {
			// The vault still has the old token: put its hash back.
			if back := s.AsInstance(ctx, uid, gid, oldHash, "internal-set-token", db); back != nil {
				err = fmt.Errorf("%w; and putting the old token back failed (%v): restore the backup just made", err, back)
			}
		}
	}
	if startErr := s.Systemd.Start(ctx, unit); startErr != nil && err == nil {
		err = startErr
	}
	if err != nil {
		return err
	}
	return s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second)
}
