// Package backup writes and reads the manager's one archive format, used for
// both backups and exports: tar, compressed with zstd, encrypted with age.
// Backups are encrypted to the vault's public key; exports to a passphrase
// chosen for that export.
package backup

import (
	"archive/tar"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

// Manifest describes an archive.
type Manifest struct {
	Format          int       `json:"format"`
	Kind            string    `json:"kind"` // "backup" or "export"
	CreatedAt       time.Time `json:"createdAt"`
	ManagerVersion  string    `json:"managerVersion"`
	SharkordVersion string    `json:"sharkordVersion"`
	Instance        struct {
		ID        string            `json:"id"`
		Domain    string            `json:"domain"`
		TLS       string            `json:"tls"`
		Encrypted bool              `json:"encrypted"`
		Settings  map[string]string `json:"settings,omitempty"`
	} `json:"instance"`
	OwnerToken *string `json:"ownerToken"`
}

// FormatVersion is the archive layout this build writes and reads.
const FormatVersion = 1

// Skipped are top-level folders Sharkord unpacks again at every start, or
// its own scratch, logs and pre-upgrade snapshots.
var Skipped = []string{"interface", "drizzle", "mediasoup", "tmp", "logs", "backups", "lost+found"}

// skip reports whether a data-relative path stays out of archives.
func skip(rel string) bool {
	top, _, _ := strings.Cut(rel, "/")
	switch {
	case slices.Contains(Skipped, top), strings.HasPrefix(top, ".ssm-"):
		return true
	case rel == "db.sqlite", rel == "db.sqlite-wal", rel == "db.sqlite-shm", rel == "db.sqlite-journal":
		return true
	}
	return false
}

// WriteOpts are an archive's contents and destination.
type WriteOpts struct {
	Manifest  Manifest
	DataPath  string // the instance's data folder
	DBCopy    string // a consistent copy of db.sqlite (from VACUUM INTO)
	Recipient age.Recipient
	// Note receives files that were left out (links, devices).
	Note func(string)
}

// Write streams tar into zstd into age, to w. The database copy goes first,
// so every upload it refers to is already on disk.
func Write(w io.Writer, o WriteOpts) error {
	enc, err := age.Encrypt(w, o.Recipient)
	if err != nil {
		return err
	}
	zw, err := zstd.NewWriter(enc, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)

	mb, err := json.MarshalIndent(o.Manifest, "", "  ")
	if err != nil {
		return err
	}
	now := time.Now()
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(mb)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(mb); err != nil {
		return err
	}
	// The database copy is written by the instance's own user (internal-vacuum)
	// into a folder that user owns, so it's opened through a root on that folder:
	// swapped for a link to a root-only file, it is refused rather than read.
	dbRoot, err := os.OpenRoot(filepath.Dir(o.DBCopy))
	if err != nil {
		return fmt.Errorf("add the database: %w", err)
	}
	defer dbRoot.Close()
	if err := addFile(tw, dbRoot, filepath.Base(o.DBCopy), "db.sqlite"); err != nil {
		return fmt.Errorf("add the database: %w", err)
	}
	// Root walks a folder the instance's own user owns, so the walk and every
	// open go through a root on it, which never follows a link out of it.
	data, err := os.OpenRoot(o.DataPath)
	if err != nil {
		return err
	}
	defer data.Close()
	err = fs.WalkDir(data.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: "data/" + rel + "/", Mode: 0o700, ModTime: now, Typeflag: tar.TypeDir})
		case d.Type().IsRegular():
			return addFile(tw, data, filepath.FromSlash(rel), "data/"+rel)
		default:
			if o.Note != nil {
				o.Note("left out " + rel + " (not a plain file)")
			}
			return nil
		}
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return enc.Close()
}

