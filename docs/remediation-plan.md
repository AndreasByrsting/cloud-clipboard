# 云剪贴板：安全与前端交互整改方案

> 版本：v1.0  
> 日期：2026-10-23  
> 基线：`9ebe5c9`（工作区干净）  
> 状态：待评审 / 待排期  
> 范围：后端授权与数据安全、文件上传下载、管理后台、实时通道、前端布局与滚动、复制粘贴、移动端发送、样式与动效  
> 非范围：端到端加密、整体视觉重设计、更换前端框架、多租户/账号体系

---

## 0. 摘要

当前系统有三个根因，而不是一堆零散 bug：

1. **授权只做到「房间码」为止**：房间码被当作唯一凭证，但消息、文件、房间销毁接口绕过了房间作用域；`GET /api/rooms` 还能直接枚举全站房间。结果是“知道一个 ID 就能操作任意房间的数据”。
2. **文件同源内联返回**：上传的 SVG 可以直接在应用源执行脚本，与管理员会话组合后风险被放大。
3. **前端用 `innerHTML` 全量重渲染，且移动端 composer 被 FAB 门控**：滚动跳动、复制选择失效、输入焦点丢失、动效重放、上传打断输入、手机上要 3–4 步才能发送，都是这两个结构性问题的派生症状。

本方案按“先止血、再补模型、最后做体验”的顺序推进：

| 阶段 | 周期（单人） | 目标 | 主要交付 |
|---|---|---|---|
| Phase 0 止血 | 5–7 人日 | 让最危险的越权、XSS、资源耗尽不可用 | 作用域路由、下载加固、上传限额、管理登录防爆破、前端急救 |
| Phase 1 模型 | 10–13 人日 | 建立可长期维护的授权与配额模型，移动端可用 | 所有者令牌、版本化迁移、配额/清理、WS 加固、滚动重构、底部 composer |
| Phase 2 体验 | 14–18 人日 | 性能、可访问性、样式与动效收敛 | 分页/虚拟列表、样式系统、动效规范、管理后台重构、完整测试 |

**需要先拍板的产品决策**见第 7 节。若只做一件事：先做 Phase 0 的“作用域路由 + 文件强制下载 + 上传服务端限额”。

---

## 1. 范围与目标

### 1.1 范围内

- 后端：路由与授权、文件上传/下载、WebSocket、管理会话、限流与配额、迁移与清理、安全响应头。
- 前端：应用壳与滚动容器、消息增量渲染、移动端 composer、复制/粘贴、样式 token、动效、Service Worker 与缓存、可访问性。
- 工程：测试矩阵、发布与回滚、监控。

### 1.2 范围外（本次不做，但预留）

- 端到端加密（E2E）。这会改变产品模型和链接识别能力，需要单独立项。
- 账号体系 / OAuth / 多租户。
- 引入 React/Vue 整体重写。Phase 2 只建议抽组件，不换框架。
- 大文件断点续传的跨会话恢复（本次只做上限与清理）。

### 1.3 验收指标

| 指标 | 现状 | 目标 |
|---|---|---|
| 房间 A 凭证访问房间 B 资源 | 可以（且无需 code） | 100% 返回 404 |
| 公开房间枚举 | `GET /api/rooms` 返回全量 | 接口不存在或仅管理员 |
| 500MB 文件下载峰值内存 | 约等于文件大小 | O(1) 流式，支持 Range |
| 声明 10GB 的分片上传实际写盘 | 可无限写入 | 超过配置上限立即 413，写盘为 0 |
| 默认管理员密码 | 可直接登录 | 强制改密后才能进入后台 |
| 登录暴力破解 | 无限制 | 5 次失败锁定 15 分钟并审计 |
| 移动端发送步骤 | 进入房间 → FAB → 显示房间卡 → 滚到顶部输入 | 进入房间即可在底部输入，1 步发送 |
| 消息更新后的滚动偏移 | 不定，常跳到顶部 | 0px（除用户主动回顶） |
| 首屏 JS（未压缩） | 约 330KB | ≤ 180KB，chart 按需加载 |
| 小字对比度 | 深色约 3.1:1 / 浅色约 2.9:1 | ≥ 4.5:1 |
| 键盘可完成主流程 | 部分不可（房间号、展开、弹窗） | 可完成发送、复制、退出、管理登录 |

---

## 2. 安全设计

### 2.1 威胁模型与角色

| 威胁 | 现状 | 处置 |
|---|---|---|
| 房间枚举 | `GET /api/rooms` 无鉴权 | 删除接口；只展示本机最近房间 |
| 跨房间 IDOR | 消息/文件按全局 ID 操作 | 全部收敛到 `/api/rooms/{code}/...` 并校验归属 |
| 房间被任意销毁 | 知道 code 即可 DELETE | 所有者令牌（owner token） |
| 存储型 XSS | SVG 以 `image/svg+xml` inline 返回 | 强制下载 + 类型白名单 + CSP sandbox |
| 磁盘/内存耗尽 | 分片上传不校验声明大小；下载全量读内存 | 服务端限额 + 流式下载 + 配额 |
| 管理员爆破 | 无锁定、默认密码、日志明文 | 限速/锁定、强制改密、移除日志密码 |
| 会话劫持 | Cookie 无 Secure、服务端 token 不过期 | Secure/Strict/__Host- + TTL + 轮换 |
| 跨站 WebSocket | `CheckOrigin` 恒 true | Origin 白名单 |
| CSRF | 依赖 SameSite=Lax | SameSite=Strict + Origin/Sec-Fetch-Site 校验 |
| 资源滥用 | 无任何限流/配额 | IP/房间双维度限流 + 配额 |

角色定义：

- **访客（Guest）**：持有房间码，可进入房间、读写消息、上传。
- **所有者（Owner）**：创建房间时获得 owner token，可改名、续期、设长期、销毁房间、导出令牌。
- **管理员（Admin）**：后台会话，可管理所有房间与系统设置。

> 说明：房间码仍然是“共享密钥”。本方案不把房间码升级为账号体系，但要让“拿到 code”不再等于“能操作全站资源”。

### 2.2 能力矩阵

| 操作 | 访客（有 code） | 所有者（有 token） | 管理员 |
|---|---|---|---|
| 查看房间详情/消息/事件 | 允许 | 允许 | 允许 |
| 发送文本/上传文件 | 允许（限流） | 允许 | 允许 |
| 删除/置顶消息 | 允许（默认，限流 + 确认） | 允许 | 允许 |
| 修改房间名 | 拒绝 | 允许 | 允许 |
| 续期 / 设为长期 | 拒绝 | 允许 | 允许 |
| 销毁房间 | 拒绝 | 允许 | 允许 |
| 查看/导出 owner token | 拒绝 | 允许 | 可重置 |
| 系统设置 / 全站房间列表 | 拒绝 | 拒绝 | 允许 |

产品可选项：`room_owner_only_manage`（默认 false）。开启后删除/置顶也仅所有者可操作，适合“只读分享”场景。

### 2.3 API 改造

#### 2.3.1 路由变化

