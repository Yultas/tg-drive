package vfs

import (
	"database/sql"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Node struct {
	ID              int64     `json:"id"`
	ParentID        int64     `json:"parent_id"`
	Name            string    `json:"name"`
	Path            string    `json:"path"`
	IsDir           bool      `json:"is_dir"`
	Size            int64     `json:"size"`
	ModTime         time.Time `json:"mod_time"`
	MimeType        string    `json:"mime_type"`
	TGMessageID     int       `json:"tg_message_id"`
	TGChannelID     int64     `json:"tg_channel_id"`
	TGFileID        int64     `json:"tg_file_id"`
	TGAccessHash    int64     `json:"tg_access_hash"`
	TGFileReference []byte    `json:"tg_file_reference"`
	Status          string    `json:"status"` // "synced", "uploading", "pending", "error"
}

type DB struct {
	db *sql.DB
	mu sync.RWMutex
}

func OpenDB(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// SQLite pragmas for high concurrent read/write performance
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA cache_size=-64000;",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return nil, fmt.Errorf("failed to exec pragma %s: %w", pragma, err)
		}
	}

	instance := &DB{db: db}
	if err := instance.initSchema(); err != nil {
		return nil, err
	}

	return instance, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS nodes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		parent_id INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		path TEXT UNIQUE NOT NULL,
		is_dir BOOLEAN NOT NULL DEFAULT 0,
		size INTEGER NOT NULL DEFAULT 0,
		mod_time INTEGER NOT NULL,
		mime_type TEXT DEFAULT '',
		tg_message_id INTEGER DEFAULT 0,
		tg_channel_id INTEGER DEFAULT 0,
		tg_file_id INTEGER DEFAULT 0,
		tg_access_hash INTEGER DEFAULT 0,
		tg_file_reference BLOB,
		status TEXT NOT NULL DEFAULT 'synced'
	);
	CREATE INDEX IF NOT EXISTS idx_nodes_parent ON nodes(parent_id);
	CREATE INDEX IF NOT EXISTS idx_nodes_path ON nodes(path);
	`
	_, err := d.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	// Ensure Root directory exists
	var count int
	_ = d.db.QueryRow("SELECT COUNT(*) FROM nodes WHERE path = '/'").Scan(&count)
	if count == 0 {
		now := time.Now().Unix()
		_, err = d.db.Exec(`INSERT INTO nodes (parent_id, name, path, is_dir, size, mod_time, status)
			VALUES (0, '', '/', 1, 0, ?, 'synced')`, now)
	}

	return err
}

func CleanPath(p string) string {
	p = path.Clean("/" + strings.TrimSpace(p))
	p = strings.ReplaceAll(p, "\\", "/")
	return p
}

func (d *DB) GetNodeByPath(p string) (*Node, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	p = CleanPath(p)
	row := d.db.QueryRow(`
		SELECT id, parent_id, name, path, is_dir, size, mod_time, mime_type,
		       tg_message_id, tg_channel_id, tg_file_id, tg_access_hash, tg_file_reference, status
		FROM nodes WHERE path = ?`, p)

	var n Node
	var modUnix int64
	err := row.Scan(&n.ID, &n.ParentID, &n.Name, &n.Path, &n.IsDir, &n.Size,
		&modUnix, &n.MimeType, &n.TGMessageID, &n.TGChannelID, &n.TGFileID,
		&n.TGAccessHash, &n.TGFileReference, &n.Status)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	n.ModTime = time.Unix(modUnix, 0)
	return &n, nil
}

func (d *DB) ListChildren(parentID int64) ([]*Node, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, parent_id, name, path, is_dir, size, mod_time, mime_type,
		       tg_message_id, tg_channel_id, tg_file_id, tg_access_hash, tg_file_reference, status
		FROM nodes WHERE parent_id = ? ORDER BY is_dir DESC, name ASC`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*Node
	for rows.Next() {
		var n Node
		var modUnix int64
		if err := rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.Path, &n.IsDir, &n.Size,
			&modUnix, &n.MimeType, &n.TGMessageID, &n.TGChannelID, &n.TGFileID,
			&n.TGAccessHash, &n.TGFileReference, &n.Status); err != nil {
			return nil, err
		}
		n.ModTime = time.Unix(modUnix, 0)
		nodes = append(nodes, &n)
	}
	return nodes, nil
}

func (d *DB) MkdirAll(dirPath string) (*Node, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mkdirAllLocked(dirPath)
}

