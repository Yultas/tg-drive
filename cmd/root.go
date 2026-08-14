package cmd

import (
	"fmt"
	"os"

	"tg-drive/pkg/config"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "tg-drive",
	Short: "⚡ TG-Drive: Виртуальный облачный диск и автосинхронизация поверх Telegram (до 4 ГБ)",
	Long: `TG-Drive превращает ваш Telegram в неограниченное персональное облачное хранилище.
Поддерживает файлы до 4 ГБ (Telegram Premium), виртуальный диск (Z: в Windows, /mnt в Linux),
и автоматическую фоновую синхронизацию выбранных локальных папок.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
}

var (
	cfgDir    string
	proxyFlag string
)

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgDir, "config-dir", "", "Кастомная директория конфигурации")
	rootCmd.PersistentFlags().StringVar(&proxyFlag, "proxy", "", "Адрес прокси (socks5://..., http://..., tg://...)")
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	if cfgDir != "" {
		config.SetConfigDir(cfgDir)
	}
	cfg, err := config.Load()
	if err == nil && proxyFlag != "" {
		cfg.ProxyURL = proxyFlag
	}
}