| 旧接口 | 新接口 | 鉴权 |
|---|---|---|
| `GET /api/rooms` | 删除（或仅管理员 `GET /api/admin/rooms`） | — |
| `POST /api/rooms` | 不变，响应增加 `ownerToken` | — |
| `GET /api/rooms/{code}/detail` | 不变 | code |
| `POST /api/rooms/{code}/name` | 不变 | owner |
| `POST /api/rooms/{code}/extend` | 不变 | owner |
| `POST /api/rooms/{code}/permanent` | 不变 | owner |
| `DELETE /api/rooms/{code}` | 不变 | owner |
| `GET /api/rooms/{code}/messages` | 增加 `?before_id=&limit=` 分页 | code |
| `POST /api/rooms/{code}/messages` | 增加 `clientMessageId` | code |
| `DELETE /api/messages/{id}` | `DELETE /api/rooms/{code}/messages/{id}` | code + 归属 |
| `POST /api/messages/{id}/pin` | `POST /api/rooms/{code}/messages/{id}/pin` | code + 归属 |
| `GET /api/messages/{id}/file` | `GET /api/rooms/{code}/messages/{id}/file` | code + 归属 |
| `POST /api/rooms/{code}/uploads` | 不变 | code |
| `PUT /api/messages/uploads/{id}/chunk` | `PUT /api/rooms/{code}/uploads/{id}/chunk` | code + 会话归属 |
| `POST /api/messages/uploads/{id}/complete` | `POST /api/rooms/{code}/uploads/{id}/complete` | code + 会话归属 |
| `GET /api/rooms/{code}/events` | 不变 | code |
| `GET /api/bootstrap/status` | 增加 `serverVersion`、`apiVersion`、`minClientVersion`；移除公开的 `defaultPasswordActive` | — |

旧 `/api/messages/*` 一律返回 404（不返回 410，避免成为探测信号）。前端同版本上线，详见 3.7。

#### 2.3.2 所有者令牌

- 生成：32 字节 `crypto/rand`，base64url 编码；数据库只存 `sha256(token)`。
- 下发：仅 `POST /api/rooms` 响应返回一次，字段 `ownerToken`；同时把房间字段平铺返回，兼容旧前端（旧前端忽略该字段）。
- 传递：`X-Room-Owner: <token>` 请求头；服务端用 `subtle.ConstantTimeCompare` 比较哈希。
- 客户端：`localStorage["cc.owner.<code>"]`；房间设置里提供“复制所有者密钥”，用于迁移到其他设备。提示“持有该密钥即可销毁房间”。
- 丢失：只能由管理员删除/重置；不提供“凭 code 认领”，避免越权。
- 历史房间：迁移时 `owner_token_hash = ''`。这类房间在过期前（最长 24h）允许 code 执行管理操作，之后随过期清理自然淘汰；或由管理员统一处理。

#### 2.3.3 错误约定

保持现有 `{"error": "..."}` 兼容，同时增加机器可读字段：

```json
{ "error": "房间不存在或已关闭", "code": "room_not_found" }
```

| code | HTTP | 场景 |
|---|---|---|
| `room_not_found` | 404 | code 不存在 / 已过期 |
| `message_not_found` | 404 | 消息不存在或不属于该房间 |
| `owner_required` | 403 | 缺少/错误 owner token |
| `payload_too_large` | 413 | JSON/文本/分片超限 |
| `quota_exceeded` | 507 | 房间或全局配额不足 |
| `rate_limited` | 429 | 触发限流，带 `Retry-After` |
| `invalid_upload` | 400 | 会话不存在、偏移/大小不符 |

跨房间访问统一返回 404，不泄露资源是否存在。

### 2.4 数据模型与迁移

#### 2.4.1 先修迁移机制

当前 `Migrate` 通过 `strings.Split(schema, ";")` 逐条执行，并靠忽略 `duplicate column name` 实现“幂等”；没有版本表、没有事务，迁移失败会留下半完成状态。建议替换为：

- 新增 `sql/migrations/001_init.sql`、`002_security.sql`… 按文件名排序执行。
- 表 `schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`。
- 每个文件在单独事务中执行，成功后记录版本；失败回滚并终止启动。
- 迁移前把数据库复制为 `clipboard.db.bak.<timestamp>`（或 `VACUUM INTO`），保留最近 3 份。
- `sql/init.sql` 仅保留为首次安装的基线，不再直接执行 `ALTER TABLE`。

#### 2.4.2 建议变更

```sql
-- 002_security.sql
ALTER TABLE rooms ADD COLUMN owner_token_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE rooms ADD COLUMN used_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE upload_sessions ADD COLUMN expires_at INTEGER;
ALTER TABLE messages ADD COLUMN client_message_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_room_client
  ON messages(room_id, client_message_id)
  WHERE client_message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_upload_sessions_expires
  ON upload_sessions(expires_at);

CREATE TABLE IF NOT EXISTS admin_audit_logs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,
  target     TEXT,
  ip         TEXT,
  created_at INTEGER NOT NULL
);
```

- `used_bytes` 用于房间配额；删除消息/房间时扣减，启动维护任务时按实际文件大小校准一次。
- `client_message_id` 用于发送幂等：客户端生成 UUID，重复提交返回同一条消息。
- `admin_audit_logs` 记录登录失败、改密、删除房间、批量删除、修改设置。
- `last_active_at` 要么在每次读写消息/上传时更新，要么从 API 中移除，避免“活跃”语义误导。

### 2.5 上传与下载

#### 2.5.1 上传

创建会话：

1. 校验 `fileName` 非空、`fileSize > 0`、`fileSize <= settings.max_upload_bytes`。
2. 校验房间配额：`used_bytes + 活跃会话预留 + fileSize <= room_quota_bytes`；校验全局磁盘水位。
3. 只允许白名单/黑名单策略（见 7.4），拒绝可执行与危险类型（至少拒绝 `svg`、`html`、`xhtml`、`js`、`exe`、`sh`、`bat`）。
4. 生成随机存储名（不带原始扩展名或仅保留白名单扩展名），`.partial` 权限 `0600`。
5. 会话写 `expires_at = now + 1h`；响应**不返回** `storedPath`。

追加分片：

- 路径必须带 `{code}`，服务端校验 `session.room_code == code`。
- `uploadedSize + len(chunk) <= declaredSize`；建议客户端带 `offset`，服务端校验连续性，防止并发分片交错。
- 单块上限 5MB；同一会话串行写，必要时加内存锁。
- 累计写盘超过配额立即中止并删除临时文件。

完成：

- 实际大小必须等于声明大小；可选校验客户端 `sha256`。
- 用 `http.DetectContentType` 复核实际类型，与声明不一致时按检测结果处理或拒绝。
- 重命名为最终文件，创建消息（带 `clientMessageId`），扣配额，删除会话。
- 任何一步失败都要清理临时文件。

清理：

- 清理任务删除 `expires_at < now` 的会话及其 `.partial` 文件。
- 启动时扫描 upload 目录，删除没有 DB 记录、且超过 24h 的文件。
- 房间删除时同步删除其会话与文件（现有逻辑保留）。

#### 2.5.2 下载

- 用 `os.Open` + `http.ServeContent` 流式输出，支持 Range、ETag、If-Modified-Since。
- 默认 `Content-Disposition: attachment`；文件名用 RFC 5987：
  - `filename="<ASCII fallback>"`（过滤引号、分号、控制字符）
  - `filename*=UTF-8''<percent-encoded>`
- 仅对白名单栅格图片（png/jpeg/gif/webp/avif）允许 `?inline=1`；**SVG 永不 inline**。
- 所有文件响应加：
  - `X-Content-Type-Options: nosniff`
  - `Content-Security-Policy: sandbox; default-src 'none'; style-src 'unsafe-inline'`
  - `Cache-Control: private, max-age=3600`
