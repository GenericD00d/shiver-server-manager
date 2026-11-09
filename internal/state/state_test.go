package state

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingGivesDefault(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st, Default()) {
		t.Fatalf("got %+v, want default", st)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st := Default()
	st.Settings.PublicIPv4 = "203.0.113.10"
	st.Instances["chat-a"] = &Instance{
		Status:          StatusActive,
		Domain:          "chat-a.example.com",
		TLS:             TLSLetsEncrypt,
		SharkordVersion: "0.0.30",
		Location:        "/var/lib/sharkord",
		DataPath:        "/var/lib/sharkord/chat-a",
		HTTPPort:        5000,
		WebRTCPort:      40000,
		Encrypted:       true,
		Settings:        map[string]string{"SHARKORD_WEBRTC_MAX_BITRATE": "30000000"},
		CreatedAt:       time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC),
		Operation:       &Operation{Name: "create", LastStep: "user", StartedAt: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)},
	}
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("round trip differs:\n got %+v\nwant %+v", got, st)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestLoadRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, []byte(`{"schema": 2, "settings": {}, "instances": {}}`), 0o600)
	_, err := Load(path)
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("got %v, want ErrNewerSchema", err)
	}
}

func TestLoadRefusesBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `{`,
		"no schema":     `{"settings": {}}`,
		"unknown field": `{"schema": 1, "settings": {}, "instances": {}, "surprise": true}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			os.WriteFile(path, []byte(body), 0o600)
			if _, err := Load(path); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestExclusiveLockBlocksAndNamesHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	a, err := Acquire(path, Exclusive, Holder(123, "alice"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(path, Exclusive, Holder(456, "bob"), 300*time.Millisecond)
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("got %v, want HeldError", err)
	}
	if !strings.Contains(held.Error(), "pid 123, user alice") {
		t.Fatalf("holder not named: %q", held.Error())
	}
	_, err = Acquire(path, Shared, "", 300*time.Millisecond)
	if !errors.As(err, &held) {
		t.Fatalf("shared lock got %v while exclusive held", err)
	}
	if err := a.Release(); err != nil {
		t.Fatal(err)
	}
	b, err := Acquire(path, Exclusive, Holder(456, "bob"), time.Second)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	b.Release()
}

func TestSharedLocksCoexistAndBlockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	a, err := Acquire(path, Shared, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := Acquire(path, Shared, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Release()
	if _, err := Acquire(path, Exclusive, "x", 300*time.Millisecond); err == nil {
		t.Fatal("exclusive lock taken while shared locks held")
	}
}

func TestLockWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	a, err := Acquire(path, Exclusive, "a", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		a.Release()
	}()
	b, err := Acquire(path, Exclusive, "b", 3*time.Second)
	if err != nil {
		t.Fatalf("waiting acquire failed: %v", err)
	}
	b.Release()
	if err := b.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
}
