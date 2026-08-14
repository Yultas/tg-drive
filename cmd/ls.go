package cmd

import (
	"fmt"
	"strings"

	"tg-drive/pkg/config"
	"tg-drive/pkg/vfs"

	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls [путь]",
	Short: "📂 Просмотреть список файлов и папок в облаке Telegram",
	Long:  "Отображает структуру файлов в облаке, их размер, статус синхронизации и ID сообщения в Telegram.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targetPath := "/"
		if len(args) > 0 {
			targetPath = args[0]
		}
		targetPath = vfs.CleanPath(targetPath)

		db, err := vfs.OpenDB(config.GetDBPath())
		if err != nil {
			return fmt.Errorf("ошибка открытия базы данных: %w", err)
		}
		defer db.Close()

		parent, err := db.GetNodeByPath(targetPath)
		if err != nil {
			return fmt.Errorf("путь '%s' не найден в облаке", targetPath)
		}

		children, err := db.ListChildren(parent.ID)
		if err != nil {
			return fmt.Errorf("ошибка получения списка файлов: %w", err)
		}

		fmt.Printf("📂 Содержимое директории: %s\n", targetPath)
		fmt.Println("─────────────────────────────────────────────────────────────────────────────")
		fmt.Printf("%-32s %-12s %-18s %s\n", "Имя", "Размер", "Статус", "ID сообщения TG")
		fmt.Println("─────────────────────────────────────────────────────────────────────────────")

		if len(children) == 0 {
			fmt.Println("  (директория пуста)")
		}

		for _, node := range children {
			name := node.Name
			if node.IsDir {
				name = "📁 " + name + "/"
				fmt.Printf("%-32s %-12s %-18s %s\n", name, "<ПАПКА>", "—", "—")
			} else {
				name = "📄 " + name
				statusStr := "✅ synced"
				if strings.HasPrefix(node.Status, "uploading") {
					statusStr = "⏳ " + node.Status
				} else if node.Status == "error" {
					statusStr = "❌ error"
				}

				msgIDStr := "—"
				if node.TGMessageID != 0 {
					msgIDStr = fmt.Sprintf("#%d", node.TGMessageID)
				}

				fmt.Printf("%-32s %-12s %-18s %s\n", truncateStr(name, 31), formatBytes(node.Size), statusStr, msgIDStr)
			}
		}
		fmt.Println("─────────────────────────────────────────────────────────────────────────────")
		return nil
	},
}

func truncateStr(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-3]) + "..."
}

func init() {
	rootCmd.AddCommand(lsCmd)
}