- MIME 由服务端检测/白名单决定，不信任扩展名。
- 访问路径必须包含房间码并校验归属，禁止全局消息 ID 直接取文件。

### 2.6 WebSocket

- **Origin**：解析 `Origin`，host 必须等于 `r.Host`（或配置允许的域名）；浏览器外客户端无 Origin 时由配置决定是否允许。
- **鉴权**：路径含房间码；可选 `?token=` 用于未来只读分享。房间码枚举接口删除后，风险显著降低。
- **读限制**：`conn.SetReadLimit(4 << 10)`；只读取并丢弃客户端消息；`SetReadDeadline(90s)` + pong 续期。
- **写模型**：
  - 每个连接一个 `send chan []byte`（容量 32）和唯一写协程，所有写带 `SetWriteDeadline(10s)`。
  - 广播非阻塞入队；队列满则关闭该连接（1013），避免慢客户端阻塞全局。
  - 欢迎消息也走同一写协程，消除“注册后直接写”的并发窗口。
- **限制**：每房间最大连接数（如 200）、每 IP 最大连接数（如 10）。
- **事件负载**：创建/文件事件直接带 `message` 对象，前端不再为每条事件拉全量列表；删除/置顶带 `messageId`。
- **重连**：欢迎消息带 `latestEventId`；客户端先建立 WS，再拉列表，期间事件按 eventId 去重/补拉，避免竞态丢事件。

### 2.7 HTTP 加固与限流

`http.Server`：

```go
ReadHeaderTimeout: 5 * time.Second
IdleTimeout:       120 * time.Second
MaxHeaderBytes:    1 << 20
// WriteTimeout 不设全局值，避免长上传/WS 被误杀；在具体 handler 内控制
```

中间件：

- `http.MaxBytesReader`：JSON 请求 1MB；文本消息按 `max_message_text_length` 的 UTF-8 字节数 + 1KB 余量。
- 安全响应头：
  - `X-Content-Type-Options: nosniff`
  - `X-Frame-Options: DENY`
  - `Referrer-Policy: no-referrer`
  - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
  - `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
  - HTTPS 下加 `Strict-Transport-Security: max-age=31536000; includeSubDomains`
- 注：CSP 的 `style-src 'unsafe-inline'` 是因为当前大量内联 style；Phase 2 清理后可去掉。`script-src 'self'` 要求移除 HTML 内联事件（`onfocus`/`onblur`）与内联脚本，或改用 nonce。现代浏览器的 `connect-src 'self'` 允许同源 `ws/wss`；若实测被拦截，再显式补 `ws://<host> wss://<host>`。

限流（单实例内存版，预留 Redis 接口）：

| 维度 | 限制 |
|---|---|
| `POST /api/rooms` | 5 次/小时/IP |
| 发送消息 | 30 次/分钟/房间，60 次/分钟/IP |
| 创建上传会话 | 10 次/小时/IP |
| 分片 | 20 次/秒/上传会话 |
| 登录 | 5 次/15 分钟/IP，之后指数退避 |
| `GET` 消息/事件 | 120 次/分钟/IP |
| WebSocket | 10 并发/IP |

实现建议：固定窗口 + token bucket 结合，LRU 清理空闲 key；超限返回 429 + `Retry-After`。多实例部署时明确说明需要外置存储。

### 2.8 管理后台

- **默认密码**：
  - 首次启动若无凭据：读 `APP_ADMIN_PASSWORD`；未设置则生成随机 20 位密码，仅打印一次到 stdout，并标记 `requires_password_change=1`。
  - 未改密前，除 `/api/admin/password`、`/api/admin/logout` 外全部 403。
  - 删除启动日志中的明文默认密码；README 增加说明。
- **登录**：
  - IP 维度失败计数与锁定；失败响应统一为“密码错误”，不区分用户是否存在（当前只有单管理员）。
  - 失败日志写审计表。
  - 登录成功轮换 token；`MaxAge=24h`，服务端记录 `created_at/last_seen_at` 并强制 TTL。
- **会话**：
  - 空闲 2h、绝对 24h 过期；后台协程定期清理。
  - 改密要求当前密码；成功后使所有旧会话失效，仅保留当前会话（或全部失效重新登录）。
  - Cookie：`__Host-cc_admin`、`HttpOnly`、`Secure`（TLS 或可信代理时）、`SameSite=Strict`、`Path=/`。
- **KDF**：
  - 新密码使用 `crypto/pbkdf2`（Go 1.26 标准库，SHA-256，≥600,000 次）或 `golang.org/x/crypto/argon2`（argon2id）。
  - 保留旧 `pbkdf-sha256$` 校验与登录时自动升级；新增算法前缀 `pbkdf2-sha256$` / `argon2id$`。
- **CSRF**：管理端变更请求校验 `Origin` 与 `Sec-Fetch-Site`（同源才允许）；SameSite=Strict 作为第二道防线。
- **审计**：登录成功/失败、改密、删除/批量删除房间、修改设置，记录 actor、action、target、IP、时间。

### 2.9 数据可靠性与隐私

- API 不再返回 `FilePath`/`StoredPath`；数据库只存相对路径，读取前做 `filepath.IsLocal` 与 `EvalSymlinks` 包含性校验。
- SQLite 与上传文件默认明文。部署文档明确建议：数据卷加密、定期备份、备份恢复演练。可选提供 `VACUUM INTO` 定时备份任务。
- 长期房间默认需要 owner token；增加每 IP 长期房间上限；管理员可一键关闭“设为长期”能力。
- 过期清理改为 SQL 条件删除，避免每分钟全表扫描；统计刷新降为每小时。
- 删除房间仍为硬删除，但管理端删除前自动备份该房间的消息 JSON 与文件清单到 `data/trash/`，保留 7 天（可选，但强烈建议）。

---

## 3. 前端设计

### 3.1 应用壳与滚动

#### 3.1.1 目标结构

```text
.app-shell (height: 100dvh; display: grid; grid-template-rows: auto 1fr; overflow: hidden)
├── header.topbar
└── .app-body (min-height: 0)
    └── .workspace (display: grid; grid-template-columns: 380px 1fr; min-height: 0)
        ├── aside.room-panel (overflow-y: auto; overscroll-behavior: contain)
        └── main.chat-panel (min-height: 0; display: flex; flex-direction: column)
            ├── .chat-header（移动端：房间名/码/状态/更多）
            ├── .message-stream (flex: 1; min-height: 0; overflow-y: auto;
            │                    overscroll-behavior: contain;
            │                    scrollbar-gutter: stable)
            └── .composer（桌面在聊天区底部；移动端 fixed 底部）
```

关键点：

- **桌面只滚消息区**，侧栏独立滚动；**移动端只滚消息区**，页面本身不滚（`body { overflow: hidden }`）。
- 所有 flex 子项补 `min-height: 0`，否则 `flex: 1` 不生效。
- 移除 `100dvh - 62px` 之类魔法数，用 grid 自适应。
- 管理后台不再使用 3D 翻转，改为同一 `app-body` 内切换视图（淡入/滑入 160ms）。

#### 3.1.2 滚动锚定

