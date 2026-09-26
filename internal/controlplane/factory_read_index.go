package controlplane

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// The index is a disposable cache alongside the Machinist database. JSON
// manifests remain authoritative, and startup rebuilds every entry.
func openFactoryReadIndex(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("index path must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS factory_entries (kind TEXT NOT NULL, id TEXT NOT NULL, modified_ns INTEGER NOT NULL, PRIMARY KEY(kind,id))`,
		`CREATE INDEX IF NOT EXISTS factory_entries_order ON factory_entries(kind,modified_ns DESC,id DESC)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func (r *factoryRead) refreshIndex(kind string) error {
	r.indexMu.Lock()
	defer r.indexMu.Unlock()
	if kind != "runs" && kind != factoryChangeDir {
		return fmt.Errorf("unknown factory index kind %q", kind)
	}
	path := filepath.Join(r.root, kind)
	info, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && !info.IsDir() {
		return fmt.Errorf("factory %s path is not a directory", kind)
	}
	modified := time.Time{}
	if err == nil {
		modified = info.ModTime()
	}
	if previous, indexed := r.indexedAt[kind]; indexed && previous.Equal(modified) {
		return nil
	}

	tx, err := r.index.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM factory_entries WHERE kind=?`, kind); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO factory_entries(kind,id,modified_ns) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if !modified.IsZero() {
		dir, err := os.Open(path)
		if err != nil {
			return err
		}
		defer dir.Close()
		for {
			batch, readErr := dir.ReadDir(1024)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
			}
			for _, entry := range batch {
				name := entry.Name()
				id := name
				if kind == factoryChangeDir {
					id = strings.TrimSuffix(name, ".json")
				}
				if !validFactoryID(id) || (kind == "runs" && !entry.IsDir()) || (kind == factoryChangeDir && (entry.IsDir() || !strings.HasSuffix(name, ".json"))) {
					continue
				}
				item, err := entry.Info()
				if err != nil {
					return err
				}
				if kind == factoryChangeDir && !item.Mode().IsRegular() {
					continue
				}
				if _, err := stmt.Exec(kind, id, item.ModTime().UnixNano()); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	r.indexedAt[kind] = modified
	return nil
}

func (r *factoryRead) page(kind string, limit, offset int) ([]string, int, error) {
	if err := r.refreshIndex(kind); err != nil {
		return nil, 0, err
	}
	r.indexMu.Lock()
	defer r.indexMu.Unlock()
	var total int
	if err := r.index.QueryRow(`SELECT count(*) FROM factory_entries WHERE kind=?`, kind).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.index.Query(`SELECT id FROM factory_entries WHERE kind=? ORDER BY modified_ns DESC,id DESC LIMIT ? OFFSET ?`, kind, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return ids, total, nil
}
