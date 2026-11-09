// Package vault keeps secrets (owner tokens, data-folder keys) encrypted
// with age. The vault's own X25519 identity is stored encrypted with the
// vault passphrase; that identity, shown once at setup, is the recovery key.
// Anything can be encrypted to the vault's public key without unlocking it,
// so scheduled backups need no passphrase.
package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"filippo.io/age"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

const (
	identityFile  = "identity.age"
	recipientFile = "recipient.txt"
	canaryFile    = "canary.age"
	secretsDir    = "secrets"

	canaryText = "shiver-server-manager vault canary v1"

	// MinPassphrase is the shortest vault or export passphrase accepted.
	MinPassphrase = 12
)

// Secret names, one file each under secrets/<id>/.
const (
	OwnerToken = "owner-token"
	LUKSKey    = "luks-key"
	LUKSHeader = "luks-header"
)

var secretNames = map[string]bool{OwnerToken: true, LUKSKey: true, LUKSHeader: true}

var (
	ErrNoVault           = errors.New("the vault hasn't been set up yet; run first-time setup")
	ErrExists            = errors.New("the vault already exists")
	ErrWrongPassphrase   = errors.New("wrong vault passphrase")
	ErrWrongRecoveryKey  = errors.New("that recovery key doesn't open this vault")
	ErrBadRecoveryFormat = errors.New("that isn't a recovery key; it starts with AGE-SECRET-KEY-1")
	ErrNoSecret          = errors.New("no such secret in the vault")
)

type Vault struct {
	Dir string
	// WorkFactor is the scrypt work factor (log2 N) for the passphrase.
	// Zero means age's default (18, about a second). Tests lower it.
	WorkFactor int
}

// Key is an unlocked vault. Drop it with Forget when the session ends.
type Key struct{ id *age.X25519Identity }

// Identity returns the key for decrypting backups and secrets.
func (k *Key) Identity() age.Identity { return k.id }

// Forget drops the key. Go can't wipe the string age keeps inside the
// identity, so this only makes it unreachable.
func (k *Key) Forget() { k.id = nil }

func (v *Vault) path(name ...string) string {
	return filepath.Join(append([]string{v.Dir}, name...)...)
}

// Exists reports whether setup finished; identity.age is written last.
func (v *Vault) Exists() bool {
	for _, f := range []string{recipientFile, canaryFile, identityFile} {
		if _, err := os.Stat(v.path(f)); err != nil {
			return false
		}
	}
	return true
}

// CheckPassphrase enforces the minimum length.
func CheckPassphrase(p string) error {
	if utf8.RuneCountInString(p) < MinPassphrase {
		return fmt.Errorf("the passphrase must be at least %d characters", MinPassphrase)
	}
	return nil
}