func (d *DB) mkdirAllLocked(dirPath string) (*Node, error) {
	dirPath = CleanPath(dirPath)
	if dirPath == "/" {
		var n Node
		var modUnix int64
		_ = d.db.QueryRow("SELECT id, parent_id, name, path, is_dir, size, mod_time FROM nodes WHERE path = '/'").
			Scan(&n.ID, &n.ParentID, &n.Name, &n.Path, &n.IsDir, &n.Size, &modUnix)
		n.ModTime = time.Unix(modUnix, 0)
		return &n, nil
	}

	parts := strings.Split(strings.Trim(dirPath, "/"), "/")
	currentPath := ""
	var parentID int64 = 1 // root node ID is 1

	var lastNode *Node
	for _, part := range parts {
		currentPath += "/" + part
		var id int64
		var isDir bool
		err := d.db.QueryRow("SELECT id, is_dir FROM nodes WHERE path = ?", currentPath).Scan(&id, &isDir)
		if err == sql.ErrNoRows {
			now := time.Now().Unix()
			res, err := d.db.Exec(`
				INSERT INTO nodes (parent_id, name, path, is_dir, size, mod_time, status)
				VALUES (?, ?, ?, 1, 0, ?, 'synced')`, parentID, part, currentPath, now)
			if err != nil {
				return nil, err
			}
			id, _ = res.LastInsertId()
			parentID = id
			lastNode = &Node{
				ID:       id,
				ParentID: parentID,
				Name:     part,
				Path:     currentPath,
				IsDir:    true,
				ModTime:  time.Unix(now, 0),
				Status:   "synced",
			}
		} else if err != nil {
			return nil, err
		} else {
			if !isDir {
				return nil, fmt.Errorf("path component %s is a file, not a directory", currentPath)
			}
			parentID = id
		}
	}
	return lastNode, nil
}

func (d *DB) SaveFileNode(n *Node) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	n.Path = CleanPath(n.Path)
	parentDir := path.Dir(n.Path)
	
	// Auto-create parent directory chain if needed
	var parentID int64 = 1
	if parentDir != "/" {
		parentNode, err := d.mkdirAllLocked(parentDir)
		if err == nil && parentNode != nil {
			parentID = parentNode.ID
		} else {
			_ = d.db.QueryRow("SELECT id FROM nodes WHERE path = ?", parentDir).Scan(&parentID)
		}
	}
	n.ParentID = parentID
	n.Name = path.Base(n.Path)

	modUnix := n.ModTime.Unix()
	if modUnix <= 0 {
		modUnix = time.Now().Unix()
	}

	query := `
	INSERT INTO nodes (parent_id, name, path, is_dir, size, mod_time, mime_type,
	                   tg_message_id, tg_channel_id, tg_file_id, tg_access_hash, tg_file_reference, status)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		parent_id = excluded.parent_id,
		name = excluded.name,
		is_dir = excluded.is_dir,
		size = excluded.size,
		mod_time = excluded.mod_time,
		mime_type = excluded.mime_type,
		tg_message_id = excluded.tg_message_id,
		tg_channel_id = excluded.tg_channel_id,
		tg_file_id = excluded.tg_file_id,
		tg_access_hash = excluded.tg_access_hash,
		tg_file_reference = excluded.tg_file_reference,
		status = excluded.status;
	`
	_, err := d.db.Exec(query,
		n.ParentID, n.Name, n.Path, n.IsDir, n.Size, modUnix, n.MimeType,
		n.TGMessageID, n.TGChannelID, n.TGFileID, n.TGAccessHash, n.TGFileReference, n.Status)
	return err
}

func (d *DB) DeleteNode(p string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	p = CleanPath(p)
	if p == "/" {
		return fmt.Errorf("cannot delete root")
	}

	// Delete node and all nested children if directory
	_, err := d.db.Exec("DELETE FROM nodes WHERE path = ? OR path LIKE ?", p, p+"/%")
	return err
}

func (d *DB) RenameNode(oldPath, newPath string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	oldPath = CleanPath(oldPath)
	newPath = CleanPath(newPath)
	newName := path.Base(newPath)
	newParentDir := path.Dir(newPath)

	var newParentID int64 = 1
	if newParentDir != "/" {
		_ = d.db.QueryRow("SELECT id FROM nodes WHERE path = ?", newParentDir).Scan(&newParentID)
	}

	// Update the node itself
	_, err := d.db.Exec(`UPDATE nodes SET name = ?, path = ?, parent_id = ? WHERE path = ?`,
		newName, newPath, newParentID, oldPath)
	if err != nil {
		return err
	}

	// Update children if it was a folder
	_, err = d.db.Exec(`
		UPDATE nodes 
		SET path = ? || substr(path, ?)
		WHERE path LIKE ?`, newPath, len(oldPath)+1, oldPath+"/%")
	return err
}

func (d *DB) GetStats() (totalFiles int64, totalDirs int64, totalSize int64, err error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	row := d.db.QueryRow(`
		SELECT 
			COALESCE(SUM(CASE WHEN is_dir = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_dir = 1 AND path != '/' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN is_dir = 0 THEN size ELSE 0 END), 0)
		FROM nodes`)
	err = row.Scan(&totalFiles, &totalDirs, &totalSize)
	return
}
