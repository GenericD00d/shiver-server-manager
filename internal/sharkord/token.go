package sharkord

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

var (
	ansi      = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	tokenUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

	ErrTokenMismatch = errors.New("that token doesn't match this server's database")
	ErrNoTokenInDB   = errors.New("this server's database has no secret token yet")
)

// CaptureToken finds the owner token in first-start output: colour codes
// stripped, then the first UUIDv7 after the words "access token".
func CaptureToken(output string) (string, bool) {
	clean := ansi.ReplaceAllString(output, "")
	i := strings.Index(clean, "access token")
	if i < 0 {
		return "", false
	}
	tok := tokenUUID.FindString(clean[i:])
	return tok, tok != ""
}

// HashToken is how Sharkord stores the token: lowercase hex SHA-256.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// DBPath is the instance's database.
func DBPath(dataPath string) string { return filepath.Join(dataPath, "db.sqlite") }

// StoredTokenHash reads settings.secret_token. The database (and its WAL)
// is copied to a private temp folder and read there, so the manager never
// creates root-owned -shm or -wal files beside the live database.
func StoredTokenHash(dataPath string) (string, error) {
	var hash sql.NullString
	err := withDBCopy(dataPath, func(db *sql.DB) error {
		return db.QueryRow(`SELECT secret_token FROM settings LIMIT 1`).Scan(&hash)
	})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !hash.Valid) || (err == nil && hash.String == "") {
		return "", ErrNoTokenInDB
	}
	return hash.String, err
}

// CheckToken compares token with the database's hash.
func CheckToken(dataPath, token string) error {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return ErrTokenMismatch
	}
	stored, err := StoredTokenHash(dataPath)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(strings.ToLower(stored))) != 1 {
		return ErrTokenMismatch
	}
	return nil
}

// HasSettings reports whether the database exists and has a settings row,
// meaning Sharkord has already started once.
func HasSettings(dataPath string) bool {
	if _, err := os.Stat(DBPath(dataPath)); err != nil {
		return false
	}
	_, err := StoredTokenHash(dataPath)
	return err == nil
}

func withDBCopy(dataPath string, fn func(*sql.DB) error) error {
	src := DBPath(dataPath)
	if _, err := os.Lstat(src); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "ssm-db-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, suffix := range []string{"", "-wal"} {
		if err := copyRegular(src+suffix, filepath.Join(tmp, "db.sqlite"+suffix)); err != nil && !(suffix != "" && errors.Is(err, os.ErrNotExist)) {
			return err
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(tmp, "db.sqlite"))
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}

// copyRegular copies a regular file without following links.
func copyRegular(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a regular file", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Healthz checks http://127.0.0.1:<port>/healthz answers {"status":"ok"}
// within 3 seconds.
func Healthz(ctx context.Context, port int) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || !strings.Contains(strings.ReplaceAll(string(b), " ", ""), `"status":"ok"`) {
		return fmt.Errorf("/healthz answered %s", resp.Status)
	}
	return nil
}

// WaitHealthy polls Healthz every 500 ms until it answers or timeout passes.
func WaitHealthy(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if last = Healthz(ctx, port); last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("the server didn't answer on port %d within %s: %v", port, timeout, last)
}

// IntegrityCheck runs PRAGMA integrity_check on a copy of the database.
func IntegrityCheck(dataPath string) error {
	var result string
	err := withDBCopy(dataPath, func(db *sql.DB) error {
		return db.QueryRow(`PRAGMA integrity_check`).Scan(&result)
	})
	if err != nil {
		return fmt.Errorf("check %s: %w", DBPath(dataPath), err)
	}
	if result != "ok" {
		return fmt.Errorf("the database at %s failed its integrity check: %s", DBPath(dataPath), result)
	}
	return nil
}

// NewToken makes an owner token the way Sharkord does: a UUIDv7.
func NewToken() (string, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

var hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SetTokenHash replaces settings.secret_token in a stopped server's
// database. Sharkord also signs logins and file links with it, so everyone
// has to log in again. The manager runs it as the instance's own user.
func SetTokenHash(dbPath, hash string) error {
	if !hashRE.MatchString(hash) {
		return errors.New("not a SHA-256 hash")
	}
	if _, err := os.Lstat(dbPath); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('settings') WHERE name = 'secret_token'`).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return errors.New("Sharkord's database has no settings.secret_token")
	}
	res, err := db.Exec(`UPDATE settings SET secret_token = ?`, hash)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows != 1 {
		return fmt.Errorf("expected one settings row, found %d", rows)
	}
	return nil
}
