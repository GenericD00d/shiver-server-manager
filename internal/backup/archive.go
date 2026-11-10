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
	if err := addFile(tw, o.DBCopy, "db.sqlite"); err != nil {
		return fmt.Errorf("add the database: %w", err)
	}
	err = filepath.WalkDir(o.DataPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(o.DataPath, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: "data/" + rel + "/", Mode: 0o700, ModTime: now, Typeflag: tar.TypeDir})
		case d.Type().IsRegular():
			return addFile(tw, p, "data/"+rel)
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

func addFile(tw *tar.Writer, src, name string) error {
	// O_NOFOLLOW-equivalent: Lstat first, refuse anything but a plain file.
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a plain file", src)
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	mode := int64(0o600)
	if fi.Mode()&0o111 != 0 { // a program, such as a plugin's yt-dlp
		mode = 0o700
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(tw, f)
	if err == nil && n != fi.Size() {
		err = fmt.Errorf("%s changed size while being archived", src)
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
		target, err := safeTarget(o.Dest, h)
		if err != nil {
			return m, err
		}
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
			if err := os.MkdirAll(target, 0o700); err != nil {
				return m, err
			}
			if o.Chown != nil {
				if err := o.Chown(target); err != nil {
					return m, err
				}
			}
		case h.Typeflag == tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return m, err
			}
			perm := os.FileMode(0o600)
			if h.Mode&0o111 != 0 {
				perm = 0o700
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
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
			if o.Chown != nil {
				if err := o.Chown(target); err != nil {
					return m, err
				}
			}
		}
	}
	if !sawManifest {
		return m, errors.New("the archive has no manifest")
	}
	return m, nil
}

// safeTarget maps an archive entry into dest, refusing anything unsafe.
func safeTarget(dest string, h *tar.Header) (string, error) {
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
		return filepath.Join(dest, "db.sqlite"), nil
	case strings.HasPrefix(clean, "data/") && clean != "data/":
		rel := strings.TrimPrefix(clean, "data/")
		if skip(rel) {
			return "", fmt.Errorf("unexpected %q in the archive", name)
		}
		return filepath.Join(dest, filepath.FromSlash(rel)), nil
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
