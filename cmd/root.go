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

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	_, _ = config.Load()
}
