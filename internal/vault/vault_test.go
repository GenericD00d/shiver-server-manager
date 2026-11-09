package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const pass = "correct horse battery"

func newVault(t *testing.T) (*Vault, string) {
	t.Helper()
	v := &Vault{Dir: filepath.Join(t.TempDir(), "vault"), WorkFactor: 10}
	rk, err := v.Create(pass)
	if err != nil {
		t.Fatal(err)
	}
	return v, rk
}

func TestCreateAndUnlock(t *testing.T) {
	v, rk := newVault(t)
	if !strings.HasPrefix(rk, "AGE-SECRET-KEY-1") {
		t.Fatalf("recovery key %q", rk)
	}
	if !v.Exists() {
		t.Fatal("vault doesn't exist after Create")
	}
	if _, err := v.Create(pass); !errors.Is(err, ErrExists) {
		t.Fatalf("second Create: %v", err)
	}
	k, err := v.Unlock(pass)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := v.Recipient()
	if k.id.Recipient().String() != r.String() {
		t.Fatal("unlocked identity doesn't match recipient.txt")
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{"", identityFile, recipientFile, canaryFile} {
			fi, _ := os.Stat(v.path(f))
			want := os.FileMode(0o600)
			if f == "" {
				want = 0o700
			}
			if fi.Mode().Perm() != want {
				t.Errorf("%s: mode %v, want %v", f, fi.Mode().Perm(), want)
			}
		}
	}
}

func TestWrongPassphraseRefused(t *testing.T) {
	v, _ := newVault(t)
	for _, p := range []string{"wrong passphrase!", "", pass + " "} {
		if _, err := v.Unlock(p); !errors.Is(err, ErrWrongPassphrase) {
			t.Errorf("%q: got %v, want ErrWrongPassphrase", p, err)
		}
	}
}

func TestPassphraseTooShort(t *testing.T) {
	v := &Vault{Dir: t.TempDir(), WorkFactor: 10}
	if _, err := v.Create("short"); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if v.Exists() {
		t.Fatal("vault created anyway")
	}
}

func TestUnlockWithoutVault(t *testing.T) {
	v := &Vault{Dir: t.TempDir()}
	if _, err := v.Unlock(pass); !errors.Is(err, ErrNoVault) {
		t.Fatalf("got %v", err)
	}
}

func TestRecoverSetsNewPassphrase(t *testing.T) {
	v, rk := newVault(t)
	v.PutSecret("chat-a", OwnerToken, []byte("tok"))

	// Pasted with spaces, lower case and a newline.
	pasted := strings.ToLower(rk[:20]) + " " + rk[20:] + "\n"
	k, err := v.Recover(pasted, "a brand new passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v.GetSecret(k, "chat-a", OwnerToken); string(got) != "tok" {
		t.Fatalf("secret after recover: %q", got)
	}
	if _, err := v.Unlock(pass); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatal("old passphrase still works")
	}
	if _, err := v.Unlock("a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverRefusesOtherKeys(t *testing.T) {
	v, _ := newVault(t)
	_, otherKey := newVault(t)
	if _, err := v.Recover(otherKey, "a brand new passphrase"); !errors.Is(err, ErrWrongRecoveryKey) {
		t.Fatalf("other vault's key: %v", err)
	}
	if _, err := v.Recover("not a key", "a brand new passphrase"); !errors.Is(err, ErrBadRecoveryFormat) {
		t.Fatalf("garbage key: %v", err)
	}
	if _, err := v.Unlock(pass); err != nil {
		t.Fatal("failed recover changed the passphrase")
	}
}

func TestChangePassphrase(t *testing.T) {
	v, _ := newVault(t)
	k, _ := v.Unlock(pass)
	if err := v.ChangePassphrase(k, "short"); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if err := v.ChangePassphrase(k, "another long passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Unlock(pass); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatal("old passphrase still works")
	}
	if _, err := v.Unlock("another long passphrase"); err != nil {
		t.Fatal(err)
	}
	k.Forget()
	if err := v.ChangePassphrase(k, "yet another passphrase"); err == nil {
		t.Fatal("forgotten key still works")
	}
}

func TestSecrets(t *testing.T) {
	v, _ := newVault(t)
	token := []byte("0192d7a0-5b3c-7def-8123-456789abcdef")
	if err := v.PutSecret("chat-a", OwnerToken, token); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(v.path(secretsDir, "chat-a", OwnerToken+".age"))
	if bytes.Contains(raw, token) {
		t.Fatal("secret stored in the clear")
	}
	if !v.HasSecret("chat-a", OwnerToken) || v.HasSecret("chat-a", LUKSKey) {
		t.Fatal("HasSecret wrong")
	}
	k, _ := v.Unlock(pass)
	got, err := v.GetSecret(k, "chat-a", OwnerToken)
	if err != nil || !bytes.Equal(got, token) {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := v.GetSecret(k, "chat-a", LUKSKey); !errors.Is(err, ErrNoSecret) {
		t.Fatalf("missing secret: %v", err)
	}
	if err := v.DeleteSecrets("chat-a"); err != nil {
		t.Fatal(err)
	}
	if err := v.DeleteSecrets("chat-a"); err != nil {
		t.Fatal("deleting twice failed")
	}
	if v.HasSecret("chat-a", OwnerToken) {
		t.Fatal("secret survived delete")
	}
}

func TestSecretPathsChecked(t *testing.T) {
	v, _ := newVault(t)
	for _, c := range [][2]string{{"../x", OwnerToken}, {"chat-a", "../../identity"}, {"Chat", OwnerToken}} {
		if err := v.PutSecret(c[0], c[1], []byte("x")); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if err := v.DeleteSecrets(".."); err == nil {
		t.Fatal("DeleteSecrets(..) accepted")
	}
}

func TestConfirmRecoveryKey(t *testing.T) {
	key := "AGE-SECRET-KEY-1ABCDEFXYZ123"
	if !ConfirmRecoveryKey(key, "xyz123\n") || ConfirmRecoveryKey(key, "YZ123") || ConfirmRecoveryKey(key, "") {
		t.Fatal("ConfirmRecoveryKey wrong")
	}
}

func TestPartialCreateCanBeRedone(t *testing.T) {
	v := &Vault{Dir: t.TempDir(), WorkFactor: 10}
	os.WriteFile(v.path(recipientFile), []byte("age1junk\n"), 0o600)
	if v.Exists() {
		t.Fatal("partial vault counted as existing")
	}
	if _, err := v.Create(pass); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Unlock(pass); err != nil {
		t.Fatal(err)
	}
}