// Create sets up a new vault and returns the recovery key, which the caller
// shows once.
func (v *Vault) Create(passphrase string) (recoveryKey string, err error) {
	if v.Exists() {
		return "", ErrExists
	}
	if err := CheckPassphrase(passphrase); err != nil {
		return "", err
	}
	if err := os.MkdirAll(v.Dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(v.Dir, 0o700); err != nil {
		return "", err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(v.path(recipientFile), []byte(id.Recipient().String()+"\n"), 0o600, nil); err != nil {
		return "", err
	}
	canary, err := encrypt([]byte(canaryText), id.Recipient())
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(v.path(canaryFile), canary, 0o600, nil); err != nil {
		return "", err
	}
	if err := v.writeIdentity(id, passphrase); err != nil {
		return "", err
	}
	return id.String(), nil
}

func (v *Vault) writeIdentity(id *age.X25519Identity, passphrase string) error {
	if err := CheckPassphrase(passphrase); err != nil {
		return err
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	if v.WorkFactor > 0 {
		r.SetWorkFactor(v.WorkFactor)
	}
	data, err := encrypt([]byte(id.String()+"\n"), r)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(v.path(identityFile), data, 0o600, nil)
}

// Unlock opens the vault with its passphrase.
func (v *Vault) Unlock(passphrase string) (*Key, error) {
	if !v.Exists() {
		return nil, ErrNoVault
	}
	sid, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, ErrWrongPassphrase // empty passphrase
	}
	enc, err := os.ReadFile(v.path(identityFile))
	if err != nil {
		return nil, err
	}
	plain, err := decrypt(enc, sid)
	var noMatch *age.NoIdentityMatchError
	if errors.As(err, &noMatch) {
		return nil, ErrWrongPassphrase
	}
	if err != nil {
		return nil, fmt.Errorf("read vault identity: %w", err)
	}
	id, err := age.ParseX25519Identity(strings.TrimSpace(string(plain)))
	clear(plain)
	if err != nil {
		return nil, fmt.Errorf("read vault identity: %w", err)
	}
	if err := v.checkCanary(id); err != nil {
		return nil, fmt.Errorf("the vault's files don't match each other: %w", err)
	}
	return &Key{id: id}, nil
}

// Recover opens the vault with the recovery key and sets a new passphrase.
func (v *Vault) Recover(recoveryKey, newPassphrase string) (*Key, error) {
	if !v.Exists() {
		return nil, ErrNoVault
	}
	id, err := ParseRecoveryKey(recoveryKey)
	if err != nil {
		return nil, err
	}
	if err := v.checkCanary(id); err != nil {
		return nil, ErrWrongRecoveryKey
	}
	if err := v.writeIdentity(id, newPassphrase); err != nil {
		return nil, err
	}
	return &Key{id: id}, nil
}

// ParseRecoveryKey reads a pasted recovery key, ignoring spaces and case.
func ParseRecoveryKey(s string) (*age.X25519Identity, error) {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	if !strings.HasPrefix(s, "AGE-SECRET-KEY-1") {
		return nil, ErrBadRecoveryFormat
	}
	id, err := age.ParseX25519Identity(s)
	if err != nil {
		return nil, ErrBadRecoveryFormat
	}
	return id, nil
}

// ConfirmRecoveryKey checks the admin typed back the key's last 6 characters.
func ConfirmRecoveryKey(key, typed string) bool {
	typed = strings.TrimSpace(typed)
	return len(key) >= 6 && len(typed) == 6 && strings.EqualFold(key[len(key)-6:], typed)
}

// ChangePassphrase rewrites identity.age under a new passphrase.
func (v *Vault) ChangePassphrase(k *Key, newPassphrase string) error {
	if k == nil || k.id == nil {
		return errors.New("the vault is locked")
	}
	return v.writeIdentity(k.id, newPassphrase)
}

func (v *Vault) checkCanary(id age.Identity) error {
	enc, err := os.ReadFile(v.path(canaryFile))
	if err != nil {
		return err
	}
	plain, err := decrypt(enc, id)
	if err != nil {
		return err
	}
	if string(plain) != canaryText {
		return errors.New("canary text differs")
	}
	return nil
}

// Recipient is the vault's public key; encrypting to it needs no unlock.
func (v *Vault) Recipient() (*age.X25519Recipient, error) {
	b, err := os.ReadFile(v.path(recipientFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoVault
	}
	if err != nil {
		return nil, err
	}
	return age.ParseX25519Recipient(strings.TrimSpace(string(b)))
}

func (v *Vault) secretPath(id, name string) (string, error) {
	if err := validate.InstanceID(id); err != nil {
		return "", err
	}
	if !secretNames[name] {
		return "", fmt.Errorf("unknown secret %q", name)
	}
	return v.path(secretsDir, id, name+".age"), nil
}

// PutSecret encrypts data to the vault's public key. No unlock needed.
func (v *Vault) PutSecret(id, name string, data []byte) error {
	p, err := v.secretPath(id, name)
	if err != nil {
		return err
	}
	r, err := v.Recipient()
	if err != nil {
		return err
	}
	enc, err := encrypt(data, r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(p, enc, 0o600, nil)
}

// GetSecret decrypts a secret. Clear the result when done with it.
func (v *Vault) GetSecret(k *Key, id, name string) ([]byte, error) {
	if k == nil || k.id == nil {
		return nil, errors.New("the vault is locked")
	}
	p, err := v.secretPath(id, name)
	if err != nil {
		return nil, err
	}
	enc, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSecret
	}
	if err != nil {
		return nil, err
	}
	return decrypt(enc, k.id)
}

func (v *Vault) HasSecret(id, name string) bool {
	p, err := v.secretPath(id, name)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// DeleteSecrets removes every secret of a server. Gone already is fine.
func (v *Vault) DeleteSecrets(id string) error {
	if err := validate.InstanceID(id); err != nil {
		return err
	}
	return os.RemoveAll(v.path(secretsDir, id))
}

func encrypt(plain []byte, r age.Recipient) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decrypt(enc []byte, id age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(enc), id)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// HasAnySecret reports whether a server has anything in the vault.
func (v *Vault) HasAnySecret(id string) bool {
	if validate.InstanceID(id) != nil {
		return false
	}
	entries, err := os.ReadDir(v.path(secretsDir, id))
	return err == nil && len(entries) > 0
}

// SecretIDs lists the servers that have secrets in the vault.
func (v *Vault) SecretIDs() []string {
	entries, _ := os.ReadDir(v.path(secretsDir))
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validate.InstanceID(e.Name()) == nil {
			ids = append(ids, e.Name())
		}
	}
	return ids
}
