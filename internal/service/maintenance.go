package service

import (
	"strings"
	"time"

	"cloud-clipboard/internal/store"
)

type MaintenanceService struct {
	settings       *store.SettingsStore
	rooms          *store.RoomStore
	messages       *store.MessageStore
	files          *FileService
	roomOps        *RoomService
	statistics     *store.StatisticsStore
	uploadSessions *store.UploadSessionStore
}

func NewMaintenanceService(settings *store.SettingsStore, rooms *store.RoomStore, messages *store.MessageStore, files *FileService, roomOps *RoomService, statistics *store.StatisticsStore, uploadSessions *store.UploadSessionStore) *MaintenanceService {
	return &MaintenanceService{
		settings:       settings,
		rooms:          rooms,
		messages:       messages,
		files:          files,
		roomOps:        roomOps,
		statistics:     statistics,
		uploadSessions: uploadSessions,
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

	if err := s.cleanupStaleUploads(now); err != nil {
		return err
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

// cleanupStaleUploads removes abandoned upload sessions (and their .partial
// files) so unauthenticated chunk uploads cannot accumulate forever.
func (s *MaintenanceService) cleanupStaleUploads(now time.Time) error {
	if s.uploadSessions == nil {
		return nil
	}
	sessions, err := s.uploadSessions.ListStale(now.Add(-time.Hour).Unix())
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if s.files != nil && strings.TrimSpace(session.StoredPath) != "" {
			if err := s.files.Delete(session.StoredPath); err != nil {
				return err
			}
		}
		if err := s.uploadSessions.Delete(session.ID); err != nil {
			return err
		}
	}
	return nil
}