```text
capture():
  firstVisible = 第一个与容器顶部相交的 .message-card
  return { id, top: firstVisible.getBoundingClientRect().top }

restore(anchor):
  el = get(anchor.id)
  if el: stream.scrollTop += el.getBoundingClientRect().top - anchor.top
```

- 更新前 capture，更新后 restore。
- 若更新前 `scrollTop <= 40`（用户在最上面），更新后保持 0，让新消息可见。
- 若用户在下方阅读（`scrollTop > 150`），不要自动滚动；显示“N 条新消息”悬浮按钮，点击回顶。
- 使用 `requestAnimationFrame` 合并渲染；避免在同一帧内多次 capture/restore。
- “回到顶部”按钮用 `IntersectionObserver` + 顶部 sentinel 控制，不再用 `window.scrollY > 3 * innerHeight`。
- 监听容器 resize（如键盘弹起）时保持锚点。

#### 3.1.3 滚动条与触摸

- `scrollbar-width: thin; scrollbar-color: var(--border-strong) transparent;`（Firefox）
- `::-webkit-scrollbar` 保留，thumb 提高对比度（`--text-muted`）。
- `scrollbar-gutter: stable` 防止内容横跳。
- `overscroll-behavior: contain` 防滚动链/下拉刷新。
- 移动端 `-webkit-overflow-scrolling: touch`（默认已开，按需）。
- 滚动容器底部 `padding-bottom` 给固定 composer 与 safe-area 留空间。

### 3.2 消息增量渲染

#### 3.2.1 方案选择

| 方案 | 改动量 | 风险 | 结论 |
|---|---|---|---|
| 继续全量 `innerHTML` | 小 | 滚动/焦点/图片问题持续 | 否 |
| 引入 Preact/Vue 重写消息区 | 大 | 与现有事件委托混用，周期长 | Phase 2 再评估 |
| **自研 keyed 复用（推荐）** | 中 | 需要仔细处理排序/删除 | 是 |

#### 3.2.2 实现要点

- 维护 `Map<messageId, HTMLElement>`。
- `syncMessages(nextList)`：
  1. 删除不在 nextList 中的 DOM 节点与 Map 项。
  2. 对已存在的节点只做字段级 patch：置顶徽章、操作按钮、过期状态、展开 class、图片容器。文本/图片节点不重建。
  3. 新消息用 `<template>` 克隆一次，插入 Map。
  4. 按目标顺序用 `insertBefore` 重排；由于是“最新在上”，通常只需在头部插入。
- 事件委托已绑在 `#app`，新节点无需重新绑定。
- `applyMessageEvent` 直接使用 WS 事件里的 `message`；没有 payload 时才 `GET /api/rooms/{code}/messages/{id}`，绝不拉全量列表。
- 删除/置顶只 patch 单卡。
- `updateSidebar` 不再整体重建：拆成 `updateComposeCounter()`、`updateUploadProgress()`、`updateRoomMeta()`。上传进度只改进度条宽度和文本。
- 列表分页：初次 `GET ...?limit=50`；滚动到列表底部（即最旧）时加载 `before_id`；最多保留 500 条在 DOM，超出后从尾部移除（或 Phase 2 上虚拟列表）。置顶消息建议单独返回或固定在首页，分页只针对非置顶消息，避免“置顶 + 倒序”导致 `before_id` 漏项/重复。

#### 3.2.3 焦点与选择

- composer 不参与消息渲染，输入过程中永不被重建。
- 消息卡片 patch 不触碰文本节点，保留用户选择。
- 展开/收起只切换 class，不重写 HTML。
- 删除/置顶按钮点击后保持滚动位置。

### 3.3 移动端发送

#### 3.3.1 交互

- 进入房间后**底部 composer 常驻**，不再由 FAB/房间卡控制。
- 顶栏：房间名 + 房间码 + 连接状态 + “更多”（复制链接、二维码、改名、续期、退出、销毁）。
- 输入框：`textarea` 自增高，最大 6 行；16px 字号避免 iOS 缩放；`enterkeyhint="enter"`。
- 发送：独立按钮；空内容禁用；发送中显示 spinner；成功后保持焦点。
- Enter 规则：
  - 桌面：Enter 发送，Shift+Enter 换行。
  - 移动端：Enter 换行，只通过发送按钮发送。
  - 所有路径先判断 `event.isComposing`，输入法组合期间不发送。
- 键盘适配：监听 `visualViewport` 的 `resize/scroll`，设置 CSS 变量 `--kb-offset`，composer 用 `bottom: var(--kb-offset)`；收起时归零。
- 安全区：`padding-bottom: env(safe-area-inset-bottom)`；FAB 同样避让。
- 发送后：乐观插入 pending 卡片（带 `clientMessageId`），成功替换、失败显示“重试”；不阻塞连续输入。
- 草稿：按房间存 `localStorage["cc.draft.<code>"]`；发送成功清空；切换房间加载。

#### 3.3.2 附件

- 底部 sheet：拍照（`capture="environment"`）、相册、文件。
- 逐文件状态：排队 / 上传中 / 成功 / 失败 / 取消；支持单个重试与移除。
- 上传过程中不重建 composer、不改变滚动位置；进度在附件条内展示。
- 失败不清空整个队列；成功的文件从队列移除，避免重复上传。
- 粘贴图片进入同一队列。

#### 3.3.3 剪贴板与分享

- composer 增加“粘贴并发送”按钮：`navigator.clipboard.readText()`（HTTPS 下）；不可用时提示。
- 注册 PWA `share_target`（Phase 2）：
  ```json
  "share_target": {
    "action": "/share",
    "method": "GET",
    "params": { "title": "title", "text": "text", "url": "url" }
  }
  ```
  服务端 `/share` 返回应用壳，前端读取 query 并预填 composer。这样手机可从其他 App“分享到云剪贴板”。

#### 3.3.4 移除 FAB 门控

- `mobileRoomCardVisible`、`mobileQRVisible` 与 composer 解耦。
- FAB 只保留：主题、强调色、回到顶部；房间操作进入“更多”sheet。
- 删除 `toggle-mobile-room-card` 相关逻辑，减少状态分支。

### 3.4 复制与粘贴

#### 3.4.1 复制

统一 `copyText(text, { success, failure })`：

1. 若 `window.isSecureContext && navigator.clipboard?.writeText`：尝试 `await`。
2. 失败或不可用：在**同一个用户手势调用栈**内执行 `document.execCommand('copy')`（隐藏 textarea）。
3. 仍失败：弹出对话框，显示只读文本并自动全选，提示手动复制。
4. 成功后使用 `aria-live` 区域播报；不要只依赖 2.5s 消失的 toast。

覆盖场景：

- 复制消息文本（文件消息复制下载链接）。
- 复制房间号、房间链接。
- 复制 owner token（仅所有者，二次确认）。
- 图片消息“复制图片地址”。

选择与展开：

- 移除 `.message-body-clickable { user-select: none }`。
- 展开/收起改为卡片底部独立按钮；整卡点击只切换展开，不阻止文本选择（或仅双击卡片切换）。
- 长文本默认 3 行，提供“展开全文”。

#### 3.4.2 粘贴

- 把 `paste` 监听从 `document` 移到 composer：
  - 有文件（截图）→ 加入待上传队列，插入缩略图。
  - 有文本 → 用 `setRangeText` 插入光标处，保留撤销栈。
  - 组合输入（`isComposing`）期间不处理。
