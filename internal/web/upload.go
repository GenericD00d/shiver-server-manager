package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxUpload is the largest file the page can upload (a plugin archive).
const MaxUpload = 100 << 20

// uploadExts are the file types the page uploads, by name.
var uploadExts = []string{".zip", ".tar", ".tar.gz", ".tgz", ".tar.zst", ".tzst"}

// upload saves a file the page sends (the raw body) into UploadsDir under a
// random name, root-only, and returns its path for an action to use. The
// command that uses it deletes it; anything older than a day goes on the
// next upload.
func (s *Server) upload(w http.ResponseWriter, r *http.Request, sess *Session) {
	if s.UploadsDir == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "uploads aren't available"})
		return
	}
	name := strings.ToLower(r.URL.Query().Get("name"))
	ext := ""
	for _, e := range uploadExts {
		if strings.HasSuffix(name, e) && len(e) > len(ext) {
			ext = e
		}
	}
	if ext == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "choose a .zip, .tar, .tar.gz or .tar.zst file"})
		return
	}
	if err := os.MkdirAll(s.UploadsDir, 0o700); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.cleanUploads()
	var rnd [12]byte
	rand.Read(rnd[:])
	path := filepath.Join(s.UploadsDir, hex.EncodeToString(rnd[:])+ext)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, MaxUpload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the file is over 100 MB"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the upload didn't finish"})
		return
	}
	s.logAs(sess.User, "web upload", nil, "file", path)
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) cleanUploads() {
	entries, err := os.ReadDir(s.UploadsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && time.Since(fi.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(s.UploadsDir, e.Name()))
		}
	}
}
