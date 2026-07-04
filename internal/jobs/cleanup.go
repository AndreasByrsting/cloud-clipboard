package jobs

import (
	"context"
	"time"

	"cloud-clipboard/internal/logger"
	"cloud-clipboard/internal/service"
)

func StartCleanup(ctx context.Context, interval time.Duration, maintenance *service.MaintenanceService) {
	// 启动时立即执行一次清理
	if err := maintenance.Run(time.Now()); err != nil {
		logger.Warn("startup cleanup failed: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := maintenance.Run(now); err != nil {
				logger.Error("cleanup task failed: %v", err)
			}
		}
	}
}