- 非输入区域粘贴文本时，显示“粘贴到草稿”快捷提示，而不是静默追加。
- 输入框内原生粘贴保持浏览器默认行为，不阻止。

### 3.5 样式系统

#### 3.5.1 Token 修正

- 删除/改名所有未定义变量引用：`--color-accent` → `--accent`；`--bg-hover` → `--bg-card-hover`；`--color-warning`/`--color-danger` → `--warning`/`--danger`。
- 对比度建议（需用工具复核）：
  - 深色 `--text-muted: #7a7f93`（约 4.7:1 on `#0f1117`）。
  - 浅色 `--text-muted: #6b6e80`（约 4.6:1 on `#f4f5f9`）。
  - 新增 `--text-faint` 仅用于装饰性内容，不允许承载信息。
- 统一字体为系统栈，移除未引入的 Inter/JetBrains Mono：
  - `--font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;`
  - `--font-mono: ui-monospace, SFMono-Regular, "Cascadia Mono", Consolas, monospace;`
- 图标：把 `filter: invert` 改为 CSS mask + `currentColor`，主题与强调色自然生效：
  ```css
  .icon { background-color: currentColor; -webkit-mask: var(--icon) center / contain no-repeat; mask: var(--icon) center / contain no-repeat; }
  ```
  或在 Phase 2 改为内联 SVG sprite。

#### 3.5.2 结构与质量

- 用 `@layer tokens, base, layout, components, admin, utilities;` 明确层叠顺序，消除重复规则与顺序依赖。
- 删除重复定义：`.empty-state-disconnected`、`.app-view-face`、`.sidebar` 媒体查询、`.msg-action-icon` 主题覆盖。
- 内联 `style=` 全部改为工具类或组件类；为 CSP 去 `unsafe-inline` 做准备。
- `transition: all` 改为具体属性（`transform, opacity, border-color, box-shadow`）。
- 触控目标：移动端按钮、消息操作、关闭按钮 ≥ 44×44px；`.btn-xs` 仅用于桌面密集区域。
- 主题初始化：在 `<head>` 最前插入极小内联脚本设置 `data-theme`（读取 localStorage，回退 `prefers-color-scheme`），消除闪白/闪黑。
- 生命周期 class 统一：JS 与 CSS 都用 `lifecycle-text`；删除不存在的 `lifecycle-value`/`room-lifecycle`。
- 管理页删除 `filter: brightness(1.3)` 这类会改变文字颜色的 hover。

#### 3.5.3 管理端

- 设置表单改为受控：所有输入在 `input` 时写回 `state.adminSettings`，离开时提示未保存。
- 数值输入加单位、范围提示与前端校验（与后端一致）。
- 保存后重新拉取 `/api/admin/settings`，不要用本地对象覆盖。
- 图表按需加载；切换 tab 时销毁实例。
- 房间列表增加搜索（房间码/名称）与状态筛选（可选）。

### 3.6 动效

- 新增 token：
  ```css
  --motion-fast: 120ms;
  --motion-base: 180ms;
  --motion-slow: 240ms;
  --ease-standard: cubic-bezier(0.2, 0, 0, 1);
  ```
- 只对 `transform`/`opacity` 做动画；不使用 `transition: all`。
- 新消息：仅新插入卡片 180ms 淡入 + 上移 8px；已有卡片不重放。
- 管理视图：160ms 交叉淡入，移除 3D 翻转与 `perspective`。
- 按钮按压：`transform: scale(0.98)` 120ms。
- 无限动画只保留连接状态点；欢迎 logo 脉冲、倒计时闪烁改为静态样式 + 每秒文本更新。
- `@media (prefers-reduced-motion: reduce)`：关闭所有非必要动画与平滑滚动。
- Toast：
  - 队列化，单条独立计时；错误 5–6s，成功 3s；悬停暂停；可手动关闭。
  - 入场 200ms，退出 150ms；`aria-live="polite"`。
- 抽屉/弹窗：200ms 淡入 + 轻位移；关闭时先播放退出再移除。
- 背景模糊（`backdrop-filter`）限制在顶栏/抽屉，移动端可关闭。

### 3.7 缓存、版本与资源

这是本次改造最容易被忽略、但会直接导致“上线后用户还是旧页面”的部分。

#### 3.7.1 版本握手

- 构建时已有 `version.Version`/`Commit`，Docker 已注入。新增：
  - `GET /api/bootstrap/status` 返回 `{serverVersion, commit, apiVersion, minClientVersion}`。
  - `index.html` 由 Go handler 动态替换 `{{BUILD}}`，或在 `<head>` 注入 `window.__BUILD__`。
- 前端启动时比较 `window.__BUILD__` 与 `serverVersion`；不一致时：
  1. 注销 Service Worker（若存在）。
  2. 删除 `cloud-clipboard-*` 缓存。
  3. `sessionStorage` 防循环标记后 `location.reload()`。
- 静态资源 URL 加 `?v=<build>`：CSS/JS 全部带版本，避免 SW/HTTP 缓存命中旧文件。

#### 3.7.2 Service Worker

Phase 0 建议**先移除注册**，并在 `index.html` 里加一次性清理脚本（旧 SW 的导航请求是 network-first，因此新 HTML 一定能加载）：

```js
// 伪代码
if (!sessionStorage.getItem('cc-sw-cleaned')) {
  navigator.serviceWorker?.getRegistrations().then(rs => rs.forEach(r => r.unregister()));
  caches.keys().then(keys => keys.filter(k => k.startsWith('cloud-clipboard')).forEach(k => caches.delete(k)));
  sessionStorage.setItem('cc-sw-cleaned', '1');
  location.reload();
}
```

Phase 2 如需离线能力，再引入：
- `CACHE_NAME = "cc-<build>"`；`install` 用 `new Request(url, { cache: 'reload' })` 强制取新资源。
- HTML network-first；带 hash/`?v=` 的资源 cache-first + immutable。
- `activate` 清理旧缓存 + `clients.claim()`；`controllerchange` 时提示刷新。
- 版本号变化时自动更新，避免永久旧缓存。

#### 3.7.3 资源

- `chart.umd.min.js` 仅在管理页按需加载（动态 `<script>`）。
- 删除未引用的 `static/js/vue.global.prod.js`、`static/js/axios.min.js`（当前均为 0 字节）。
- 脚本加 `defer`；CSS 保留在 head。
- 服务端对文本资源启用 gzip/br（`Accept-Encoding` 判断，设置 `Vary: Accept-Encoding`）。
- 静态资源缓存：带 `?v=` 的 `public, max-age=31536000, immutable`；`index.html`、`sw.js` 用 `no-cache`。
- 自定义静态文件 handler 关闭目录列表，设置 `Content-Type` 与安全头。

### 3.8 可访问性

- 所有可交互元素必须是 `<button>`/`<a>`；`code`/`div` 点击补 `role`、`tabindex`、`keydown`（Enter/Space）。
- `:focus-visible`：2px 强调色 outline + 2px offset；不要全局 `outline: none`。
- 弹窗/抽屉：`role="dialog" aria-modal="true"`、焦点陷阱、Esc 关闭、关闭后焦点归还触发元素；遮罩点击关闭。
- Toast 容器 `aria-live="polite"`；连接状态变化也播报。
- 表单：`label` 关联、`autocomplete="current-password"`；错误信息用 `aria-describedby`。
- 图片预览有 `alt`；纯装饰图标 `aria-hidden="true"`。
- 语义结构：`header/nav/main/aside` + 合理的标题层级。
- 移动端触控目标 ≥ 44px；键盘可完成发送、复制、退出、管理登录。
- 对比度 ≥ 4.5:1；`prefers-reduced-motion` 生效。

