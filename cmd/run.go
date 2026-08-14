package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"tg-drive/pkg/daemon"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "▶️ Запустить TG-Drive в консоли (WebDAV + Автосинхронизация + Монтирование)",
	RunE: func(cmd *cobra.Command, args []string) error {
		prog, err := daemon.NewProgram()
		if err != nil {
			return err
		}

		if err := prog.Start(nil); err != nil {
			return err
		}

		// Wait for SIGINT / SIGTERM to cleanly shut down and unmount
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan

		return prog.Stop(nil)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
