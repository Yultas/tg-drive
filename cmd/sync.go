package cmd

import (
	"fmt"
	"path/filepath"

	"tg-drive/pkg/config"

	"github.com/spf13/cobra"
)

var (
	syncLocalDir  string
	syncRemoteDir string
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "🔄 Управление автосинхронизацией папок",
}

var syncAddCmd = &cobra.Command{
	Use:   "add",
	Short: "➕ Добавить папку в список автосинхронизации",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if syncLocalDir == "" {
			return fmt.Errorf("укажите локальную папку через --local")
		}

		absLocal, err := filepath.Abs(syncLocalDir)
		if err != nil {
			return err
		}

		if syncRemoteDir == "" {
			syncRemoteDir = "/" + filepath.Base(absLocal)
		}

		// Check duplicate
		for _, m := range cfg.SyncList {
			if m.LocalPath == absLocal {
				fmt.Printf("⚠️ Папка %s уже находится в списке синхронизации.\n", absLocal)
				return nil
			}
		}

		cfg.SyncList = append(cfg.SyncList, config.SyncMapping{
			ID:         fmt.Sprintf("sync_%d", len(cfg.SyncList)+1),
			LocalPath:  absLocal,
			RemotePath: syncRemoteDir,
			Enabled:    true,
		})

		if err := config.Save(cfg); err != nil {
			return err
		}

		fmt.Printf("✅ Папка добавлена в автосинхронизацию:\n  Локально: %s\n  Облако:   %s\n", absLocal, syncRemoteDir)
		return nil
	},
}

var syncListCmd = &cobra.Command{
	Use:   "list",
	Short: "📋 Показать список синхронизируемых папок",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if len(cfg.SyncList) == 0 {
			fmt.Println("ℹ️ Список папок для автосинхронизации пуст. Добавьте папку командой: tg-drive sync add --local <путь>")
			return nil
		}

		fmt.Println("📁 Настроенные папки для автосинхронизации:")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		for i, m := range cfg.SyncList {
			status := "🟢 Включено"
			if !m.Enabled {
				status = "🔴 Отключено"
			}
			fmt.Printf("[%d] %s -> %s (%s)\n", i+1, m.LocalPath, m.RemotePath, status)
		}
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		return nil
	},
}

func init() {
	syncAddCmd.Flags().StringVarP(&syncLocalDir, "local", "l", "", "Локальный путь к папке")
	syncAddCmd.Flags().StringVarP(&syncRemoteDir, "remote", "r", "", "Путь к папке в облаке Telegram (по умолчанию: имя папки)")

	syncCmd.AddCommand(syncAddCmd)
	syncCmd.AddCommand(syncListCmd)
	rootCmd.AddCommand(syncCmd)
}
