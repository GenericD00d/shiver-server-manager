package backup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseTimesAndDays(t *testing.T) {
	got, err := ParseTimes(" 12:00, 3:30 ,12:00")
	if err != nil || !reflect.DeepEqual(got, []string{"03:30", "12:00"}) {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"", "25:00", "noon", "12:00,x"} {
		if _, err := ParseTimes(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	days, err := ParseDays("Sunday,mon,wed,mon")
	if err != nil || !reflect.DeepEqual(days, []string{"mon", "wed", "sun"}) {
		t.Fatalf("%v %v", days, err)
	}
	if d, err := ParseDays("every"); err != nil || d != nil {
		t.Fatalf("%v %v", d, err)
	}
	if d, _ := ParseDays("mon,tue,wed,thu,fri,sat,sun"); d != nil {
		t.Fatalf("all seven should mean every day: %v", d)
	}
	if _, err := ParseDays("funday"); err == nil {
		t.Fatal("funday accepted")
	}
}

func TestSlots(t *testing.T) {
	loc := time.UTC
	wed := time.Date(2026, 11, 11, 13, 0, 0, 0, loc) // a Wednesday
	def := Schedule{}
	if slot, _ := def.LastSlot(wed); !slot.Equal(time.Date(2026, 11, 11, 12, 0, 0, 0, loc)) {
		t.Fatalf("default last slot %v", slot)
	}
	if next, _ := def.NextSlot(wed); !next.Equal(time.Date(2026, 11, 12, 0, 0, 0, 0, loc)) {
		t.Fatalf("default next slot %v", next)
	}
	weekly := Schedule{Times: []string{"03:30"}, Days: []string{"mon"}}
	if slot, _ := weekly.LastSlot(wed); !slot.Equal(time.Date(2026, 11, 9, 3, 30, 0, 0, loc)) {
		t.Fatalf("weekly last slot %v", slot)
	}
	if next, _ := weekly.NextSlot(wed); !next.Equal(time.Date(2026, 11, 16, 3, 30, 0, 0, loc)) {
		t.Fatalf("weekly next slot %v", next)
	}
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	if !weekly.Due(wed, time.Date(2026, 11, 8, 0, 0, 0, 0, loc), created) {
		t.Fatal("Sunday's backup should leave Monday's slot owed")
	}
	if weekly.Due(wed, time.Date(2026, 11, 9, 3, 31, 0, 0, loc), created) {
		t.Fatal("already taken")
	}
	if weekly.Due(wed, time.Time{}, time.Date(2026, 11, 10, 0, 0, 0, 0, loc)) {
		t.Fatal("a server created after the slot owes nothing")
	}
}

func writeBackups(t *testing.T, dir string, now time.Time, ages ...int) {
	t.Helper()
	for _, days := range ages {
		at := now.Add(-time.Duration(days) * 24 * time.Hour)
		name := FileName(at)
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
		if _, err := Record(dir, Entry{File: name, CreatedAt: at}, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetention(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	writeBackups(t, dir, now, 30, 20, 10, 5, 1)

	plan, _ := PlanPrune(dir, Retention{Days: 7}, now)
	if len(plan) != 3 {
		t.Fatalf("plan %v", plan)
	}
	removed, err := Prune(dir, Retention{Count: 3}, now)
	if err != nil || len(removed) != 2 {
		t.Fatalf("%v %v", removed, err)
	}
	if removed, _ := Prune(dir, Retention{Days: 7}, now); len(removed) != 1 {
		t.Fatalf("by age: %v", removed)
	}
	// Everything is old, but the newest stays.
	if removed, _ := Prune(dir, Retention{Days: 7}, now.AddDate(0, 1, 0)); len(removed) != 1 {
		t.Fatalf("old: %v", removed)
	}
	ix, _ := LoadIndex(dir)
	if len(ix.Backups) != 1 {
		t.Fatalf("left %v", ix.Backups)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 { // the backup and index.json
		t.Fatalf("files %v", entries)
	}
}

func TestDelete(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	writeBackups(t, dir, now, 2, 1)
	ix, _ := LoadIndex(dir)
	if err := Delete(dir, ix.Backups[0].File); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../state.json", "index.json", "", "nope.tar.zst.age"} {
		if err := Delete(dir, bad); err == nil {
			t.Errorf("%q deleted", bad)
		}
	}
	ix, _ = LoadIndex(dir)
	if len(ix.Backups) != 1 {
		t.Fatalf("%v", ix.Backups)
	}
}
