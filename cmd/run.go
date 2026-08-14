package cmd

import (
	"tg-drive/pkg/daemon"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "▶️ Запустить TG-Drive в консоли (WebDAV + Автосинхронизация + Монтирование)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return daemon.Run()
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
