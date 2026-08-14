package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tg-drive/pkg/config"
	"tg-drive/pkg/vfs"
	"tg-drive/pkg/webdav"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "📊 Показать текущий статус хранилища, диска и синхронизации",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		db, err := vfs.OpenDB(config.GetDBPath())
		if err != nil {
			return err
		}
		defer db.Close()

		totalFiles, totalDirs, totalSize, err := db.GetStats()
		if err != nil {
			return err
		}

		// Try pinging running WebDAV server
		url := fmt.Sprintf("http://%s:%d/api/status", cfg.WebDAVHost, cfg.WebDAVPort)
		client := &http.Client{Timeout: 1 * time.Second}
		resp, err := client.Get(url)
		
		serverRunning := false
		var liveStatus webdav.StatusResponse
		if err == nil && resp.StatusCode == 200 {
			serverRunning = true
			_ = json.NewDecoder(resp.Body).Decode(&liveStatus)
			_ = resp.Body.Close()
		}

		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("  ⚡ TG-Drive — Статус облачного хранилища Telegram")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		
		if serverRunning {
			fmt.Printf("🟢 Сервер:         Активен (http://%s:%d)\n", cfg.WebDAVHost, cfg.WebDAVPort)
			mountStr := "❌ Не смонтирован"
			if liveStatus.IsMounted {
				mountStr = fmt.Sprintf("✅ Смонтирован на %s", liveStatus.Drive)
			}
			fmt.Printf("💾 Диск:           %s\n", mountStr)
		} else {
			fmt.Printf("⚪ Сервер:         Не запущен (запустите через 'tg-drive run' или 'tg-drive service start')\n")
		}

		fmt.Printf("📁 Всего папок:    %d\n", totalDirs)
		fmt.Printf("📄 Всего файлов:   %d\n", totalFiles)
		fmt.Printf("📦 Общий объем:    %s\n", formatBytes(totalSize))
		fmt.Printf("🔄 Папок на синхро: %d\n", len(cfg.SyncList))
		if cfg.ProxyURL != "" {
			fmt.Printf("🛡️ Прокси:         %s\n", cfg.ProxyURL)
		} else {
			fmt.Printf("🛡️ Прокси:         Прямое подключение (без прокси)\n")
		}
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		return nil
	},
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
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