// addFile archives the plain file name, opened through root so that a link
// cannot lead outside it. The type is checked on the opened file, not before:
// between the walk and the open, the instance's user could swap the file for
// a link or a pipe. readFlags opens without blocking, so a pipe swapped in
// can't hang the backup while it holds the manager's lock.
func addFile(tw *tar.Writer, root *os.Root, name, archiveName string) error {
	f, err := root.OpenFile(name, readFlags, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a plain file", name)
	}
	mode := int64(0o600)
	if fi.Mode()&0o111 != 0 { // a program, such as a plugin's yt-dlp
		mode = 0o700
	}
	if err := tw.WriteHeader(&tar.Header{Name: archiveName, Mode: mode, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(tw, f)
	if err == nil && n != fi.Size() {
		err = fmt.Errorf("%s changed size while being archived", name)
	}
	return err
}

// Kind of encryption an archive uses, read from its age header.
type Kind int

const (
	Unknown    Kind = iota
	Passphrase      // an export: ask for its passphrase
	VaultKey        // a backup: unlock the vault
)

// Detect reads the start of an age file to tell an export from a backup.
func Detect(r io.Reader) (Kind, error) {
	br := bufio.NewReader(io.LimitReader(r, 4096))
	first, err := br.ReadString('\n')
	if err != nil || strings.TrimSpace(first) != "age-encryption.org/v1" {
		return Unknown, errors.New("not an archive made by the manager (no age header)")
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return Unknown, errors.New("damaged archive header")
		}
		switch {
		case strings.HasPrefix(line, "-> scrypt "):
			return Passphrase, nil
		case strings.HasPrefix(line, "-> X25519 "):
			return VaultKey, nil
		case strings.HasPrefix(line, "---"):
			return Unknown, errors.New("the archive is encrypted in a way the manager doesn't use")
		}
	}
}

// ReadOpts unpack an archive.
type ReadOpts struct {
	Identity age.Identity
	// Dest receives db.sqlite and the data folder's contents.
	Dest string
	// Owner sets the unpacked files' owner (nil leaves root).
	Chown func(path string) error
}

// Read decrypts and unpacks an archive into o.Dest, returning its manifest.
// Only plain files and folders with relative paths are accepted: no "..",
// no links, no devices.
//
// Every write goes through an os.Root anchored at o.Dest, which resolves each
// path beneath it and refuses to follow a symlink out of it. o.Dest sits in
// a folder the instance's own user owns, so without this a symlink that user
// planted would make root, which does the unpacking, write outside the
// folder. o.Dest is also kept root-owned during the unpack (ops.stagingDir),
// so nothing can be planted mid-unpack, and o.Chown can't be aimed at a file
// that has since been swapped for a link. TestReadDoesNotFollowSymlinksInDest.
func Read(r io.Reader, o ReadOpts) (Manifest, error) {
	var m Manifest
	dec, err := age.Decrypt(r, o.Identity)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return m, errors.New("wrong passphrase or key for this archive")
		}
		return m, err
	}
	zr, err := zstd.NewReader(dec)
	if err != nil {
		return m, err
	}
	defer zr.Close()
	root, err := os.OpenRoot(o.Dest)
	if err != nil {
		return m, err
	}
	defer root.Close()
	chown := func(rel string) error {
		if o.Chown == nil {
			return nil
		}
		return o.Chown(filepath.Join(o.Dest, filepath.FromSlash(rel)))
	}
	tr := tar.NewReader(zr)
	sawManifest := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, fmt.Errorf("damaged archive: %w", err)
		}
		rel, err := safeTarget(h)
		if err != nil {
			return m, err
		}
		target := filepath.FromSlash(rel)
		switch {
		case h.Name == "manifest.json":
			b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return m, err
			}
			if err := json.Unmarshal(b, &m); err != nil {
				return m, fmt.Errorf("damaged manifest: %w", err)
			}
			if m.Format > FormatVersion {
				return m, fmt.Errorf("the archive was made by a newer version of the manager (format %d)", m.Format)
			}
			sawManifest = true
		case h.Typeflag == tar.TypeDir:
			if err := root.MkdirAll(target, 0o700); err != nil {
				return m, err
			}
			if err := chown(rel); err != nil {
				return m, err
			}
		case h.Typeflag == tar.TypeReg:
			if dir := filepath.FromSlash(path.Dir(rel)); dir != "." {
				if err := root.MkdirAll(dir, 0o700); err != nil {
					return m, err
				}
			}
			perm := os.FileMode(0o600)
			if h.Mode&0o111 != 0 {
				perm = 0o700
			}
			f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
			if err != nil {
				return m, err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return m, err
			}
			if err := f.Close(); err != nil {
				return m, err
			}
			if err := chown(rel); err != nil {
				return m, err
			}
		}
	}
	if !sawManifest {
		return m, errors.New("the archive has no manifest")
	}
	return m, nil
}

// safeTarget maps an archive entry to a path within the destination root,
// refusing anything unsafe. The returned path is slash-separated and
// relative; "" means the entry (the manifest) is read rather than written.
func safeTarget(h *tar.Header) (string, error) {
	switch h.Typeflag {
	case tar.TypeReg, tar.TypeDir:
	default:
		return "", fmt.Errorf("the archive holds %q, which isn't a plain file or folder", h.Name)
	}
	name := h.Name
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe path %q in the archive", name)
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", fmt.Errorf("unsafe path %q in the archive", name)
	}
	switch {
	case clean == "manifest.json":
		return "", nil
	case clean == "db.sqlite":
		return "db.sqlite", nil
	case strings.HasPrefix(clean, "data/") && clean != "data/":
		rel := strings.TrimPrefix(clean, "data/")
		if skip(rel) {
			return "", fmt.Errorf("unexpected %q in the archive", name)
		}
		return rel, nil
	}
	return "", fmt.Errorf("unexpected %q in the archive", name)
}

// Sum is a file's SHA-256 in hex.
func Sum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadManifest decrypts just enough of an archive to read its manifest.
func ReadManifest(file string, identity age.Identity) (Manifest, error) {
	var m Manifest
	f, err := os.Open(file)
	if err != nil {
		return m, err
	}
	defer f.Close()
	dec, err := age.Decrypt(f, identity)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return m, errors.New("wrong passphrase or key for this archive")
		}
		return m, err
	}
	zr, err := zstd.NewReader(dec)
	if err != nil {
		return m, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	h, err := tr.Next()
	if err != nil || h.Name != "manifest.json" {
		return m, errors.New("the archive doesn't start with a manifest")
	}
	b, err := io.ReadAll(io.LimitReader(tr, 1<<20))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("damaged manifest: %w", err)
	}
	if m.Format > FormatVersion {
		return m, fmt.Errorf("the archive was made by a newer version of the manager (format %d)", m.Format)
	}
	return m, nil
}