---

## 4. 实施计划

> 人日为单开发估算；前后端可并行时按依赖拆开。所有涉及行号以基线 `9ebe5c9` 为准。

### Phase 0：止血（5–7 人日）

| ID | 任务 | 主要文件 | 验收 |
|---|---|---|---|
| P0-1 | 删除公开 `GET /api/rooms`；保留管理员列表 | `internal/http/router.go`、`internal/http/handlers_rooms.go` | 未鉴权请求返回 404/405；前端无调用 |
| P0-2 | 消息/文件/上传路由收敛到房间作用域，校验归属 | `router.go`、`handlers_rooms.go`、`handlers_files.go`、`internal/service/room.go`、`static/js/api.js`、`static/js/app.js` | 跨房间/全局 ID 全部 404；本房间功能正常 |
| P0-3 | 下载加固：`ServeContent`、强制 attachment、SVG 禁止 inline、nosniff、RFC5987 | `handlers_files.go`、`internal/service/file.go` | 500MB 下载内存稳定；`curl -I` 头正确；SVG 直接访问不执行脚本 |
| P0-4 | 上传服务端限额：声明大小、累计大小、会话过期、响应去除 `storedPath` | `handlers_rooms.go`、`internal/store/message_store.go`、`internal/service/file.go` | 声明 10GB 返回 413；实际写盘为 0；API 不含绝对路径 |
| P0-5 | `MaxBytesReader`、server timeouts、基础安全头 | `internal/http/json.go`、`main.go`、`router.go` | 超大 JSON 413；`curl -I` 含安全头 |
| P0-6 | 管理登录限速/锁定、会话 TTL、Cookie 加固、移除默认密码日志、改密需当前密码 | `internal/http/helpers.go`、`session.go`、`internal/service/admin.go`、`main.go` | 5 次失败锁定；改密后旧会话失效；未改默认密码时后台 403 |
| P0-7 | 前端急救：IME `isComposing`、移动端 composer 默认可见、生命周期 class、未定义变量、移除全局 Enter 上传、SW 清理 + 版本握手 | `static/js/app.js`、`static/js/state.js`、`static/css/*.css`、`static/index.html`、`static/sw.js` | 中文输入法回车不误发；手机进入房间即可输入；部署后不再长期旧 JS |

**Phase 0 完成标准**：生产可部署；上述验收全部通过；补一组授权矩阵测试。

### Phase 1：授权与体验模型（10–13 人日）

| ID | 任务 | 依赖 | 验收 |
|---|---|---|---|
| P1-1 | 版本化迁移 + 备份；新增字段/表 | — | 旧库平滑升级；失败回滚；备份存在 |
| P1-2 | Owner token：生成、哈希存储、校验中间件、历史房间兼容、前端存储/导出/门控 | P1-1、P0-2 | 非所有者无法改名/续期/销毁；所有者多设备可用 |
| P1-3 | 房间配额 + 全局水位 + 清理任务（孤儿文件、过期会话） | P1-1 | 超配额 507；清理后磁盘/记录一致 |
| P1-4 | WS 加固：Origin、读限制、单写协程、事件带消息体、连接上限 | P0-2 | `-race` 通过；跨站 Origin 被拒；慢客户端不影响他人 |
| P1-5 | 限流中间件（IP/房间） + 429/Retry-After | P0-1 | 超限返回 429；正常流量不受影响 |
| P1-6 | 前端应用壳与单滚动容器 + 滚动锚定 | P0-7 | 桌面/移动均无双重滚动；更新不跳位 |
| P1-7 | 消息 keyed 复用 + 单条事件更新 + 分页接口 | P1-4、P1-6 | 1000 条消息滚动流畅；图片不重载；选择不丢 |
| P1-8 | 移动端底部 composer + visualViewport + 草稿 + 乐观发送 | P1-7 | 手机 1 步发送；键盘不遮挡；输入不被打断 |
| P1-9 | 复制/粘贴统一：安全上下文回退、消息复制、图片粘贴、选择恢复 | P1-7 | HTTP 局域网可复制；长文本可选中；截图可粘贴 |
| P1-10 | 安全头 CSP 基础版 + 移除内联事件 | P0-5 | CSP 无报错；键盘功能正常 |
| P1-11 | 测试：授权矩阵、上传限额、限流、WS 并发、移动端 Playwright | 全部 | CI 全绿 |

**Phase 1 完成标准**：授权模型闭环；手机端可用性达标；无已知高危问题。

### Phase 2：体验与工程质量（14–18 人日）

| ID | 任务 | 验收 |
|---|---|---|
| P2-1 | 消息分页 + 虚拟列表/尾部裁剪 | 1 万条消息首屏 < 1s，滚动 60fps |
| P2-2 | 样式系统重构：`@layer`、tokens、对比度、图标 mask、44px、去内联样式 | 无未定义变量；AA；主题无闪烁；Lighthouse a11y ≥ 90 |
| P2-3 | 动效规范：入场/抽屉/toast/reduced-motion；管理页去 3D 翻转 | 无无限动画干扰；低端机无明显掉帧 |
| P2-4 | 管理后台：受控表单、未保存提示、搜索/筛选、图表懒加载 | 设置不丢；切 tab 正常；首屏不加载 chart |
| P2-5 | PWA：版本化 SW、share_target、离线壳 | 部署后自动更新；分享可用 |
| P2-6 | 性能：gzip/br、缓存头、删除死文件、静态 handler | 首屏 JS ≤ 180KB；重复访问命中缓存 |
| P2-7 | 测试：Playwright 全流程、负载/内存、设备矩阵、恢复演练 | 第 5 节全部通过 |
| P2-8 | 文档：部署、备份恢复、运维、升级说明 | README/运维手册更新 |

### 里程碑

- **M0（Phase 0）**：可安全暴露在公网的最小版本。
- **M1（Phase 1）**：授权闭环 + 移动端可日常使用。
- **M2（Phase 2）**：体验与工程质量达标，可长期维护。

---

## 5. 测试与验收

### 5.1 自动化

| 类型 | 覆盖 | 工具 |
|---|---|---|
| Go 单元/集成 | 授权矩阵、上传限额、配额、KDF、会话 TTL、限流、下载头 | `go test ./... -race` + `httptest` |
| 前端 E2E | 移动端 composer、IME、滚动锚定、复制回退、图片粘贴、焦点陷阱 | Playwright（移动 viewport） |
| 安全 | IDOR、枚举、XSS 载荷、爆破、WS Origin、CSP | `curl` 脚本 + 手工 |
| 性能 | 并发下载内存、1GB 上传拒绝、1 万条消息渲染 | `pprof`、k6/`hey`、Lighthouse |

授权矩阵测试至少包含：

1. 房间 A 的 code 不能读取/删除/下载房间 B 的消息与文件。
2. 全局消息 ID 路径全部 404。
3. 非所有者不能改名/续期/设长期/销毁。
4. 所有者 token 错误/缺失返回 403；正确则成功。
5. 管理接口未登录 401；改密后旧 Cookie 401。
6. 上传声明超限/累计超限/跨房间会话全部拒绝。

