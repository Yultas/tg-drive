package cmd

import (
	"fmt"

	"tg-drive/pkg/config"

	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "🛡️ Управление настройками прокси (SOCKS5, HTTP/HTTPS, MTProto)",
	Long: `Позволяет настроить работу TG-Drive через прокси-сервер.

Поддерживаемые протоколы:
  - SOCKS5:  socks5://127.0.0.1:1080 или socks5://user:pass@host:port
  - HTTP:    http://127.0.0.1:8080 или http://user:pass@host:port
  - MTProto: tg://proxy?server=1.2.3.4&port=443&secret=... или mtproto://secret@host:port`,
}

var proxySetCmd = &cobra.Command{
	Use:   "set [proxy_url]",
	Short: "Установить адрес прокси",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		proxyURL := args[0]
		cfg.ProxyURL = proxyURL
		if err := config.Save(cfg); err != nil {
			return err
		}

		fmt.Printf("✅ Прокси успешно настроен: %s\n", proxyURL)
		fmt.Println("ℹ️ Перезапустите службу или консольный режим: tg-drive service restart")
		return nil
	},
}

var proxyClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Отключить прокси (прямое подключение)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		cfg.ProxyURL = ""
		if err := config.Save(cfg); err != nil {
			return err
		}

		fmt.Println("✅ Прокси отключен. Используется прямое подключение.")
		fmt.Println("ℹ️ Перезапустите службу: tg-drive service restart")
		return nil
	},
}

var proxyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Показать текущую конфигурацию прокси",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if cfg.ProxyURL == "" {
			fmt.Println("⚪ Прокси: Отключен (прямое подключение к Telegram)")
		} else {
			fmt.Printf("🛡️ Прокси: Включен (%s)\n", cfg.ProxyURL)
		}
		return nil
	},
}

func init() {
	proxyCmd.AddCommand(proxySetCmd)
	proxyCmd.AddCommand(proxyClearCmd)
	proxyCmd.AddCommand(proxyStatusCmd)
	rootCmd.AddCommand(proxyCmd)
}
