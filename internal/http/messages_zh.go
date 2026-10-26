package httpapi

import (
	"fmt"
	"strings"
)

// apiErrorMessageTranslations maps internal error strings (which are also part
// of the HTTP contract) to user-facing Chinese messages. Keeping this in one
// place avoids English leaking into toasts when a handler returns err.Error().
var apiErrorMessageTranslations = map[string]string{
	// auth / admin
	"authentication required":                    "请先登录",
	"invalid password":                           "密码错误",
	"password fields are required":               "请填写完整的密码",
	"password confirmation does not match":       "两次输入的新密码不一致",
	"current password is incorrect":              "当前密码不正确",
	"new password must be at least 8 characters": "新密码至少 8 位",
	"invalid password hash format":               "管理员密码数据异常，请重置管理员密码",
	"invalid password hash iterations":           "管理员密码数据异常，请重置管理员密码",

	// request / routing
	"invalid request body":   "请求内容格式不正确",
	"invalid JSON body":      "请求内容格式不正确",
	"method not allowed":     "当前操作不被允许",
	"missing file":           "未选择文件",
	"invalid file name":      "文件名不合法",
	"invalid message id":     "消息标识不合法",
	"invalid since_event_id": "同步参数不合法",

	// upload
	"invalid multipart body":                 "上传内容格式不正确",
	"invalid upload session payload":         "上传参数不合法",
	"invalid upload path":                    "上传地址不合法",
	"upload session not found":               "上传会话不存在或已过期",
	"chunk exceeds configured chunk size":    "上传分片过大",
	"file exceeds configured size limit":     "文件超过大小限制",
	"uploaded size does not match file size": "上传未完成，文件大小不一致",
	"upload exceeds declared file size":      "上传内容超过声明大小",

	// room
	"room name must be 40 characters or fewer": "房间名称不能超过 40 个字符",
	"message exceeds configured length limit":  "消息内容超过长度限制",
	"permanent room does not need extension":   "长期房间无需续期",
	"room has no expiration":                   "当前房间没有有效期",
	"unable to allocate unique room code":      "房间号分配失败，请稍后重试",
	"roomCodes is required":                    "请选择要删除的房间",

	// files / storage
	"file service is not configured":            "文件服务不可用",
	"empty file path":                           "文件路径为空",
	"file path is outside the upload directory": "文件路径不合法",
	"path is a directory":                       "文件不存在",

	// settings validation
	"room_default_ttl_sec must be at least 60":         "房间有效期不能小于 60 秒",
	"room_extend_sec must be at least 60":              "房间续期时间不能小于 60 秒",
	"room_can_extend_sec must be at least 60":          "房间可续期阈值不能小于 60 秒",
	"file_message_expire_sec must be at least 60":      "文件消息过期时间不能小于 60 秒",
	"room_code_length must be between 4 and 16":        "房间号长度必须在 4 到 16 之间",
	"max_upload_bytes must be at least 1048576":        "上传大小限制至少为 1MB",
	"max_message_text_length must be at least 1":       "消息长度限制至少为 1",
	"empty_state_title cannot be empty":                "首页空态标题不能为空",
	"empty_state_title must be 60 characters or fewer": "首页空态标题不能超过 60 个字符",
	"empty_state_body cannot be empty":                 "首页空态内容不能为空",
	"empty_state_body must be 240 characters or fewer": "首页空态内容不能超过 240 个字符",
}

// localizeAPIErrorMessage converts known internal English errors to Chinese and
// hides unknown internal errors behind a generic message. Messages that already
// contain Chinese are returned unchanged.
func localizeAPIErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return "操作失败，请稍后重试"
	}
	if translated, ok := apiErrorMessageTranslations[message]; ok {
		return translated
	}
	if strings.HasPrefix(message, "room still has more than ") {
		fields := strings.Fields(message)
		if len(fields) >= 6 {
			return fmt.Sprintf("房间剩余时间超过 %s 小时，暂时无需续期", fields[5])
		}
		return "房间剩余时间较长，暂时无需续期"
	}
	if strings.HasPrefix(message, "missing setting: ") {
		return "系统设置缺失，请重启服务或检查数据库"
	}
	if strings.HasPrefix(message, "unsupported setting: ") {
		return "不支持的设置项"
	}
	if isASCIIOnly(message) {
		return "操作失败，请稍后重试"
	}
	return message
}

func isASCIIOnly(value string) bool {
	for _, r := range value {
		if r > 127 {
			return false
		}
	}
	return true
}
