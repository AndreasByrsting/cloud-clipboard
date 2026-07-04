package app

import (
	"database/sql"
	"net/http"

	"cloud-clipboard/internal/config"
	"cloud-clipboard/internal/realtime"
	"cloud-clipboard/internal/service"
	"cloud-clipboard/internal/store"
)

type App struct {
	Config      config.Config
	DB          *sql.DB
	Hub         *realtime.Hub
	Admin       *service.AdminService
	Room        *service.RoomService
	Settings    *service.SettingsService
	Files       *service.FileService
	Maintenance *service.MaintenanceService
	Statistics  *store.StatisticsStore
	Handler     http.Handler
}
