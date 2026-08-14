package cmd

import (
	"context"
	"fmt"
	"time"

	"tg-drive/pkg/config"
	"tg-drive/pkg/telegram"

	"github.com/spf13/cobra"
)

var (
	flagPhone   string
	flagApiID   int
	flagApiHash string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "🔑 Авторизация в Telegram (по номеру телефона или QR-коду)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if flagPhone != "" {
			cfg.PhoneNumber = flagPhone
		}
		if flagApiID != 0 {
			cfg.ApiID = flagApiID
		}
		if flagApiHash != "" {
			cfg.ApiHash = flagApiHash
		}
		_ = config.Save(cfg)

		fmt.Println("🚀 Инициализация клиента Telegram...")
		client := telegram.NewClient(cfg)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		if err := client.Start(ctx); err != nil {
			return fmt.Errorf("ошибка авторизации: %w", err)
		}

		user := client.GetUser()
		if user != nil {
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			fmt.Printf("✅ Авторизация успешна!\n")
			fmt.Printf("👤 Имя: %s %s\n", user.FirstName, user.LastName)
			if user.Username != "" {
				fmt.Printf("🏷 Username: @%s\n", user.Username)
			}
			if user.Premium {
				fmt.Printf("⭐ Статус: Telegram Premium (лимит 4 ГБ активен!)\n")
			} else {
				fmt.Printf("ℹ️ Статус: Обычный аккаунт (лимит 2 ГБ)\n")
			}
			fmt.Printf("💾 Сессия сохранена в: %s\n", config.GetSessionPath())
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		}

		client.Stop()
		return nil
	},
}

func init() {
	authCmd.Flags().StringVarP(&flagPhone, "phone", "p", "", "Номер телефона аккаунта Telegram")
	authCmd.Flags().IntVar(&flagApiID, "api-id", 0, "Пользовательский API ID (с my.telegram.org)")
	authCmd.Flags().StringVar(&flagApiHash, "api-hash", "", "Пользовательский API Hash (с my.telegram.org)")
	rootCmd.AddCommand(authCmd)
}
