package plugins

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// EnableInDB switches plugins on for the server and Shiver itself, in
// Sharkord's database. Both are off by default. This is the one place the
// manager writes to Sharkord's database: only with the instance stopped,
// after a backup, on a tested Sharkord version, and after checking the
// columns exist. The manager runs it as the instance's own user.
func EnableInDB(dbPath string) error {
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := needColumns(db, "settings", "enable_plugins"); err != nil {
		return err
	}
	if err := needColumns(db, "plugin_data", "plugin_id", "enabled", "settings"); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE settings SET enable_plugins = 1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO plugin_data (plugin_id, enabled, settings) VALUES (?, 1, '{}')
		ON CONFLICT(plugin_id) DO UPDATE SET enabled = 1`, ID); err != nil {
		return err
	}
	return tx.Commit()
}

func needColumns(db *sql.DB, table string, cols ...string) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		have[n] = true
	}
	for _, c := range cols {
		if !have[c] {
			return fmt.Errorf("Sharkord's database has no %s.%s; switch plugins on in Sharkord's settings instead", table, c)
		}
	}
	return nil
}

// ErrNotTested is returned when the switches can't be set in the database.
var ErrNotTested = errors.New("not a tested Sharkord version")
