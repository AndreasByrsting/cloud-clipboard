package store

import (
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"time"
)

var ErrNotFound = errors.New("not found")

type SettingsStore struct {
	db *sql.DB
}

func NewSettingsStore(db *sql.DB) *SettingsStore {
	return &SettingsStore{db: db}
}

func (s *SettingsStore) EnsureDefaults(now time.Time) error {
	defaults := map[string]string{
		"room_code_length":        "6",
		"room_default_ttl_sec":    strconv.Itoa(24 * 60 * 60),
		"room_extend_sec":         strconv.Itoa(24 * 60 * 60),
		"room_can_extend_sec":     strconv.Itoa(25 * 60 * 60),
		"file_message_expire_sec": strconv.Itoa(24 * 60 * 60),
		"file_never_expire":        "false",
		"max_upload_bytes":        strconv.Itoa(500 * 1024 * 1024),
		"max_message_text_length": strconv.Itoa(40960),
		"empty_state_title":       "选择一个房间开始",
		"empty_state_body":        "•尝试创建新房间或通过房间号加入已有房间，即可开始同步内容\n•您的数据将在服务器上有限期存储，除非您手动设置为永久房间\n•本服务仅作为数据传输工具，请勿依赖本服务作为唯一存储介质\n•房间隔离码不足以构成加密保护，请勿传输未加密重要敏感信息\n•您需对传输内容的合法性自行承担全部责任，禁止传播非法内容",
	}

	for key, value := range defaults {
		if _, err := s.db.Exec(`
			INSERT INTO system_settings(key, value, updated_at)
			VALUES(?, ?, ?)
			ON CONFLICT(key) DO NOTHING
		`, key, value, now.Unix()); err != nil {
			return err
		}
	}

	return nil
}

func (s *SettingsStore) Get(key string) (string, error) {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM system_settings WHERE key = ?`, key).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return value, nil
}

func (s *SettingsStore) GetInt(key string) (int, error) {
	v, err := s.Get(key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func (s *SettingsStore) MustGet(key string) string {
	v, _ := s.Get(key)
	return v
}

func (s *SettingsStore) GetAll() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM system_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]string{}
	for rows.Next() {
		var key string
		var raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		result[key] = raw
	}
	return result, rows.Err()
}

func (s *SettingsStore) GetAllInts() (map[string]int, error) {
	values, err := s.GetAll()
	if err != nil {
		return nil, err
	}
	result := map[string]int{}
	for key, raw := range values {
		value, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		result[key] = value
	}
	return result, nil
}

func (s *SettingsStore) Set(key string, value string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE system_settings SET value = ?, updated_at = ? WHERE key = ?`, value, now.Unix(), key)
	return err
}

func (s *SettingsStore) SetInt(key string, value int, now time.Time) error {
	return s.Set(key, strconv.Itoa(value), now)
}

func SortedSettingKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
