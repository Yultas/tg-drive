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
	db          *DB
	tgClient    *telegram.Client
	cfg         *config.Config
	tempDir     string
	activeFiles map[string]string // virtualPath -> localTempPath
	mu          sync.RWMutex
}

func NewVFS(db *DB, tgClient *telegram.Client, cfg *config.Config) (*VFS, error) {
	tempDir := filepath.Join(config.GetDefaultConfigDir(), "temp_cache")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp cache dir: %w", err)
	}

	return &VFS{
		db:          db,
		tgClient:    tgClient,
		cfg:         cfg,
		tempDir:     tempDir,
		activeFiles: make(map[string]string),
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
		// Directory listing
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
	isWrite := (flag&os.O_WRONLY != 0) || (flag&os.O_RDWR != 0) || (flag&os.O_CREATE != 0) || (flag&os.O_TRUNC != 0)

	if isWrite {
		tempFilePath := filepath.Join(v.tempDir, fmt.Sprintf("upload_%d_%s", time.Now().UnixNano(), path.Base(cleanName)))
		f, err := os.OpenFile(tempFilePath, flag, perm)
		if err != nil {
			return nil, err
		}

		v.mu.Lock()
		v.activeFiles[cleanName] = tempFilePath
		v.mu.Unlock()

		return &WriteFile{
			vfs:          v,
			virtualPath:  cleanName,
			tempFilePath: tempFilePath,
			localFile:    f,
			node:         node,
		}, nil
	}

	// Reading an existing file
	v.mu.RLock()
	activeTempPath, hasActive := v.activeFiles[cleanName]
	v.mu.RUnlock()

	if hasActive {
		// File is currently being uploaded or locally cached, serve from local file
		f, err := os.Open(activeTempPath)
		if err == nil {
			return &LocalReadFile{
				file:        f,
				virtualPath: cleanName,
			}, nil
		}
	}

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
	v.mu.Lock()
	delete(v.activeFiles, CleanPath(name))
	v.mu.Unlock()
	return v.db.DeleteNode(name)
}

func (v *VFS) Rename(ctx context.Context, oldName, newName string) error {
	v.mu.Lock()
	cleanOld := CleanPath(oldName)
	cleanNew := CleanPath(newName)
	if tempPath, ok := v.activeFiles[cleanOld]; ok {
		delete(v.activeFiles, cleanOld)
		v.activeFiles[cleanNew] = tempPath
	}
	v.mu.Unlock()
	return v.db.RenameNode(oldName, newName)
}

