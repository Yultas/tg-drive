package vfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"tg-drive/pkg/config"
	"tg-drive/pkg/telegram"

	"golang.org/x/net/webdav"
)

type VFS struct {
	db       *DB
	tgClient *telegram.Client
	cfg      *config.Config
	tempDir  string
	mu       sync.RWMutex
}

func NewVFS(db *DB, tgClient *telegram.Client, cfg *config.Config) (*VFS, error) {
	tempDir := filepath.Join(config.GetDefaultConfigDir(), "temp_cache")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp cache dir: %w", err)
	}

	return &VFS{
		db:       db,
		tgClient: tgClient,
		cfg:      cfg,
		tempDir:  tempDir,
	}, nil
}

func (v *VFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	_, err := v.db.MkdirAll(name)
	return err
}

func (v *VFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	cleanName := CleanPath(name)

	node, err := v.db.GetNodeByPath(cleanName)
	if err == nil && node.IsDir {
		// Directory
		children, err := v.db.ListChildren(node.ID)
		if err != nil {
			return nil, err
		}
		var fileInfos []os.FileInfo
		for _, child := range children {
			fileInfos = append(fileInfos, &FileInfo{node: child})
		}
		return &DirFile{
			node:     node,
			children: fileInfos,
		}, nil
	}

	// Creating or Writing file
	isWrite := (flag&os.O_WRONLY != 0) || (flag&os.O_RDWR != 0) || (flag&os.O_CREATE != 0)

	if isWrite {
		tempFilePath := filepath.Join(v.tempDir, fmt.Sprintf("upload_%d_%s", time.Now().UnixNano(), path.Base(cleanName)))
		f, err := os.OpenFile(tempFilePath, flag, perm)
		if err != nil {
			return nil, err
		}

		return &WriteFile{
			vfs:          v,
			virtualPath:  cleanName,
			tempFilePath: tempFilePath,
			localFile:    f,
			node:         node,
		}, nil
	}

	// Reading an existing file
	if err != nil {
		return nil, os.ErrNotExist
	}

	return &ReadFile{
		vfs:  v,
		node: node,
		ctx:  ctx,
	}, nil
}

func (v *VFS) RemoveAll(ctx context.Context, name string) error {
	return v.db.DeleteNode(name)
}

func (v *VFS) Rename(ctx context.Context, oldName, newName string) error {
	return v.db.RenameNode(oldName, newName)
}

func (v *VFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	node, err := v.db.GetNodeByPath(name)
	if err != nil {
		return nil, os.ErrNotExist
	}
	return &FileInfo{node: node}, nil
}

// FileInfo wrapper implementing os.FileInfo
type FileInfo struct {
	node *Node
}

func (fi *FileInfo) Name() string       { return fi.node.Name }
func (fi *FileInfo) Size() int64        { return fi.node.Size }
func (fi *FileInfo) Mode() os.FileMode  {
	if fi.node.IsDir {
		return os.ModeDir | 0755
	}
	return 0644
}
func (fi *FileInfo) ModTime() time.Time { return fi.node.ModTime }
func (fi *FileInfo) IsDir() bool        { return fi.node.IsDir }
func (fi *FileInfo) Sys() interface{}   { return nil }

// DirFile handles directory listing
type DirFile struct {
	node     *Node
	children []os.FileInfo
	readPos  int
}

func (d *DirFile) Close() error               { return nil }
func (d *DirFile) Read(p []byte) (int, error) { return 0, io.EOF }
func (d *DirFile) Seek(offset int64, whence int) (int64, error) {
	if offset == 0 && whence == io.SeekStart {
		d.readPos = 0
		return 0, nil
	}
	return 0, fmt.Errorf("seek not supported on directory")
}
func (d *DirFile) Write(p []byte) (int, error) { return 0, fmt.Errorf("cannot write to directory") }
func (d *DirFile) Stat() (os.FileInfo, error)  { return &FileInfo{node: d.node}, nil }
func (d *DirFile) Readdir(count int) ([]os.FileInfo, error) {
	if d.readPos >= len(d.children) {
		if count <= 0 {
			return []os.FileInfo{}, nil
		}
		return nil, io.EOF
	}
	if count <= 0 {
		res := d.children[d.readPos:]
		d.readPos = len(d.children)
		return res, nil
	}
	end := d.readPos + count
	if end > len(d.children) {
		end = len(d.children)
	}
	res := d.children[d.readPos:end]
	d.readPos = end
	return res, nil
}

