package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud-clipboard/internal/store"
)

type SettingsPayload struct {
	RoomCodeLength       int    `json:"roomCodeLength"`
	RoomDefaultTTLSec    int    `json:"roomDefaultTTLSec"`
	RoomExtendSec        int    `json:"roomExtendSec"`
	RoomCanExtendSec     int    `json:"roomCanExtendSec"`
	FileMessageExpireSec int    `json:"fileMessageExpireSec"`
	FileNeverExpire      bool   `json:"fileNeverExpire"`
	MaxUploadBytes       int    `json:"maxUploadBytes"`
	MaxMessageTextLength int    `json:"maxMessageTextLength"`
	EmptyStateTitle      string `json:"emptyStateTitle"`
	EmptyStateBody       string `json:"emptyStateBody"`
}

type PublicSettingsPayload struct {
	EmptyStateTitle      string `json:"emptyStateTitle"`
	EmptyStateBody       string `json:"emptyStateBody"`
	MaxUploadBytes       int    `json:"maxUploadBytes"`
	MaxMessageTextLength int    `json:"maxMessageTextLength"`
	RoomCanExtendSec     int    `json:"roomCanExtendSec"`
	RoomExtendSec        int    `json:"roomExtendSec"`
}

type SettingsService struct {
	store *store.SettingsStore
}

func NewSettingsService(settingsStore *store.SettingsStore) *SettingsService {
	return &SettingsService{store: settingsStore}
}

func (s *SettingsService) GetAll() (SettingsPayload, error) {
	values, err := s.store.GetAll()
	if err != nil {
		return SettingsPayload{}, err
	}
	return settingsFromMap(values)
}

func (s *SettingsService) GetPublic() (PublicSettingsPayload, error) {
	values, err := s.store.GetAll()
	if err != nil {
		return PublicSettingsPayload{}, err
	}
	settings, err := settingsFromMap(values)
	if err != nil {
		return PublicSettingsPayload{}, err
	}
	return PublicSettingsPayload{
		EmptyStateTitle:      settings.EmptyStateTitle,
		EmptyStateBody:       settings.EmptyStateBody,
		MaxUploadBytes:       settings.MaxUploadBytes,
		MaxMessageTextLength: settings.MaxMessageTextLength,
		RoomCanExtendSec:     settings.RoomCanExtendSec,
		RoomExtendSec:        settings.RoomExtendSec,
	}, nil
}

func (s *SettingsService) Update(values SettingsPayload, now time.Time) error {
	for _, pair := range []struct {
		key   string
		value int
	}{
		{"room_code_length", values.RoomCodeLength},
		{"room_default_ttl_sec", values.RoomDefaultTTLSec},
		{"room_extend_sec", values.RoomExtendSec},
		{"room_can_extend_sec", values.RoomCanExtendSec},
		{"file_message_expire_sec", values.FileMessageExpireSec},
		{"max_upload_bytes", values.MaxUploadBytes},
		{"max_message_text_length", values.MaxMessageTextLength},
	} {
		if err := validateSetting(pair.key, pair.value); err != nil {
			return err
		}
		if err := s.store.SetInt(pair.key, pair.value, now); err != nil {
			return err
		}
	}

	for _, pair := range []struct {
		key   string
		value string
	}{
		{"empty_state_title", values.EmptyStateTitle},
		{"empty_state_body", values.EmptyStateBody},
	} {
		if err := validateTextSetting(pair.key, pair.value); err != nil {
			return err
		}
		if err := s.store.Set(pair.key, strings.TrimSpace(pair.value), now); err != nil {
			return err
		}
	}

	// 布尔值设置
	fileNeverExpire := "false"
	if values.FileNeverExpire {
		fileNeverExpire = "true"
	}
	if err := s.store.Set("file_never_expire", fileNeverExpire, now); err != nil {
		return err
	}
	return nil
}

