package db

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrationsFS embed.FS

type DB struct {
	sql *sql.DB
}

func Open(dsn string) (*DB, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open %q: %w", dsn, err)
	}
	conn.SetMaxOpenConns(1) // SQLite WAL supports one writer
	d := &DB{sql: conn}
	if err := d.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) SQL() *sql.DB {
	return d.sql
}

func (d *DB) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("db: read migrations: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("db: read %s: %w", e.Name(), err)
		}
		if _, err := d.sql.Exec(string(data)); err != nil {
			return fmt.Errorf("db: apply %s: %w", e.Name(), err)
		}
	}
	return nil
}