// WriteFile intercepts writes and uploads file to Telegram when closed
type WriteFile struct {
	vfs          *VFS
	virtualPath  string
	tempFilePath string
	localFile    *os.File
	node         *Node
	closed       bool
}

func (w *WriteFile) Write(p []byte) (int, error) {
	return w.localFile.Write(p)
}

func (w *WriteFile) Read(p []byte) (int, error) {
	return w.localFile.Read(p)
}

func (w *WriteFile) Seek(offset int64, whence int) (int64, error) {
	return w.localFile.Seek(offset, whence)
}

func (w *WriteFile) Stat() (os.FileInfo, error) {
	stat, err := w.localFile.Stat()
	if err != nil {
		return nil, err
	}
	name := filepath.Base(w.virtualPath)
	return &FileInfo{
		node: &Node{
			Name:    name,
			Path:    w.virtualPath,
			IsDir:   false,
			Size:    stat.Size(),
			ModTime: stat.ModTime(),
		},
	}, nil
}

func (w *WriteFile) Readdir(count int) ([]os.FileInfo, error) {
	return nil, fmt.Errorf("not a directory")
}

func (w *WriteFile) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	_ = w.localFile.Close()

	stat, err := os.Stat(w.tempFilePath)
	if err != nil || stat.Size() == 0 {
		_ = os.Remove(w.tempFilePath)
		return nil
	}

	// Asynchronously upload to Telegram or upload directly
	go func() {
		defer os.Remove(w.tempFilePath)

		res, err := w.vfs.tgClient.UploadFile(context.Background(), w.tempFilePath, nil)
		if err != nil {
			fmt.Printf("❌ Failed to upload %s to Telegram: %v\n", w.virtualPath, err)
			return
		}

		node := &Node{
			Name:            filepath.Base(w.virtualPath),
			Path:            w.virtualPath,
			IsDir:           false,
			Size:            res.Size,
			ModTime:         time.Now(),
			MimeType:        res.MimeType,
			TGMessageID:     res.MessageID,
			TGChannelID:     res.ChannelID,
			TGFileID:        res.FileID,
			TGAccessHash:    res.AccessHash,
			TGFileReference: res.FileReference,
			Status:          "synced",
		}
		_ = w.vfs.db.SaveFileNode(node)
		fmt.Printf("✅ Uploaded %s to Telegram (%d bytes)\n", w.virtualPath, res.Size)
	}()

	return nil
}

// ReadFile streams chunks on-demand from Telegram
type ReadFile struct {
	vfs    *VFS
	node   *Node
	ctx    context.Context
	offset int64
}

func (r *ReadFile) Stat() (os.FileInfo, error) {
	return &FileInfo{node: r.node}, nil
}

func (r *ReadFile) Read(p []byte) (int, error) {
	if r.offset >= r.node.Size {
		return 0, io.EOF
	}

	toRead := len(p)
	if int64(toRead) > r.node.Size-r.offset {
		toRead = int(r.node.Size - r.offset)
	}

	// Read chunk from Telegram
	chunk, err := r.vfs.tgClient.DownloadChunk(
		r.ctx,
		r.node.TGFileID,
		r.node.TGAccessHash,
		r.node.TGFileReference,
		r.offset,
		toRead,
	)
	if err != nil {
		return 0, err
	}

	n := copy(p, chunk)
	r.offset += int64(n)
	if r.offset >= r.node.Size {
		return n, io.EOF
	}
	return n, nil
}

func (r *ReadFile) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = r.offset + offset
	case io.SeekEnd:
		newOffset = r.node.Size + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if newOffset < 0 {
		return 0, fmt.Errorf("negative position")
	}
	r.offset = newOffset
	return r.offset, nil
}

func (r *ReadFile) Write(p []byte) (int, error)             { return 0, fmt.Errorf("read-only") }
func (r *ReadFile) Readdir(count int) ([]os.FileInfo, error) { return nil, fmt.Errorf("not a directory") }
func (r *ReadFile) Close() error                           { return nil }
