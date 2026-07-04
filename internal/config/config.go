package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// TimeLocation is the configured timezone location, set during Load().
// Defaults to Asia/Shanghai.
var TimeLocation *time.Location

const (
	defaultListenAddr               = ":8080"
	defaultDBPath                   = "./data/clipboard.db"
	defaultUploadDir                = "./data/uploads"
	defaultLogLevel                 = "info"
	defaultCleanupIntervalSec       = 60
	defaultMaxUploadBytes     int64 = 500 * 1024 * 1024
	DefaultAdminPassword            = "123456789"
	defaultTimezone                 = "Asia/Shanghai"
)

type Config struct {
	ListenAddr         string
	DBPath             string
	UploadDir          string
	LogLevel           string
	CleanupInterval    time.Duration
	MaxUploadBytes     int64
	ResetAdminPassword string
	Timezone           string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:      envString("APP_LISTEN_ADDR", defaultListenAddr),
		DBPath:          envString("APP_DB_PATH", defaultDBPath),
		UploadDir:       envString("APP_UPLOAD_DIR", defaultUploadDir),
		LogLevel:        envString("APP_LOG_LEVEL", defaultLogLevel),
		CleanupInterval: time.Duration(envInt("APP_CLEANUP_INTERVAL_SEC", defaultCleanupIntervalSec, 5)) * time.Second,
		MaxUploadBytes:  envInt64("APP_MAX_UPLOAD_BYTES", defaultMaxUploadBytes, 1024),
		Timezone:        envString("APP_TIMEZONE", defaultTimezone),
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return Config{}, fmt.Errorf("invalid timezone %q: %w", cfg.Timezone, err)
	}
	TimeLocation = loc

	if cfg.ListenAddr == "" {
		return Config{}, fmt.Errorf("APP_LISTEN_ADDR cannot be empty")
	}
	if cfg.DBPath == "" {
		return Config{}, fmt.Errorf("APP_DB_PATH cannot be empty")
	}
	if cfg.UploadDir == "" {
		return Config{}, fmt.Errorf("APP_UPLOAD_DIR cannot be empty")
	}

	cfg.ResetAdminPassword = os.Getenv("APP_RESET_ADMIN_PASSWORD")
	return cfg, nil
}

func envString(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int, min int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < min {
		return fallback
	}
	return value
}

func envInt64(key string, fallback int64, min int64) int64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < min {
		return fallback
	}
	return value
}
