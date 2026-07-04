package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	appstate "cloud-clipboard/internal/app"
	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/db"
	httpapi "cloud-clipboard/internal/http"
	"cloud-clipboard/internal/jobs"
	"cloud-clipboard/internal/logger"
	"cloud-clipboard/internal/realtime"
	"cloud-clipboard/internal/service"
	"cloud-clipboard/internal/store"
	"cloud-clipboard/version"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.String())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("load config: %v", err)
	}

	logger.SetLevel(cfg.LogLevel)
	logger.Info("cloud-clipboard %s starting", version.Short())
	logger.Info("log level: %s", logger.LevelName())
	logConfig(cfg)

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Fatal("open database: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database, mustReadEmbedded("sql/init.sql")); err != nil {
		logger.Fatal("migrate database: %v", err)
	}

	now := time.Now().UTC()
	settingsStore := store.NewSettingsStore(database)
	if err := settingsStore.EnsureDefaults(now); err != nil {
		logger.Fatal("ensure default settings: %v", err)
	}

	adminStore := store.NewAdminStore(database)
	adminService := service.NewAdminService(adminStore)
	if err := adminService.EnsureInitialized(cfg.ResetAdminPassword, now); err != nil {
		logger.Fatal("ensure admin credentials: %v", err)
	}
	// 检查是否首次运行（默认密码激活）
	cred, found, _ := adminService.Status()
	if found && cred.DefaultPasswordActive {
		logger.Warn("============================================")
		logger.Warn(" 首次运行！默认管理员密码: %s", config.DefaultAdminPassword)
		logger.Warn(" 请尽快登录管理后台修改密码！")
		logger.Warn("============================================")
	}

	roomStore := store.NewRoomStore(database)
	messageStore := store.NewMessageStore(database)
	uploadSessionStore := store.NewUploadSessionStore(database)
	statisticsStore := store.NewStatisticsStore(database)
	fileService := service.NewFileService(cfg.UploadDir)
	hub := realtime.NewHub()
	roomService := service.NewRoomService(settingsStore, roomStore, messageStore, uploadSessionStore, fileService, hub)
	settingsService := service.NewSettingsService(settingsStore)
	maintenanceService := service.NewMaintenanceService(settingsStore, roomStore, messageStore, fileService, roomService, statisticsStore)

	staticFiles, err := fs.Sub(embeddedFiles, "static")
	if err != nil {
		logger.Fatal("load embedded static files: %v", err)
	}

	application := &appstate.App{
		Config:      cfg,
		DB:          database,
		Hub:         hub,
		Admin:       adminService,
		Room:        roomService,
		Settings:    settingsService,
		Files:       fileService,
		Maintenance: maintenanceService,
		Statistics:  statisticsStore,
	}

	handler, err := httpapi.NewRouter(application, staticFiles, uploadSessionStore)
	if err != nil {
		logger.Fatal("build router: %v", err)
	}
	application.Handler = handler

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: handler,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go jobs.StartCleanup(ctx, cfg.CleanupInterval, maintenanceService)
	go func() {
		<-ctx.Done()
		logger.Info("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Info("listening on %s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("serve http: %v", err)
	}
	logger.Info("server stopped")
}

func logConfig(cfg config.Config) {
	logger.Info("--- configuration ---")
	logger.Info("  listen:       %s", cfg.ListenAddr)
	logger.Info("  database:     %s", cfg.DBPath)
	logger.Info("  uploads:      %s", cfg.UploadDir)
	logger.Info("  cleanup:      every %v", cfg.CleanupInterval)
	logger.Info("  max upload:   %d bytes", cfg.MaxUploadBytes)
	if cfg.ResetAdminPassword != "" {
		logger.Info("  reset admin:  yes")
	}
	logger.Info("----------------------")
}

func mustReadEmbedded(path string) string {
	content, err := embeddedFiles.ReadFile(path)
	if err != nil {
		logger.Fatal("read embedded file %s: %v", path, err)
	}
	return string(content)
}
