package cmd

import (
	"fmt"
	"strings"

	"tg-drive/pkg/config"
	"tg-drive/pkg/webdav"

	"github.com/spf13/cobra"
)

var mountDriveLetter string

var mountCmd = &cobra.Command{
	Use:   "mount [letter]",
	Short: "💾 Подключить виртуальный диск (например, Z: в Windows или /mnt/tgdrive в Linux)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if len(args) > 0 {
			cfg.DriveLetter = args[0]
			_ = config.Save(cfg)
		} else if mountDriveLetter != "" {
			cfg.DriveLetter = mountDriveLetter
			_ = config.Save(cfg)
		}

		url := fmt.Sprintf("http://%s:%d", cfg.WebDAVHost, cfg.WebDAVPort)
		mounter := webdav.NewMounter(cfg.DriveLetter, url)

		if err := mounter.Mount(); err != nil {
			return err
		}

		fmt.Printf("✅ Диск %s успешно подключен к %s\n", cfg.DriveLetter, url)
		return nil
	},
}

var unmountCmd = &cobra.Command{
	Use:   "unmount [letter]",
	Short: "⏏️ Отключить виртуальный диск",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.Load()
		drive := cfg.DriveLetter
		if len(args) > 0 {
			drive = args[0]
		}
		drive = strings.TrimSuffix(drive, ":") + ":"

		url := fmt.Sprintf("http://%s:%d", cfg.WebDAVHost, cfg.WebDAVPort)
		mounter := webdav.NewMounter(drive, url)
		if err := mounter.Unmount(); err != nil {
			return err
		}

		fmt.Printf("✅ Диск %s успешно отключен.\n", drive)
		return nil
	},
}

func init() {
	mountCmd.Flags().StringVarP(&mountDriveLetter, "drive", "d", "Z:", "Буква диска или точка монтирования")
	rootCmd.AddCommand(mountCmd)
	rootCmd.AddCommand(unmountCmd)
}
