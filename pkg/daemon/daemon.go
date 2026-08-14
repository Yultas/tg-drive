package daemon

import (
	"context"
	"fmt"
	"os"

	"tg-drive/pkg/config"
	"tg-drive/pkg/syncer"
	"tg-drive/pkg/telegram"
	"tg-drive/pkg/vfs"
	"tg-drive/pkg/webdav"

	"github.com/kardianos/service"
)

type Program struct {
	cfg      *config.Config
	db       *vfs.DB
	tgClient *telegram.Client
	vfs      *vfs.VFS
	server   *webdav.Server
	syncer   *syncer.Syncer
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewProgram() (*Program, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	db, err := vfs.OpenDB(config.GetDBPath())
	if err != nil {
		return nil, err
	}

	tgClient := telegram.NewClient(cfg)
	vfsInstance, err := vfs.NewVFS(db, tgClient, cfg)
	if err != nil {
		return nil, err
	}

	server := webdav.NewServer(cfg, vfsInstance, db)
	syncEngine, err := syncer.NewSyncer(cfg, db, tgClient)
	if err != nil {
		return nil, err
	}

	return &Program{
		cfg:      cfg,
		db:       db,
		tgClient: tgClient,
		vfs:      vfsInstance,
		server:   server,
		syncer:   syncEngine,
	}, nil
}

func (p *Program) Start(s service.Service) error {
	p.ctx, p.cancel = context.WithCancel(context.Background())

	go p.run()
	return nil
}

func (p *Program) run() {
	fmt.Println("🚀 Запуск ядра TG-Drive...")

	// 1. Подключение к Telegram
	if err := p.tgClient.Start(p.ctx); err != nil {
		fmt.Printf("❌ Ошибка подключения к Telegram: %v\n", err)
		return
	}

	user := p.tgClient.GetUser()
	if user != nil {
		fmt.Printf("👤 Авторизован как: %s %s (@%s)\n", user.FirstName, user.LastName, user.Username)
	}

	// 2. Запуск модуля автосинхронизации
	if err := p.syncer.Start(p.ctx); err != nil {
		fmt.Printf("⚠️ Ошибка запуска автосинхронизации: %v\n", err)
	}

	// 3. Запуск WebDAV сервера и монтирования диска
	if err := p.server.Start(p.ctx); err != nil {
		fmt.Printf("❌ Ошибка WebDAV сервера: %v\n", err)
	}
}

func (p *Program) Stop(s service.Service) error {
	fmt.Println("🛑 Остановка сервиса TG-Drive...")
	if p.cancel != nil {
		p.cancel()
	}
	if p.server != nil {
		p.server.Stop()
	}
	if p.syncer != nil {
		p.syncer.Stop()
	}
	if p.tgClient != nil {
		p.tgClient.Stop()
	}
	if p.db != nil {
		_ = p.db.Close()
	}
	return nil
}

func GetServiceConfig() *service.Config {
	configDir := config.GetDefaultConfigDir()
	return &service.Config{
		Name:        "TGDriveService",
		DisplayName: "Telegram Drive Storage Service",
		Description: "Фоновая служба виртуального облачного диска Telegram с автосинхронизацией",
		Arguments:   []string{"run", "--config-dir", configDir},
	}
}

func Run() error {
	prog, err := NewProgram()
	if err != nil {
		return err
	}

	svcConfig := GetServiceConfig()
	s, err := service.New(prog, svcConfig)
	if err != nil {
		return err
	}

	return s.Run()
}

func Control(action string) error {
	sessionPath := config.GetSessionPath()
	if action == "start" || action == "install" {
		if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
			return fmt.Errorf("сессия Telegram не найдена!\nСначала выполните авторизацию в терминале:\n  .\\tg-drive.exe auth\nА затем запустите службу.")
		}
	}

	prog, err := NewProgram()
	if err != nil {
		return err
	}

	svcConfig := GetServiceConfig()
	s, err := service.New(prog, svcConfig)
	if err != nil {
		return err
	}

	err = service.Control(s, action)
	if err != nil {
		return fmt.Errorf("ошибка выполнения команды службы %s: %w", action, err)
	}

	fmt.Printf("✅ Успешно выполнена команда службы: %s\n", action)
	return nil
}
