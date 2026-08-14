package cmd

import (
	"fmt"

	"tg-drive/pkg/daemon"

	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service [install|uninstall|start|stop|restart|status]",
	Short: "⚙️ Управление фоновой системной службой (Windows Service / Linux systemd)",
	Long: `Позволяет установить и запускать TG-Drive в виде непрерывной фоновой службы,
которая автоматически стартует вместе с операционной системой.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		action := args[0]
		switch action {
		case "install", "uninstall", "start", "stop", "restart":
			return daemon.Control(action)
		default:
			return fmt.Errorf("неизвестное действие: %s (допустимы: install, uninstall, start, stop, restart)", action)
		}
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
}
