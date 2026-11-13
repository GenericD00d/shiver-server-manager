package plugins

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// MaxArchive is the largest plugin archive accepted from a file, and
// MaxUnpacked the most it may unpack to.
const (
	MaxArchive  = 100 << 20
	MaxUnpacked = 200 << 20
)

// entry is one file or folder in an archive, whatever its format.
type entry struct {
	name    string
	dir     bool
	regular bool
	exec    bool
	size    int64
	open    func() (io.ReadCloser, error)
}

// extract writes entries into dest, accepting only plain files and folders
// with relative paths (no "..", no links). Files are owner-only, keeping
// whether they can run.
//
// Every write goes through an os.Root anchored at dest, which resolves each
// path beneath dest and refuses to follow a symlink out of it. dest sits in
// a folder the instance's own user owns, so without this a symlink that user
// planted there would make root, which does the unpacking, write outside the
// folder. The staging folder is also kept root-owned (ops.stagingDir), so
// nothing can be planted mid-unpack. TestUnpackDoesNotFollowSymlinksInDest.
func extract(next func() (entry, bool, error), dest string) error {
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	var total int64
	for {
		e, ok, err := next()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		clean := path.Clean(strings.TrimPrefix(e.name, "./"))
		if strings.Contains(e.name, "\\") || path.IsAbs(e.name) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe path %q in the plugin archive", e.name)
		}
		if clean == "." {
			continue
		}
		target := filepath.FromSlash(clean)
		switch {
		case e.dir:
			if err := root.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		case !e.regular:
			return fmt.Errorf("%q in the plugin archive isn't a plain file", e.name)
		}
		total += e.size
		if total > MaxUnpacked {
			return fmt.Errorf("the plugin unpacks to more than %d MB", MaxUnpacked>>20)
		}
		if dir := filepath.FromSlash(path.Dir(clean)); dir != "." {
			if err := root.MkdirAll(dir, 0o700); err != nil {
				return err
			}
		}
		mode := os.FileMode(0o600)
		if e.exec {
			mode = 0o700
		}
		rc, err := e.open()
		if err != nil {
			return err
		}
		out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			rc.Close()
			return err
		}
		n, err := io.Copy(out, io.LimitReader(rc, MaxUnpacked))
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		if total += n - e.size; total > MaxUnpacked {
			return fmt.Errorf("the plugin unpacks to more than %d MB", MaxUnpacked>>20)
		}
	}
}

func zipEntries(zr *zip.Reader) func() (entry, bool, error) {
	i := 0
	return func() (entry, bool, error) {
		if i >= len(zr.File) {
			return entry{}, false, nil
		}
		f := zr.File[i]
		i++
		m := f.Mode()
		return entry{name: f.Name, dir: m.IsDir(), regular: m.IsRegular(), exec: m&0o111 != 0,
			size: int64(f.UncompressedSize64), open: f.Open}, true, nil
	}
}

func tarEntries(tr *tar.Reader) func() (entry, bool, error) {
	return func() (entry, bool, error) {
		h, err := tr.Next()
		if err == io.EOF {
			return entry{}, false, nil
		}
		if err != nil {
			return entry{}, false, fmt.Errorf("the plugin isn't a valid tar: %w", err)
		}
		return entry{name: h.Name, dir: h.Typeflag == tar.TypeDir, regular: h.Typeflag == tar.TypeReg,
			exec: h.Mode&0o111 != 0, size: h.Size,
			open: func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }}, true, nil
	}
}

// Unpack extracts a plugin zip held in memory into dest. See UnpackFile.
func Unpack(zb []byte, dest string) (string, Manifest, error) {
	zr, err := zip.NewReader(bytes.NewReader(zb), int64(len(zb)))
	if err != nil {
		return "", Manifest{}, fmt.Errorf("the plugin isn't a valid zip: %w", err)
	}
	if err := extract(zipEntries(zr), dest); err != nil {
		return "", Manifest{}, err
	}
	return readUnpacked(dest)
}

// UnpackFile extracts a plugin archive into dest: a .zip, or a .tar, plain
// or compressed with gzip or zstd (told apart by their contents, not their
// names). It returns the folder holding the plugin: dest itself, or the one
// folder inside it with manifest.json, server/index.js and client/index.js,
// as Sharkord's own installer accepts.
func UnpackFile(file, dest string) (string, Manifest, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", Manifest{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", Manifest{}, err
	}
	if !fi.Mode().IsRegular() {
		return "", Manifest{}, fmt.Errorf("%s isn't a file", file)
	}
	if fi.Size() > MaxArchive {
		return "", Manifest{}, fmt.Errorf("%s is over %d MB", file, MaxArchive>>20)
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", Manifest{}, err
	}
	var next func() (entry, bool, error)
	switch {
	case bytes.HasPrefix(head, []byte("PK\x03\x04")), bytes.HasPrefix(head, []byte("PK\x05\x06")):
		zr, err := zip.NewReader(f, fi.Size())
		if err != nil {
			return "", Manifest{}, fmt.Errorf("the plugin isn't a valid zip: %w", err)
		}
		next = zipEntries(zr)
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return "", Manifest{}, fmt.Errorf("the plugin isn't valid gzip: %w", err)
		}
		defer gz.Close()
		next = tarEntries(tar.NewReader(gz))
	case bytes.HasPrefix(head, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(f)
		if err != nil {
			return "", Manifest{}, fmt.Errorf("the plugin isn't valid zstd: %w", err)
		}
		defer zr.Close()
		next = tarEntries(tar.NewReader(zr))
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		next = tarEntries(tar.NewReader(f))
	default:
		return "", Manifest{}, errors.New("the file isn't a zip or tar archive (.zip, .tar, .tar.gz, .tgz, .tar.zst)")
	}
	if err := extract(next, dest); err != nil {
		return "", Manifest{}, err
	}
	return readUnpacked(dest)
}

func readUnpacked(dest string) (string, Manifest, error) {
	var m Manifest
	root, err := pluginRoot(dest)
	if err != nil {
		return "", m, err
	}
	b, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return "", m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return "", m, fmt.Errorf("the plugin's manifest.json isn't valid: %w", err)
	}
	return root, m, nil
}

// List reads the manifests of every plugin installed in a data folder.
func List(dataPath string) []Manifest {
	entries, err := os.ReadDir(filepath.Join(dataPath, "plugins"))
	if err != nil {
		return nil
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, ok := InstalledPlugin(dataPath, e.Name()); ok {
			out = append(out, m)
		}
	}
	return out
}
