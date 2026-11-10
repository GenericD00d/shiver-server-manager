package backup

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
)

// DefaultTimes are when backups run unless a server has its own times.
var DefaultTimes = []string{"00:00", "12:00"}

// Weekdays in the order the manager shows them.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Schedule is when a server is backed up: Times ("15:04", local time) on
// Days (empty means every day).
type Schedule struct {
	Times []string
	Days  []string
}

// ParseTimes reads "00:00,12:00" into sorted, unique HH:MM times.
func ParseTimes(s string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		t, err := time.Parse("15:04", f)
		if err != nil {
			return nil, fmt.Errorf("%q isn't a time like 03:30", f)
		}
		out = append(out, t.Format("15:04"))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("give at least one time, like 03:30")
	}
	if len(out) > 48 {
		return nil, fmt.Errorf("at most 48 times a day")
	}
	sort.Strings(out)
	return slices.Compact(out), nil
}

// ParseDays reads "mon,wed" (or "every"/"daily") into weekday names.
func ParseDays(s string) ([]string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "every" || s == "daily" || s == "all" {
		return nil, nil
	}
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if len(f) > 3 {
			f = f[:3]
		}
		if !slices.Contains(Weekdays, f) {
			return nil, fmt.Errorf("%q isn't a day (mon, tue, ... sun)", f)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return slices.Index(Weekdays, out[i]) < slices.Index(Weekdays, out[j]) })
	out = slices.Compact(out)
	if len(out) == 7 {
		return nil, nil
	}
	return out, nil
}

func weekday(t time.Time) string { return Weekdays[(int(t.Weekday())+6)%7] }

// LastSlot is the latest scheduled time at or before now.
func (s Schedule) LastSlot(now time.Time) (time.Time, bool) {
	times := s.Times
	if len(times) == 0 {
		times = DefaultTimes
	}
	for back := 0; back <= 7; back++ {
		day := now.AddDate(0, 0, -back)
		if len(s.Days) > 0 && !slices.Contains(s.Days, weekday(day)) {
			continue
		}
		for i := len(times) - 1; i >= 0; i-- {
			hm, err := time.Parse("15:04", times[i])
			if err != nil {
				continue
			}
			slot := time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
			if !slot.After(now) {
				return slot, true
			}
		}
	}
	return time.Time{}, false
}

// NextSlot is the first scheduled time after now.
func (s Schedule) NextSlot(now time.Time) (time.Time, bool) {
	times := s.Times
	if len(times) == 0 {
		times = DefaultTimes
	}
	for ahead := 0; ahead <= 7; ahead++ {
		day := now.AddDate(0, 0, ahead)
		if len(s.Days) > 0 && !slices.Contains(s.Days, weekday(day)) {
			continue
		}
		for _, t := range times {
			hm, err := time.Parse("15:04", t)
			if err != nil {
				continue
			}
			slot := time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, now.Location())
			if slot.After(now) {
				return slot, true
			}
		}
	}
	return time.Time{}, false
}

// Due reports whether a backup is owed: the last slot passed after both the
// newest backup and the server's creation.
func (s Schedule) Due(now, newest, created time.Time) bool {
	slot, ok := s.LastSlot(now)
	return ok && newest.Before(slot) && slot.After(created)
}

// Retention is how many backups to keep. Count 0 means no count limit;
// Days 0 means no age limit. The newest backup is never removed for its
// age, so a server whose backups are off keeps its last one.
type Retention struct {
	Count int
	Days  int
}

// toRemove lists the backups (oldest first) the retention drops.
func (r Retention) toRemove(list []Entry, now time.Time) []Entry {
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	var out []Entry
	for i, e := range list {
		left := len(list) - i
		tooMany := r.Count > 0 && left > r.Count
		tooOld := r.Days > 0 && left > 1 && now.Sub(e.CreatedAt) > time.Duration(r.Days)*24*time.Hour
		if !tooMany && !tooOld {
			break
		}
		out = append(out, e)
	}
	return out
}

// PlanPrune lists the files a retention would delete now.
func PlanPrune(dir string, r Retention, now time.Time) ([]string, error) {
	ix, err := LoadIndex(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range r.toRemove(ix.Backups, now) {
		files = append(files, e.File)
	}
	return files, nil
}

// Prune deletes the backups a retention drops and returns their files.
func Prune(dir string, r Retention, now time.Time) ([]string, error) {
	ix, err := LoadIndex(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range r.toRemove(ix.Backups, now) {
		if err := Delete(dir, e.File); err != nil {
			return removed, err
		}
		removed = append(removed, e.File)
	}
	return removed, nil
}

// Delete removes one backup and its index entry. Only files the index
// lists can be deleted.
func Delete(dir, file string) error {
	if file == "" || strings.ContainsAny(file, "/\\") || file == "." || file == ".." {
		return fmt.Errorf("bad backup name %q", file)
	}
	ix, err := LoadIndex(dir)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(ix.Backups, func(e Entry) bool { return e.File == file })
	if i < 0 {
		return fmt.Errorf("there's no backup %s", file)
	}
	if err := fsutil.RemoveIfExists(filepath.Join(dir, file)); err != nil {
		return err
	}
	ix.Backups = slices.Delete(ix.Backups, i, i+1)
	return saveIndex(dir, ix)
}
