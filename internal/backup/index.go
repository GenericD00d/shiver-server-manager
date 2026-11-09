package backup

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"

	_ "modernc.org/sqlite"
)

// Entry is one backup in an instance's index.
type Entry struct {
	File      string    `json:"file"`
	CreatedAt time.Time `json:"createdAt"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
}

// Index is <backupLocation>/<id>/index.json.
type Index struct {
	Backups []Entry `json:"backups"`
}

func indexPath(dir string) string { return filepath.Join(dir, "index.json") }

func LoadIndex(dir string) (Index, error) {
	var ix Index
	b, err := os.ReadFile(indexPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return ix, nil
	}
	if err != nil {
		return ix, err
	}
	return ix, json.Unmarshal(b, &ix)
}

func saveIndex(dir string, ix Index) error {
	sort.Slice(ix.Backups, func(i, j int) bool { return ix.Backups[i].CreatedAt.Before(ix.Backups[j].CreatedAt) })
	b, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(indexPath(dir), b, 0o600, nil)
}

// FileName is a backup's name: its UTC time.
func FileName(t time.Time) string {
	return t.UTC().Format("2006-01-02T150405Z") + ".tar.zst.age"
}

// Record adds a finished backup to the index, then deletes the oldest ones
// beyond keep. It returns the files it deleted.
func Record(dir string, e Entry, keep int) ([]string, error) {
	ix, err := LoadIndex(dir)
	if err != nil {
		return nil, err
	}
	ix.Backups = append(ix.Backups, e)
	sort.Slice(ix.Backups, func(i, j int) bool { return ix.Backups[i].CreatedAt.Before(ix.Backups[j].CreatedAt) })
	var removed []string
	for keep > 0 && len(ix.Backups) > keep {
		old := ix.Backups[0]
		if strings.ContainsAny(old.File, "/\\") {
			return nil, fmt.Errorf("bad file name %q in the index", old.File)
		}
		if err := fsutil.RemoveIfExists(filepath.Join(dir, old.File)); err != nil {
			return nil, err
		}
		removed = append(removed, old.File)
		ix.Backups = ix.Backups[1:]
	}
	return removed, saveIndex(dir, ix)
}

// Vacuum makes a consistent copy of a live database with VACUUM INTO. On a
// WAL database it includes changes still in the WAL file. The manager runs
// this as the instance's own user, so any -shm file SQLite creates belongs
// to the instance, never to root.
func Vacuum(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	db, err := sql.Open("sqlite", "file:"+src)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	return nil
}
