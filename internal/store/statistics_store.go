package store

import (
	"database/sql"
	"fmt"
	"time"
)

type HourlyStat struct {
	Hour         string `json:"hour"`
	RoomCount    int    `json:"roomCount"`
	MessageCount int    `json:"messageCount"`
}

type StatisticsStore struct {
	db *sql.DB
}

func NewStatisticsStore(db *sql.DB) *StatisticsStore {
	return &StatisticsStore{db: db}
}

// Refresh 重新统计最近 24 小时的每小时数据并写入 hourly_stats 表
func (s *StatisticsStore) Refresh(now time.Time) error {
	// 对齐到当前小时，确保每次刷新覆盖相同的 24 个时段
	hourStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())

	for i := 23; i >= 0; i-- {
		t := hourStart.Add(-time.Duration(i) * time.Hour)
		hourKey := t.Format("2006-01-02 15")
		hStart := t.Unix()
		hEnd := t.Add(time.Hour).Unix()

		var roomCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM rooms WHERE created_at >= ? AND created_at < ?`, hStart, hEnd).Scan(&roomCount); err != nil {
			return fmt.Errorf("count rooms for %s: %w", hourKey, err)
		}

		var msgCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE created_at >= ? AND created_at < ?`, hStart, hEnd).Scan(&msgCount); err != nil {
			return fmt.Errorf("count messages for %s: %w", hourKey, err)
		}

		_, err := s.db.Exec(`
			INSERT INTO hourly_stats(hour, room_count, message_count, updated_at)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(hour) DO UPDATE SET room_count = excluded.room_count, message_count = excluded.message_count, updated_at = excluded.updated_at
		`, hourKey, roomCount, msgCount, now.Unix())
		if err != nil {
			return fmt.Errorf("upsert stats for %s: %w", hourKey, err)
		}
	}

	// 清理超过 24 小时的旧数据，防止累积
	cutoff := hourStart.Add(-24 * time.Hour).Format("2006-01-02 15")
	if _, err := s.db.Exec(`DELETE FROM hourly_stats WHERE hour < ?`, cutoff); err != nil {
		return fmt.Errorf("clean old stats: %w", err)
	}
	return nil
}

// GetRecent 获取最近 24 小时的统计数据
func (s *StatisticsStore) GetRecent(now time.Time) ([]HourlyStat, error) {
	cutoff := now.Add(-24 * time.Hour).Format("2006-01-02 15")
	rows, err := s.db.Query(`SELECT hour, room_count, message_count FROM hourly_stats WHERE hour >= ? ORDER BY hour ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []HourlyStat
	for rows.Next() {
		var stat HourlyStat
		if err := rows.Scan(&stat.Hour, &stat.RoomCount, &stat.MessageCount); err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}
	return stats, rows.Err()
}

// 确保 StatisticsStore 实现接口
var _ interface {
	Refresh(now time.Time) error
	GetRecent(now time.Time) ([]HourlyStat, error)
} = (*StatisticsStore)(nil)