### 5.2 手工/设备矩阵

- iOS Safari（当前主流版本，含 PWA standalone）、Android Chrome、桌面 Chrome/Edge/Firefox/Safari。
- 键盘：Tab 顺序、Esc 关闭、Enter/Space 激活、焦点环可见。
- 网络：3G 慢网、断网重连、WS 降级轮询、恢复。
- 输入法：中文拼音/五笔、日文、组合输入中回车。
- 剪贴板：HTTPS、HTTP 局域网、iOS 权限拒绝、无 Clipboard API。

### 5.3 安全验收清单

```bash
BASE=http://localhost:8080

# 1) 枚举接口不存在
curl -s -o /dev/null -w '%{http_code}\n' $BASE/api/rooms                  # 期望 404/405

# 2) 跨房间/全局 ID 不可用
curl -s -o /dev/null -w '%{http_code}\n' $BASE/api/messages/1/file        # 期望 404
curl -s -o /dev/null -w '%{http_code}\n' $BASE/api/rooms/AAAA/messages/1  # 期望 404（若 1 属于其他房间）

# 3) 所有者操作
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE $BASE/api/rooms/AAAA    # 期望 403
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE -H 'X-Room-Owner: ...' $BASE/api/rooms/AAAA  # 期望 200

# 4) 下载头
curl -sI $BASE/api/rooms/AAAA/messages/1/file | grep -Ei 'content-disposition|nosniff|content-security'

# 5) 上传限额（声明 10GB）
curl -s -o /dev/null -w '%{http_code}\n' -X POST $BASE/api/rooms/AAAA/uploads \
  -H 'Content-Type: application/json' -d '{"fileName":"big.bin","fileSize":10737418240}'
# 期望 413

# 6) 安全头
curl -sI $BASE/ | grep -Ei 'content-security|x-frame|x-content-type|referrer'
```

### 5.4 性能指标

- 首页首屏（本地、无缓存）≤ 1.5s；JS ≤ 180KB（未压缩）。
- 消息区 1000 条滚动无明显掉帧；更新单条不触发整列表重排。
- 100 并发 100MB 下载，服务端 RSS 增长 < 50MB。
- 1GB 上传在 10MB 限额下被立即拒绝，磁盘写入 0。
- WS 1000 连接下广播延迟 p95 < 100ms（单机）。

---

## 6. 发布、兼容与回滚

### 6.1 兼容与版本握手

Phase 0 的 API 是破坏性变更（旧 `/api/messages/*` 移除）。必须同批上线：

1. `index.html` 动态注入 `__BUILD__`，静态资源加 `?v=`。
2. 前端启动比较服务端版本，不一致则清 SW 缓存并强制刷新一次。
3. Phase 0 移除 SW 注册并清理旧缓存。
4. 服务端保留 `GET /api/rooms` 的 404 行为，不保留旧路由（安全优先）。
5. 发布说明明确：旧标签页需刷新。

如果希望平滑过渡，可在第一阶段让旧路由返回 404 而非 410，并让新前端兜底提示“请刷新页面”。

### 6.2 数据库迁移

- 迁移前自动备份；迁移在事务中执行；失败不启动（fail fast）。
- 只做加法（新增列/表/索引），不做删除/重命名，保证可回滚到旧镜像。
- 单实例 SQLite：迁移期间短暂停写；启动失败时保留备份与日志。
- 生产升级窗口选择低峰；升级后立即跑冒烟脚本（第 5.3 节）。

### 6.3 灰度与回滚

- 可先以 `APP_ENFORCE_OWNER_TOKEN=false` 上线（仅记录“缺少 token”日志），观察一天后改为 true；但 Phase 0 的作用域路由必须立即生效。
- 回滚镜像即可；数据库为加法迁移，旧镜像可继续运行（新列被忽略）。
- 若配额逻辑异常，提供 `APP_UPLOAD_QUOTA_BYTES=0` 临时关闭配额（仍保留单文件上限）。
- 记录发布版本、commit、迁移版本；回滚时确认迁移版本兼容。

### 6.4 监控

- 结构化日志：请求 ID、路径、状态码、耗时、IP（脱敏）、房间码（可哈希）。
- 指标：活跃房间、消息/上传速率、上传失败率、磁盘使用率、WS 连接数、限流命中、登录失败。
- 告警：磁盘 > 80%、5xx 比例、迁移失败、备份失败。
- 管理端显示存储使用与配额（可选）。

---

## 7. 待确认的产品决策

| # | 决策 | 建议 | 影响 |
|---|---|---|---|
| 1 | 删除/置顶消息是否仅所有者 | 默认所有房间参与者；提供开关 | 协作体验 vs 防破坏 |
| 2 | 长期房间策略 | 仅所有者可设；每 IP 上限；管理员可关闭 | 存储成本 |
| 3 | 房间码长度 | 默认 8，最小 6；已有 4 位不改 | 熵与可用性 |
| 4 | 文件类型策略 | 黑名单（svg/html/js/exe/sh…）+ 图片白名单 inline | 安全 vs 兼容 |
| 5 | 所有房间默认 TTL | 24h；永久房间需显式设置 | 数据保留 |
| 6 | owner token 迁移 | 房间设置内可查看/复制；丢失只能管理员处理 | 可用性 vs 安全 |
| 7 | 端到端加密 | 单独立项，不在本次 | 产品定位 |

---

## 8. 附录

### A. 文件改动清单

| 文件 | 改动 | 阶段 |
|---|---|---|
| `internal/db/migrate.go` | 版本化迁移、事务、备份 | P1 |
| `sql/init.sql` / `sql/migrations/*` | 基线拆分、新表/列 | P1 |
| `internal/http/router.go` | 路由收敛、静态 handler、安全头 | P0/P1 |
| `internal/http/handlers_rooms.go` | 归属校验、owner、分页、上传限额 | P0/P1 |
| `internal/http/handlers_files.go` | 流式下载、类型白名单、Disposition | P0 |
| `internal/http/handlers_sync.go` | Origin、读限制、welcome | P1 |
| `internal/http/helpers.go` / `session.go` | 登录限速、TTL、Cookie、审计 | P0/P1 |
| `internal/http/json.go` / `main.go` | body 限制、server timeouts、安全头 | P0 |
| `internal/realtime/hub.go` | 单写协程、背压、连接上限 | P1 |
| `internal/service/room.go` | owner、配额、清理、错误码 | P0/P1 |
| `internal/service/file.go` | 限额、临时文件、检测、流式读 | P0/P1 |
| `internal/service/admin.go` | KDF、会话失效、当前密码 | P0 |
| `internal/service/maintenance.go` | 过期会话/孤儿文件/统计频率 | P1 |
| `internal/store/message_store.go` | 去掉路径字段、分页、幂等、配额 | P1 |
| `internal/store/room_store.go` | owner 哈希、used_bytes、last_active | P1 |
| `internal/store/*` | 审计、迁移版本 | P1 |
| `static/js/app.js` | 拆分渲染、滚动锚定、composer、粘贴、焦点 | P0/P1 |
| `static/js/api.js` | 新路由、owner header、下载 URL | P0 |
| `static/js/realtime.js` | 增量事件、超时/重连策略 | P1 |
| `static/js/state.js` | 移除移动端门控状态、草稿、锚点 | P1 |
| `static/css/tokens.css` | 补变量、对比度、motion、滚动条 | P0/P2 |
| `static/css/layout.css` | 单滚动容器、grid、安全区 | P1 |
| `static/css/components.css` | 生命周期、composer、toast、44px | P1/P2 |
| `static/css/animations.css` | reduced-motion、去翻转、新入场 | P2 |
| `static/index.html` | 版本注入、SW 清理、defer、移除内联事件 | P0 |
| `static/sw.js` | Phase 0 移除；Phase 2 版本化 | P0/P2 |
| `static/manifest.webmanifest` | share_target、PNG 图标 | P2 |
| `Dockerfile` / `README.md` | 健康检查、部署安全说明、环境变量 | P2 |
| `docs/remediation-plan.md` | 本方案 | — |

