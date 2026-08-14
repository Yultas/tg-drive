package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

type SyncMapping struct {
	ID         string `json:"id"`
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"`
	Enabled    bool   `json:"enabled"`
}

type Config struct {
	ApiID        int           `json:"api_id"`
	ApiHash      string        `json:"api_hash"`
	PhoneNumber  string        `json:"phone_number,omitempty"`
	StorageType  string        `json:"storage_type"` // "channel" or "saved_messages"
	ChannelID    int64         `json:"channel_id,omitempty"`
	ChannelTitle string        `json:"channel_title,omitempty"`
	
	// WebDAV & Mount
	WebDAVHost   string `json:"webdav_host"`
	WebDAVPort   int    `json:"webdav_port"`
	DriveLetter  string `json:"drive_letter"` // e.g. "Z:" on Windows or "/mnt/tgdrive" on Linux
	AutoMount    bool   `json:"auto_mount"`
	
	// Performance & Limits
	MaxWorkers   int   `json:"max_workers"`   // Concurrent upload workers (default 4-8)
	ChunkSizeKB  int   `json:"chunk_size_kb"` // 512 KB default for MTProto big files
	MaxFileSize  int64 `json:"max_file_size"` // 4GB for Telegram Premium (4 * 1024 * 1024 * 1024)
	
	// Web UI
	EnableWebUI bool `json:"enable_web_ui"`
	
	// Proxy (SOCKS5, HTTP, MTProto)
	ProxyURL string `json:"proxy_url,omitempty"`
	
	// Folder Sync
	SyncList []SyncMapping `json:"sync_list"`
}

var (
	cfgInstance     *Config
	cfgMu           sync.RWMutex
	customConfigDir string
)

func SetConfigDir(dir string) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	customConfigDir = dir
}

func GetDefaultConfigDir() string {
	if customConfigDir != "" {
		return customConfigDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".tgdrive")
}

func GetConfigPath() string {
	return filepath.Join(GetDefaultConfigDir(), "config.json")
}

func GetSessionPath() string {
	return filepath.Join(GetDefaultConfigDir(), "session.json")
}

func GetDBPath() string {
	return filepath.Join(GetDefaultConfigDir(), "metadata.db")
}

func DefaultConfig() *Config {
	drive := "Z:"
	if runtime.GOOS != "windows" {
		drive = "/mnt/tgdrive"
	}

	return &Config{
		ApiID:        0,
		ApiHash:      "",
		StorageType:  "channel",
		WebDAVHost:   "127.0.0.1",
		WebDAVPort:   8585,
		DriveLetter:  drive,
		AutoMount:    true,
		MaxWorkers:   6,
		ChunkSizeKB:  512,
		MaxFileSize:  4 * 1024 * 1024 * 1024, // 4GB
		EnableWebUI:  true,
		SyncList:     make([]SyncMapping, 0),
	}
}

func Load() (*Config, error) {
	cfgMu.Lock()
	defer cfgMu.Unlock()

	dir := GetDefaultConfigDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config dir: %w", err)
	}

	cfgPath := GetConfigPath()
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfg := DefaultConfig()
		cfgInstance = cfg
		_ = saveLocked(cfg, cfgPath)
		return cfg, nil
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfgInstance = cfg
	return cfg, nil
}

func Save(cfg *Config) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()

	cfgInstance = cfg
	return saveLocked(cfg, GetConfigPath())
}

func saveLocked(cfg *Config, path string) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func Get() *Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	if cfgInstance == nil {
		cfg, _ := Load()
		return cfg
	}
	return cfgInstance
}
