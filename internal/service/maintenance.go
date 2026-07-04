package service

import (
	"time"

	"cloud-clipboard/internal/store"
)

type MaintenanceService struct {
	settings   *store.SettingsStore
	rooms      *store.RoomStore
	messages   *store.MessageStore
	files      *FileService
	roomOps    *RoomService
	statistics *store.StatisticsStore
}

func NewMaintenanceService(settings *store.SettingsStore, rooms *store.RoomStore, messages *store.MessageStore, files *FileService, roomOps *RoomService, statistics *store.StatisticsStore) *MaintenanceService {
	return &MaintenanceService{
		settings:   settings,
		rooms:      rooms,
		messages:   messages,
		files:      files,
		roomOps:    roomOps,
		statistics: statistics,
	}
}

func (s *MaintenanceService) Run(now time.Time) error {
	expiredFiles, err := s.messages.ListExpiredFiles(now)
	if err != nil {
		return err
	}
	for _, message := range expiredFiles {
		if s.files != nil {
			if err := s.files.Delete(message.FilePath); err != nil {
				return err
			}
		}
		// 保留消息记录，仅清除文件路径，前端可继续显示文件名和已过期状态
		if err := s.messages.ClearFilePath(message.ID); err != nil {
			return err
		}
	}
	if s.roomOps != nil {
		if err := s.roomOps.DeleteExpiredRooms(now); err != nil {
			return err
		}
	}
	// 每次维护后刷新统计表
	if s.statistics != nil {
		return s.statistics.Refresh(now)
	}
	return nil
}