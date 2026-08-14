package webdav

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"tg-drive/pkg/config"
	"tg-drive/pkg/vfs"

	"golang.org/x/net/webdav"
)

type Server struct {
	cfg        *config.Config
	vfs        *vfs.VFS
	db         *vfs.DB
	handler    *webdav.Handler
	httpServer *http.Server
	mounter    *Mounter
}

func NewServer(cfg *config.Config, vfsInstance *vfs.VFS, db *vfs.DB) *Server {
	wdHandler := &webdav.Handler{
		FileSystem: vfsInstance,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				// Suppress harmless client aborts
				if !strings.Contains(err.Error(), "context canceled") {
					fmt.Printf("[WebDAV %s %s] %v\n", r.Method, r.URL.Path, err)
				}
			}
		},
	}

	url := fmt.Sprintf("http://%s:%d", cfg.WebDAVHost, cfg.WebDAVPort)
	mounter := NewMounter(cfg.DriveLetter, url)

	return &Server{
		cfg:     cfg,
		vfs:     vfsInstance,
		db:      db,
		handler: wdHandler,
		mounter: mounter,
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// API Status endpoint
	mux.HandleFunc("/api/status", s.handleAPIStatus)

	// WebDAV Handler for all file requests
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		s.handler.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf("%s:%d", s.cfg.WebDAVHost, s.cfg.WebDAVPort)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	fmt.Printf("🚀 WebDAV сервер запущен на http://%s\n", addr)

	// Auto-mount if configured
	if s.cfg.AutoMount {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := s.mounter.Mount(); err != nil {
				fmt.Printf("⚠️ Не удалось автоматически смонтировать диск %s: %v\n", s.cfg.DriveLetter, err)
			} else {
				fmt.Printf("💾 Диск %s успешно подключен!\n", s.cfg.DriveLetter)
			}
		}()
	}

	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	return s.httpServer.Serve(listener)
}

func (s *Server) Stop() {
	if s.mounter != nil && s.mounter.IsMounted() {
		_ = s.mounter.Unmount()
	}
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
	}
}

func (s *Server) GetMounter() *Mounter {
	return s.mounter
}

type StatusResponse struct {
	TotalFiles int64 `json:"total_files"`
	TotalDirs  int64 `json:"total_dirs"`
	TotalSize  int64 `json:"total_size"`
	IsMounted  bool  `json:"is_mounted"`
	Drive      string `json:"drive"`
	WebDAVUrl  string `json:"webdav_url"`
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	totalFiles, totalDirs, totalSize, _ := s.db.GetStats()

	url := fmt.Sprintf("http://%s:%d", s.cfg.WebDAVHost, s.cfg.WebDAVPort)
	res := StatusResponse{
		TotalFiles: totalFiles,
		TotalDirs:  totalDirs,
		TotalSize:  totalSize,
		IsMounted:  s.mounter.IsMounted(),
		Drive:      s.cfg.DriveLetter,
		WebDAVUrl:  url,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}