### B. 新增/调整的错误码

见 2.3.3。前端按 `code` 做分支，展示 `error` 文案；未知 `code` 走通用提示。

### C. 部署检查表

- [ ] TLS 终止与 HSTS
- [ ] 反向代理限制 body/超时，传递真实 IP（可信代理白名单）
- [ ] 数据卷权限、备份任务、恢复演练
- [ ] `APP_ADMIN_PASSWORD` 不长期驻留环境变量
- [ ] `APP_RESET_ADMIN_PASSWORD` 使用后移除
- [ ] 磁盘水位告警
- [ ] 日志脱敏（房间码/IP/密码）
- [ ] 版本握手与 SW 清理验证
- [ ] 冒烟脚本（第 5.3 节）

### D. 估算汇总

| 阶段 | 人日 | 可并行项 |
|---|---|---|
| Phase 0 | 5–7 | 后端 P0-1~P0-6 与前端 P0-7 可并行 |
| Phase 1 | 10–13 | 后端 P1-1~P1-5 与前端 P1-6~P1-10 可并行，P1-7 依赖 P1-4 |
| Phase 2 | 14–18 | 样式/动效与管理后台可并行 |
| 合计 | 29–38 人日 | 双人约 3–4 周 |

---

## 9. 一页纸执行顺序

1. 删 `GET /api/rooms`，消息/文件路由加房间作用域。
2. 下载改流式 + 强制 attachment + 禁 SVG inline。
3. 上传服务端限额 + 会话过期 + 配额。
4. 管理登录限速、会话 TTL、强制改密、KDF 换标准库。
5. 前端 IME、移动端 composer、生命周期 class、SW/版本。
6. 所有者令牌 + 版本化迁移 + 审计。
7. 单滚动容器 + 消息 keyed 复用 + 滚动锚定。
8. 移动端底部 composer + visualViewport + 草稿 + 乐观发送。
9. 复制/粘贴统一 + 长文本可选择。
10. 样式 token/对比度/触控目标/动效/reduced-motion。
11. 分页/虚拟列表、图表懒加载、gzip、SW 版本化。
12. 测试矩阵、监控、备份恢复、发布说明。

---

## 10. Phase 0 实施状态（2026-10-23）

### 10.1 已完成

| 项 | 状态 | 说明 |
|---|---|---|
| P0-1 房间枚举 | 完成 | 公开 `GET /api/rooms` 返回 405；仅保留创建接口 |
| P0-2 房间作用域路由 | 完成 | 消息/文件/置顶/删除/上传全部收敛到 `/api/rooms/{code}/...`，跨房间与全局 ID 返回 404；前端 `api.js` 已适配 |
| P0-3 下载加固 | 完成 | `http.ServeContent` 流式 + Range；默认 attachment；RFC 5987 文件名；SVG/HTML/XML 强制 octet-stream 且不 inline；`nosniff` + `CSP: sandbox` |
| P0-4 上传限额 | 完成 | 声明大小、累计大小、单块大小校验；响应不再包含 `storedPath`/`filePath`；过期上传会话与 `.partial` 文件清理 |
| P0-5 HTTP 加固 | 完成 | JSON body 1MB 限制；`ReadHeaderTimeout`/`IdleTimeout`/`MaxHeaderBytes`；`nosniff`/`X-Frame-Options`/`Referrer-Policy`/`Permissions-Policy` |
| P0-6 管理后台 | 完成 | 登录限速/锁定（429+Retry-After）；会话空闲 2h/绝对 24h；SameSite=Strict + 按 TLS/代理设置 Secure；默认密码未改时管理接口 403；改密需当前密码并使旧会话失效；BootstrapStatus 不再公开默认密码状态 |
| P0-7 前端急救 | 完成 | API 契约适配；IME 组合输入保护；移动端 Enter 换行；移除全局 Enter 上传；移动端 composer 默认可见；生命周期 class 修复；管理改密当前密码 UI；版本握手 + 旧 SW/cache 清理；CSS 未定义变量与滚动条修复 |
| 自动化测试 | 完成 | 新增会话 TTL/Cookie/限流/改密单测，以及作用域授权、上传限额、SVG 下载头、管理改密全流程、版本注入集成测试 |

### 10.2 验证结果

- `go build ./...`、`go vet ./...`、`go test ./...` 通过。
- 真实服务冒烟 22/22 通过：枚举接口 405、跨房间/全局 ID 404、SVG 强制下载、超限上传 413、默认密码 403 闸门、改密后旧会话 401、5 次失败后 429+Retry-After。
- `node --check` 对 `app.js`、`api.js`、`state.js` 通过；真实 `/` 已注入 build/apiVersion，静态资源带 `?v=` 且使用 immutable 缓存。

### 10.3 已知边界（留到 Phase 1）

- **owner token 尚未接入**：消息删除/置顶仍以“持有房间码”为权限，跨房间 IDOR 已关闭，但同一房间内任意参与者仍可删除/置顶。Phase 1 的 P1-2 会补所有者令牌。
- **管理员 KDF 仍为现有实现**：Phase 1 换 `crypto/pbkdf2`/argon2id；同时补审计日志、CSRF Origin 校验、会话定期清理协程。
- **限流按 `RemoteAddr`**：反向代理后所有客户端共享代理 IP，攻击者可能锁定管理员。部署时应在反代层限流，或 Phase 1 增加可信代理 XFF 解析。
- **移动端 composer 仅“默认可见”**：仍是房间卡内的输入框，尚未固定底部；滑滚动锚定、keyed 复用、复制粘贴重构、分页/虚拟列表均按 P1/P2 执行。
- **CSP 未启用**：版本握手依赖 `index.html` 内联脚本；Phase 2 移除内联脚本后加 nonce/hash 的严格 CSP。
- **Service Worker 已停用并清理**：Phase 2 再以版本化方式重新引入。
- **首次管理员密码**：仍使用项目默认密码，但未改密前管理接口全部 403，必须在登录后立即修改；生产建议通过 `APP_RESET_ADMIN_PASSWORD` 初始化后移除该环境变量。
- **上传配额/全盘水位、孤儿文件全目录扫描**：Phase 1 的 P1-3 补。

### 10.4 回滚说明

Phase 0 未改数据库结构，回滚只需回退二进制与静态资源；旧数据可直接被新版本读取。API 为破坏性变更，旧标签页需刷新（版本握手会触发一次自动刷新）。

### 10.5 下一步

按计划进入 Phase 1：版本化迁移 + owner token、配额/清理、WS 加固、限流中间件、单滚动容器与滚动锚定、keyed 消息渲染、移动端底部 composer、复制粘贴统一、测试矩阵扩展。

