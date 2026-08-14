package syncer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tg-drive/pkg/config"
	"tg-drive/pkg/telegram"
	"tg-drive/pkg/vfs"

	"github.com/fsnotify/fsnotify"
)

type SyncTask struct {
	LocalPath  string
	RemotePath string
	ModTime    time.Time
	Size       int64
}

type Syncer struct {
	cfg      *config.Config
	db       *vfs.DB
	tgClient *telegram.Client
	watcher  *fsnotify.Watcher
	queue    chan SyncTask
	debouncers map[string]*time.Timer
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewSyncer(cfg *config.Config, db *vfs.DB, tgClient *telegram.Client) (*Syncer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fs watcher: %w", err)
	}

	return &Syncer{
		cfg:        cfg,
		db:         db,
		tgClient:   tgClient,
		watcher:    watcher,
		queue:      make(chan SyncTask, 100),
		debouncers: make(map[string]*time.Timer),
	}, nil
}

func (s *Syncer) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Watch all configured folders
	for _, mapping := range s.cfg.SyncList {
		if mapping.Enabled {
			if err := s.AddFolder(mapping.LocalPath, mapping.RemotePath); err != nil {
				fmt.Printf("⚠️ Ошибка подключения синхронизации папки %s: %v\n", mapping.LocalPath, err)
			}
		}
	}

	// Start worker pool for uploads
	go s.workerLoop()
	go s.eventLoop()

	return nil
}

func (s *Syncer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.watcher != nil {
		_ = s.watcher.Close()
	}
}

func (s *Syncer) AddFolder(localPath, remotePath string) error {
	absLocal, err := filepath.Abs(localPath)
	if err != nil {
		return err
	}

	// Walk and add all subdirectories to watcher
	err = filepath.Walk(absLocal, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return s.watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("📁 Папка подключена к автосинхронизации: %s -> %s\n", absLocal, remotePath)

	// Initial scan of existing files
	go s.initialScan(absLocal, remotePath)
	return nil
}

func (s *Syncer) initialScan(localBase, remoteBase string) {
	_ = filepath.Walk(localBase, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if isIgnoredFile(info.Name()) {
			return nil
		}

		rel, _ := filepath.Rel(localBase, path)
		remotePath := filepath.ToSlash(filepath.Join(remoteBase, rel))

		// Check if file is already in DB with same size and mod time
		node, err := s.db.GetNodeByPath(remotePath)
		if err == nil && node != nil && node.Size == info.Size() {
			return nil // Already synced
		}

		s.scheduleUpload(path, remotePath)
		return nil
	})
}

func (s *Syncer) eventLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			s.handleFSEvent(event)
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("⚠️ Ошибка файлового наблюдателя: %v\n", err)
		}
	}
}

func (s *Syncer) handleFSEvent(event fsnotify.Event) {
	if isIgnoredFile(filepath.Base(event.Name)) {
		return
	}

	// If directory created, add to watcher
	if event.Has(fsnotify.Create) {
		stat, err := os.Stat(event.Name)
		if err == nil && stat.IsDir() {
			_ = s.watcher.Add(event.Name)
			return
		}
	}

	// Find matching mapping
	var matchLocal, matchRemote string
	for _, m := range s.cfg.SyncList {
		if strings.HasPrefix(event.Name, m.LocalPath) {
			matchLocal = m.LocalPath
			matchRemote = m.RemotePath
			break
		}
	}
	if matchLocal == "" {
		return
	}

	rel, _ := filepath.Rel(matchLocal, event.Name)
	remotePath := filepath.ToSlash(filepath.Join(matchRemote, rel))

	if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
		s.debounce(event.Name, remotePath)
	} else if event.Has(fsnotify.Remove) {
		_ = s.db.DeleteNode(remotePath)
		fmt.Printf("🗑 Удален файл из облака: %s\n", remotePath)
	}
}

func (s *Syncer) debounce(localPath, remotePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t, exists := s.debouncers[localPath]; exists {
		t.Stop()
	}

	s.debouncers[localPath] = time.AfterFunc(2*time.Second, func() {
		s.scheduleUpload(localPath, remotePath)
	})
}

func (s *Syncer) scheduleUpload(localPath, remotePath string) {
	stat, err := os.Stat(localPath)
	if err != nil || stat.IsDir() {
		return
	}

	s.queue <- SyncTask{
		LocalPath:  localPath,
		RemotePath: remotePath,
		ModTime:    stat.ModTime(),
		Size:       stat.Size(),
	}
}

func (s *Syncer) workerLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case task := <-s.queue:
			s.processUpload(task)
		}
	}
}

func (s *Syncer) processUpload(task SyncTask) {
	fmt.Printf("⬆️ Начало синхронизации: %s (%s)\n", filepath.Base(task.LocalPath), formatBytes(task.Size))

	progress := func(uploaded, total int64, speed float64) {
		pct := float64(uploaded) / float64(total) * 100
		speedMB := speed / (1024 * 1024)
		fmt.Printf("\r  ⏳ Прогресс: %.1f%% (%.2f МБ/с)", pct, speedMB)
	}

	res, err := s.tgClient.UploadFile(s.ctx, task.LocalPath, progress)
	fmt.Println()
	if err != nil {
		fmt.Printf("❌ Ошибка синхронизации %s: %v\n", task.LocalPath, err)
		return
	}

	node := &vfs.Node{
		Name:            filepath.Base(task.RemotePath),
		Path:            task.RemotePath,
		IsDir:           false,
		Size:            res.Size,
		ModTime:         task.ModTime,
		MimeType:        res.MimeType,
		TGMessageID:     res.MessageID,
		TGChannelID:     res.ChannelID,
		TGFileID:        res.FileID,
		TGAccessHash:    res.AccessHash,
		TGFileReference: res.FileReference,
		Status:          "synced",
	}
	_ = s.db.SaveFileNode(node)

	fmt.Printf("✅ Файл успешно синхронизирован в Telegram: %s\n", task.RemotePath)
}

func isIgnoredFile(name string) bool {
	return strings.HasPrefix(name, ".") ||
		strings.HasPrefix(name, "~$") ||
		strings.HasSuffix(name, ".tmp") ||
		strings.HasSuffix(name, ".crdownload") ||
		strings.HasSuffix(name, ".DS_Store")
}

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
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