func (v *VFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	cleanName := CleanPath(name)
	node, err := v.db.GetNodeByPath(cleanName)
	if err != nil {
		// Check active files
		v.mu.RLock()
		activeTempPath, hasActive := v.activeFiles[cleanName]
		v.mu.RUnlock()
		if hasActive {
			if st, err := os.Stat(activeTempPath); err == nil {
				return &FileInfo{
					node: &Node{
						Name:    path.Base(cleanName),
						Path:    cleanName,
						IsDir:   false,
						Size:    st.Size(),
						ModTime: st.ModTime(),
					},
				}, nil
			}
		}
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
func (fi *FileInfo) Mode() os.FileMode {
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
	if err != nil {
		w.vfs.mu.Lock()
		delete(w.vfs.activeFiles, w.virtualPath)
		w.vfs.mu.Unlock()
		return nil
	}

	fileSize := stat.Size()
	now := time.Now()

	// 1. Immediately register file in SQLite so Windows Explorer Stat / PROPFIND succeeds immediately
	node := &Node{
		Name:     filepath.Base(w.virtualPath),
		Path:     w.virtualPath,
		IsDir:    false,
		Size:     fileSize,
		ModTime:  now,
		MimeType: "application/octet-stream",
		Status:   "uploading",
	}
	_ = w.vfs.db.SaveFileNode(node)

	// If 0-byte file (placeholder), keep in DB and clean temp file
	if fileSize == 0 {
		w.vfs.mu.Lock()
		delete(w.vfs.activeFiles, w.virtualPath)
		w.vfs.mu.Unlock()
		_ = os.Remove(w.tempFilePath)
		return nil
	}

	// 2. Asynchronously upload to Telegram in background
	go func(targetNode *Node, tempPath string, vPath string) {
		defer func() {
			w.vfs.mu.Lock()
			delete(w.vfs.activeFiles, vPath)
			w.vfs.mu.Unlock()
			_ = os.Remove(tempPath)
		}()

		fmt.Printf("⬆️ Загрузка файла в Telegram: %s (%s)...\n", vPath, formatBytes(targetNode.Size))

		res, err := w.vfs.tgClient.UploadFile(context.Background(), tempPath, nil)
		if err != nil {
			fmt.Printf("❌ Ошибка загрузки %s в Telegram: %v\n", vPath, err)
			targetNode.Status = "error"
			_ = w.vfs.db.SaveFileNode(targetNode)
			return
		}

		targetNode.Size = res.Size
		targetNode.MimeType = res.MimeType
		targetNode.TGMessageID = res.MessageID
		targetNode.TGChannelID = res.ChannelID
		targetNode.TGFileID = res.FileID
		targetNode.TGAccessHash = res.AccessHash
		targetNode.TGFileReference = res.FileReference
		targetNode.Status = "synced"
		_ = w.vfs.db.SaveFileNode(targetNode)
		fmt.Printf("✅ Загружен %s в Telegram (%d байт)\n", vPath, res.Size)
	}(node, w.tempFilePath, w.virtualPath)

	return nil
}

// LocalReadFile reads a local file directly when it is being cached/uploaded
type LocalReadFile struct {
	file        *os.File
	virtualPath string
}

func (l *LocalReadFile) Close() error {
	return l.file.Close()
}

func (l *LocalReadFile) Read(p []byte) (int, error) {
	return l.file.Read(p)
}

func (l *LocalReadFile) Seek(offset int64, whence int) (int64, error) {
	return l.file.Seek(offset, whence)
}

func (l *LocalReadFile) Stat() (os.FileInfo, error) {
	st, err := l.file.Stat()
	if err != nil {
		return nil, err
	}
	return &FileInfo{
		node: &Node{
			Name:    filepath.Base(l.virtualPath),
			Path:    l.virtualPath,
			IsDir:   false,
			Size:    st.Size(),
			ModTime: st.ModTime(),
		},
	}, nil
}

func (l *LocalReadFile) Write(p []byte) (int, error)             { return 0, fmt.Errorf("read-only") }
func (l *LocalReadFile) Readdir(count int) ([]os.FileInfo, error) { return nil, fmt.Errorf("not a directory") }

// ReadFile streams chunks on-demand from Telegram with 512KB chunk buffering
type ReadFile struct {
	vfs         *VFS
	node        *Node
	ctx         context.Context
	offset      int64
	chunkBuf    []byte
	chunkOffset int64
}

func (r *ReadFile) Stat() (os.FileInfo, error) {
	return &FileInfo{node: r.node}, nil
}

func (r *ReadFile) Read(p []byte) (int, error) {
	if r.offset >= r.node.Size {
		return 0, io.EOF
	}

	// 1. Check if current offset is within cached chunk
	if r.chunkBuf != nil && r.offset >= r.chunkOffset && r.offset < r.chunkOffset+int64(len(r.chunkBuf)) {
		bufStart := int(r.offset - r.chunkOffset)
		n := copy(p, r.chunkBuf[bufStart:])
		r.offset += int64(n)
		if r.offset >= r.node.Size {
			return n, io.EOF
		}
		return n, nil
	}

	// 2. Fetch new 512 KB chunk from Telegram
	fetchSize := 512 * 1024
	if int64(fetchSize) > r.node.Size-r.offset {
		fetchSize = int(r.node.Size - r.offset)
	}

	chunk, err := r.vfs.tgClient.DownloadChunk(
		r.ctx,
		r.node.TGFileID,
		r.node.TGAccessHash,
		r.node.TGFileReference,
		r.offset,
		fetchSize,
	)
	if err != nil {
		return 0, err
	}

	r.chunkBuf = chunk
	r.chunkOffset = r.offset

	n := copy(p, r.chunkBuf)
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

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
