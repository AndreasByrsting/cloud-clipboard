package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	// 如果主数据库文件不存在，清理残留的 WAL/SHM 文件，避免 SQLite
	// 尝试将旧 WAL 日志应用到新创建的空数据库上导致 "database disk image is malformed"。
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		for _, ext := range []string{"-wal", "-shm"} {
			_ = os.Remove(path + ext)
		}
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
	}
	for _, query := range pragmas {
		if _, err := database.Exec(query); err != nil {
			database.Close()
			return nil, err
		}
	}

	if err := database.Ping(); err != nil {
		database.Close()
		return nil, err
	}

	// 启动时运行完整性检查，尽早发现数据库损坏
	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		database.Close()
		return nil, fmt.Errorf("integrity check failed: %w", err)
	}
	if integrity != "ok" {
		// 尝试 REINDEX 修复索引损坏（如 "wrong # of entries in index"）
		// 索引损坏可以从表数据重建，是最常见且可自动修复的损坏类型
		if _, reindexErr := database.Exec("REINDEX"); reindexErr != nil {
			database.Close()
			return nil, fmt.Errorf("database integrity check failed (%s) and REINDEX failed: %w", integrity, reindexErr)
		}
		// REINDEX 后再次检查
		var newIntegrity string
		if err := database.QueryRow("PRAGMA integrity_check").Scan(&newIntegrity); err != nil {
			database.Close()
			return nil, fmt.Errorf("integrity check after REINDEX failed: %w", err)
		}
		if newIntegrity != "ok" {
			database.Close()
			return nil, fmt.Errorf("database integrity check still failed after REINDEX: %s\nDelete %s and restart to recreate the database.", newIntegrity, path)
		}
		fmt.Fprintf(os.Stderr, "cloud-clipboard: database index repaired via REINDEX (was: %s)\n", integrity)
	}

	return database, nil
}
