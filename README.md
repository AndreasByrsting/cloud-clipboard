# Cloud Clipboard

跨设备文本与文件同步的云剪贴板。创建房间、分享房间号，即可在任意支持浏览器的设备间实时同步消息和文件。

## 特性
- 纯 Go 单二进制文件，静态资源内嵌，零外部依赖运行时
- SQLite 存储（纯 Go 实现，无需 CGO）
- WebSocket 即时连接 + HTTP 轮询连接自动降级，指数退避重连
- 消息文本链接自动识别，消息卡片展开/收起
- 文件分片上传，拖拽上传
- 房间生命周期管理（自动过期、续期、长期房间）
- 管理后台：房间管理、系统设置
- 深色/浅色主题，强调色自定义
- Docker 支持，多阶段构建，镜像已上传至 Docker Hub


## 项目预览
<img width="1361" height="1032" alt="image" src="https://github.com/user-attachments/assets/f4cc7d49-3db8-48e6-80f0-c673179952ff" />
<img width="1361" height="1032" alt="image" src="https://github.com/user-attachments/assets/fc88f697-13f2-4a03-8586-48c9a030fe6c" />
<img width="1361" height="1032" alt="image" src="https://github.com/user-attachments/assets/400b576b-e2ab-4895-acb3-20c88a088d23" />
<img width="432" height="880" alt="image" src="https://github.com/user-attachments/assets/322592a9-d9ae-43ba-a8d3-ee576f458970" />

## 快速开始
### 本地运行
```bash
# 需要 Go 1.26+
# 克隆项目
git clone <repo-url>
cd cloud-clipboard
# 编译运行
go build -o cloud-clipboard .
./cloud-clipboard
```
服务默认监听 `:8080`，浏览器访问：http://localhost:8080

### 方式1：拉取 Docker Hub 镜像（推荐）
```bash
# 拉取镜像
docker pull andreasgyh/cloud-clipboard:latest
# 启动容器
docker run -d 
  -p 8080:8080 
  -v cloud-clipboard-data:/data 
  --name cloud-clipboard 
  andreasgyh/cloud-clipboard:latest
```

### 方式2：本地自行构建镜像
```bash
# 构建镜像
docker build -t andreasgyh/cloud-clipboard:latest .
# 运行自建镜像
docker run -d 
  -p 8080:8080 
  -v cloud-clipboard-data:/data 
  --name cloud-clipboard 
  andreasgyh/cloud-clipboard:latest
```

### Docker Compose
```yaml
version: "3.8"
services:
  cloud-clipboard:
    image: andreasgyh/cloud-clipboard:latest
    container_name: cloud-clipboard
    ports:
      - "8080:8080"
    volumes:
      - ./data:/data
    restart: unless-stopped
```

## 环境变量
| 变量 | 默认值 | 说明 |
| ---- | ------ | ---- |
| APP_LISTEN_ADDR | :8080 | 服务监听地址 |
| APP_DB_PATH | ./data/clipboard.db | SQLite 数据库文件路径 |
| APP_UPLOAD_DIR | ./data/uploads | 文件上传存储目录 |
| APP_CLEANUP_INTERVAL_SEC | 60 | 过期数据定时清理间隔（单位：秒） |
| APP_RESET_ADMIN_PASSWORD | 空 | 可在后台重置管理员密码 |

## 打包编译
```bash
# 当前系统直接编译
go build -o cloud-clipboard .
# Linux AMD64 静态无CGO编译
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o cloud-clipboard .
# 本地构建Docker镜像
docker build -t andreasgyh/cloud-clipboard:latest .
```
## 架构
```
cloud-clipboard/
├── main.go                        # 入口
├── go.mod
├── Dockerfile                     # 多阶段 Docker 构建
├── sql/
│   └── init.sql                   # 嵌入式数据库迁移
├── static/                        # 前端（go:embed 嵌入二进制）
│   ├── index.html
│   ├── css/
│   │   ├── tokens.css             # 设计令牌（颜色、间距、圆角）
│   │   ├── base.css               # 基础重置与排版
│   │   ├── layout.css             # 布局（工作区、侧边栏、顶栏）
│   │   ├── components.css         # 组件样式（卡片、按钮、表单）
│   │   └── animations.css         # 动效（翻转、淡入、弹窗）
│   ├── js/
│   │   ├── state.js               # 全局状态管理
│   │   ├── api.js                 # HTTP API 客户端
│   │   ├── app.js                 # 主渲染与事件处理
│   │   └── realtime.js            # WebSocket + 轮询连接管理
│   └── icon/                      # SVG 图标
└── internal/
    ├── app/                       # App 聚合结构体
    │   └── app.go
    ├── config/                    # 环境变量配置
    │   └── config.go
    ├── db/                        # 数据库
    │   ├── sqlite.go              # 连接管理（WAL 模式、完整性检查）
    │   └── migrate.go             # 嵌入式迁移执行
    ├── http/                      # HTTP 接口层
    │   ├── router.go              # 路由注册
    │   ├── handlers_rooms.go      # 房间 API
    │   ├── handlers_files.go      # 文件 API
    │   ├── handlers_sync.go       # 同步 API（轮询 + WebSocket）
    │   ├── handlers_settings.go   # 设置 API
    │   ├── handlers_admin.go      # 管理员认证 API
    │   ├── handlers_admin_rooms.go# 管理员房间管理 API
    │   └── session.go             # 会话管理
    ├── jobs/                      # 后台任务
    │   └── cleanup.go             # 过期房间清理
    ├── realtime/                  # 实时通信
    │   └── hub.go                 # WebSocket 连接管理
    ├── service/                   # 业务逻辑层
    │   ├── room.go                # 房间服务
    │   ├── file.go                # 文件服务
    │   ├── settings.go            # 设置服务
    │   ├── admin.go               # 管理员服务
    │   └── maintenance.go         # 维护服务（过期清理）
    └── store/                     # 数据访问层
        ├── room_store.go          # 房间持久化
        ├── message_store.go       # 消息持久化
        ├── settings_store.go      # 系统设置持久化
        ├── admin_store.go         # 管理员凭证持久化
        └── upload_session_store.go# 上传会话持久化
```