func settingsFromMap(values map[string]string) (SettingsPayload, error) {
	parseInt := func(key string) (int, error) {
		raw, ok := values[key]
		if !ok {
			return 0, fmt.Errorf("missing setting: %s", key)
		}
		return strconv.Atoi(raw)
	}

	roomCodeLength, err := parseInt("room_code_length")
	if err != nil {
		return SettingsPayload{}, err
	}
	roomDefaultTTLSec, err := parseInt("room_default_ttl_sec")
	if err != nil {
		return SettingsPayload{}, err
	}
	roomExtendSec, err := parseInt("room_extend_sec")
	if err != nil {
		return SettingsPayload{}, err
	}
	roomCanExtendSec, err := parseInt("room_can_extend_sec")
	if err != nil {
		return SettingsPayload{}, err
	}
	fileMessageExpireSec, err := parseInt("file_message_expire_sec")
	if err != nil {
		return SettingsPayload{}, err
	}
	fileNeverExpire := strings.ToLower(strings.TrimSpace(values["file_never_expire"])) == "true"
	maxUploadBytes, err := parseInt("max_upload_bytes")
	if err != nil {
		return SettingsPayload{}, err
	}
	maxMessageTextLength, err := parseInt("max_message_text_length")
	if err != nil {
		return SettingsPayload{}, err
	}

	emptyStateTitle := values["empty_state_title"]
	if emptyStateTitle == "" {
		emptyStateTitle = "选择一个房间开始"
	}
	emptyStateBody := values["empty_state_body"]
	if emptyStateBody == "" {
		emptyStateBody = "•尝试创建新房间或通过房间号加入已有房间，即可开始同步内容 •您的数据将在服务器上有限期存储，除非您手动设置为永久房间 •本服务仅作为数据传输工具，请勿依赖本服务作为唯一存储介质 •房间隔离码不足以构成加密保护，请勿传输未加密重要敏感信息 •您需对传输内容的合法性自行承担全部责任，禁止传播非法内容"
	}

	return SettingsPayload{
		RoomCodeLength:       roomCodeLength,
		RoomDefaultTTLSec:    roomDefaultTTLSec,
		RoomExtendSec:        roomExtendSec,
		RoomCanExtendSec:     roomCanExtendSec,
		FileMessageExpireSec: fileMessageExpireSec,
		FileNeverExpire:      fileNeverExpire,
		MaxUploadBytes:       maxUploadBytes,
		MaxMessageTextLength: maxMessageTextLength,
		EmptyStateTitle:      emptyStateTitle,
		EmptyStateBody:       emptyStateBody,
	}, nil
}

func validateSetting(key string, value int) error {
	switch key {
	case "room_default_ttl_sec", "file_message_expire_sec", "room_extend_sec", "room_can_extend_sec":
		if value < 60 {
			return fmt.Errorf("%s must be at least 60", key)
		}
	case "room_code_length":
		if value < 4 || value > 16 {
			return fmt.Errorf("%s must be between 4 and 16", key)
		}
	case "max_upload_bytes":
		if value < 1024*1024 {
			return fmt.Errorf("%s must be at least 1048576", key)
		}
	case "max_message_text_length":
		if value < 1 {
			return fmt.Errorf("%s must be at least 1", key)
		}
	default:
		return fmt.Errorf("unsupported setting: %s", key)
	}
	return nil
}

func validateTextSetting(key string, value string) error {
	trimmed := strings.TrimSpace(value)
	switch key {
	case "empty_state_title":
		if trimmed == "" {
			return fmt.Errorf("%s cannot be empty", key)
		}
		if len([]rune(trimmed)) > 60 {
			return fmt.Errorf("%s must be 60 characters or fewer", key)
		}
	case "empty_state_body":
		if trimmed == "" {
			return fmt.Errorf("%s cannot be empty", key)
		}
		if len([]rune(trimmed)) > 240 {
			return fmt.Errorf("%s must be 240 characters or fewer", key)
		}
	default:
		return fmt.Errorf("unsupported setting: %s", key)
	}
	return nil
}