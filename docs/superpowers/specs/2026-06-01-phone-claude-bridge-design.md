# Phone Claude Bridge — 设计文档

**日期:** 2026-06-01
**状态:** 设计中

## 一、项目目标

在保持当前 API Key / DeepSeek 方案的条件下，通过自建桥接服务 + Flutter App，
实现从手机端实时对话 Claude Code、查看运行结果、派发后台任务。

## 二、需求摘要

| 维度 | 选择 |
|------|------|
| 客户端 | Flutter（iOS + Android） |
| 交互模式 | 实时对话 + 任务派发（fire-and-forget） |
| 网络环境 | 局域网 + 外网全支持 |
| 主机模式 | 常驻守护进程，会话按需创建/切换/回收 |

## 三、架构总览

```
┌─────────────────────────┐   Tailscale 虚拟局域网 (100.x.x.x)  ┌───────────────────────────┐
│      Flutter App        │                                      │    Windows Go 守护进程     │
│                         │══ WebSocket (实时流式对话) ═══════════►│                           │
│  · ConversationPage     │                                      │  · HTTP + WS Server       │
│  · SessionListPage      │══ HTTP REST (管理操作) ═══════════════►│  · SessionManager          │
│  · FileBrowserSheet     │                                      │  · ConPTY 子进程池         │
│  · SettingsPage         │◄═══ FCM/APNs (公网推送通知) ═════════│  · SQLite 持久化           │
└─────────────────────────┘                                      └───────────────────────────┘
```

**关键决策：**

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 网络穿透 | Tailscale | 免费、零配置、WireGuard 端到端加密 |
| 桥接语言 | Go | 单文件 exe 部署、goroutine 天然适合并发子进程管理 |
| 终端方案 | ConPTY（Windows 伪终端） | Claude Code 进入交互模式后需要真实 TTY |
| 通信协议 | WebSocket + HTTP REST | WS 做实时流式，REST 做简单 CRUD |
| 推送通知 | Firebase Cloud Messaging | iOS/Android 跨平台统一方案 |
| 持久化 | SQLite | 轻量、无外部依赖，存会话历史与设备 token |
| 监听地址 | 127.0.0.1 + Tailscale IP | 本机可用，手机通过 Tailscale 访问，不暴露到 0.0.0.0 |

## 四、通信协议

### 4.1 WebSocket 消息格式

统一信封：

```json
{ "id": "uuid", "type": "chat|control|file|session|system", "ts": 1717276800, "payload": { ... } }
```

### 4.2 消息类型

| 类型 | Client → Server | Server → Client |
|------|----------------|-----------------|
| `chat` | `send`：发送消息 | `delta`：流式文本块<br>`done`：回复结束（含 token 用量）<br>`error`：错误信息 |
| `control` | `interrupt`：暂停当前生成 | `status`：会话状态变更 |
| `file` | `autocomplete`：请求路径补全 | `autocomplete_result`：匹配结果 |
| `system` | `ping` | `pong`<br>`notify`：系统通知 |
| `session` | `switch`：切换会话 | `info`：会话详情 |

### 4.3 HTTP REST API

```
Base: http://<tailscale-ip>:9527/api/v1

POST   /auth/pair          设备配对（获取 PIN）
POST   /auth/verify        验证 PIN（获取 JWT）
POST   /auth/refresh       刷新 JWT

POST   /sessions           创建新会话
GET    /sessions           列出所有会话
GET    /sessions/:id       会话详情
DELETE /sessions/:id       关闭会话
GET    /sessions/:id/history  历史消息

POST   /fs/list            浏览目录（@ 补全用途）
GET    /health             健康检查
```

### 4.4 认证：PIN 配对 + JWT

1. App 请求配对 → 服务端返回 4 位 PIN（有效期 5 分钟）
2. 用户在 App 输入 PIN → 服务端验证 → 返回 JWT（有效期 30 天，可刷新）
3. WebSocket 连接时带 `?token=<jwt>` 参数
4. 支持多设备，每设备独立 JWT

## 五、Go 守护进程设计

### 5.1 目录结构

```
phone-claude-bridge/
├── main.go
├── go.mod
├── config/
│   └── config.go          // 配置加载（YAML + 命令行）
├── auth/
│   ├── handler.go         // PIN 配对 + JWT
│   └── middleware.go      // JWT 验证
├── session/
│   ├── manager.go         // SessionManager：全局会话生命周期
│   ├── session.go         // Session 结构体
│   └── process.go         // ConPTY 子进程管理
├── transport/
│   ├── ws.go              // WebSocket Hub
│   ├── handler.go         // HTTP REST 处理器
│   └── message.go         // 消息序列化
├── filebrowser/
│   └── browser.go         // 文件浏览
├── notify/
│   └── fcm.go             // FCM 推送
└── store/
    └── sqlite.go           // SQLite
```

### 5.2 核心结构

```go
type SessionManager struct {
    mu       sync.RWMutex
    sessions map[string]*Session
    config   *Config
    db       *sql.DB
}

type Session struct {
    ID         string           // sess_xxxx
    Name       string
    WorkDir    string
    Cpty       *conpty.ConPTY   // Windows 伪终端
    Hub        *WSHub           // 关联的 WebSocket 连接池
    History    []Message
    CreatedAt  time.Time
    LastActive time.Time
    Status     SessionStatus    // running | idle | error | closed
}
```

### 5.3 子进程管理（ConPTY）

Claude Code 以交互模式运行，需要伪终端：

```
Go 程序 → ConPTY（伪终端 API） → claude.exe（以为自己在真实终端中）
```

```go
func (s *Session) Start() error {
    cpty, err := conpty.Start(
        exec.Command("claude"),
        conpty.ConPtyDimensions(120, 40),
    )
    s.Cpty = cpty

    go s.readLoop()   // 读 stdout → ANSI 剥离 → JSON → WebSocket push
    go s.waitLoop()   // 等待进程退出 → 通知手机 + 清理
    return nil
}
```

### 5.4 ANSI 输出处理

Claude Code TUI 输出包含大量 ANSI 转义序列。处理策略采用**轻量解析**：

- 颜色/样式 → 转为语义标签（`info`、`success`、`error`、`thinking`）
- 进度条/spinner → 提取文本内容，标注为 `thinking` 类型
- 光标移动/原地刷新 → 合并重复行，只推送最终状态
- 文件选择器 → **不在 Go 端处理**，由 Flutter App 通过 HTTP `/fs/list` 自行实现
- Markdown 正文 → 透传
- 工具调用输出 → 保留原样，标记代码块类型

### 5.5 会话生命周期

```
Create → Running → 空闲超过 idle_timeout → 自动 Destroy
                         ↓
                    收到 interrupt → 暂停生成（发送 Ctrl+C）
                         ↓
                    收到 destroy → 优雅关闭:
                      stdin 写 Ctrl+C → 等 5s → Kill()
                         ↓
                     通知所有 WS 连接 "session_closed"
                         ↓
                     从 Manager 移除
```

### 5.6 配置

```yaml
# phone-claude-bridge.yaml
server:
  listen_tailscale: "100.x.x.x:9527"
  listen_local: "127.0.0.1:9527"

auth:
  jwt_secret: ""           # 自动生成
  jwt_ttl: 720h            # 30 天
  pin_ttl: 5m

session:
  max_sessions: 10
  idle_timeout: 30m
  work_dir_default: "D:\\codeAgent\\phoneClaude"

claude:
  binary: "claude"
  env:
    ANTHROPIC_BASE_URL: "..."
    ANTHROPIC_AUTH_TOKEN: "..."
    ANTHROPIC_MODEL: "deepseek-v4-pro[1m]"

notify:
  fcm_enabled: true
```

## 六、Flutter App 设计

### 6.1 页面结构

| 页面 | 职责 |
|------|------|
| **SplashPage** | 自动尝试连接上次记住的服务器，连上跳列表页，连不上跳设置页 |
| **PairingPage** | 输入 Tailscale IP + PIN 完成配对 |
| **SessionListPage** | 活跃会话卡片列表 + 后台任务区 + 新建会话 FAB |
| **ChatPage** | 消息列表（Markdown + 代码高亮 + 工具调用卡片）+ 底部输入区 |
| **FileBrowserSheet** | BottomSheet 文件浏览器，选中插入 @path |
| **SettingsPage** | 服务器配置、重新配对、通知开关、关于 |

### 6.2 状态管理（Riverpod）

```dart
final connectionProvider = StateNotifierProvider<ConnectionNotifier, ConnectionState>(...);
final sessionListProvider = AsyncNotifierProvider<SessionListNotifier, List<SessionInfo>>(...);
final chatProvider = StateNotifierProvider.family<ChatNotifier, List<ChatMessage>, String>(...);
```

### 6.3 ChatPage 消息渲染

消息模型为结构化块序列：

```dart
class MessageBlock {
  final BlockType type;   // markdown | code | thinking | tool_call | tool_result | error
  final String content;
  final Map<String, dynamic>? metadata;
}
```

渲染映射：
- `markdown` → `flutter_markdown` 组件
- `code` → 语法高亮 + 一键复制
- `thinking` → 动画 loading 指示器
- `tool_call` / `tool_result` → 可折叠卡片
- `error` → 红色背景气泡

### 6.4 输入区

- 多行自适应文本输入框
- `@` 按钮触发 FileBrowserSheet → 选中文件后插入 `@path`
- 发送按钮 → 发送中变为 "暂停" 按钮
- 预留：附加按钮（图片/文件）

### 6.5 推送通知

```
Flutter App → 获取 FCM token → POST /devices 注册到 Go 服务器
Go 服务器 → 会话任务完成 → Firebase Admin SDK → FCM 推送
手机收到推送 → 点击通知 → 跳转对应 ChatPage
```

### 6.6 网络自动切换

```
App 启动 → 尝试连接 Tailscale IP
         → 失败 → 尝试局域网 IP（同网段 192.168.x.x）
         → 都失败 → 提示用户检查网络，进入离线模式
```

## 七、安全考量

- 所有通信仅通过 Tailscale 虚拟网络（WireGuard 端到端加密）
- 不绑定 `0.0.0.0`，避免局域网内暴露
- PIN 配对机制防止未授权设备连接
- JWT 30 天有效期 + 服务端可撤销设备
- 不传输 API Key 到手机端，API Key 仅存在于 Go 守护进程配置中

## 八、开发阶段规划

| 阶段 | 内容 | 产出 |
|------|------|------|
| **Phase 1** | Go 守护进程骨架 | HTTP server + 配置加载 + SQLite |
| **Phase 2** | ConPTY 集成 + Session 管理 | 创建/关闭会话，读写子进程 |
| **Phase 3** | WebSocket 实时通信 | WS hub + 消息路由 + ANSI 处理 |
| **Phase 4** | 认证系统 | PIN 配对 + JWT + 中间件 |
| **Phase 5** | Flutter App 基础 | 页面框架 + Riverpod + WebSocket 客户端 |
| **Phase 6** | Flutter ChatPage | 流式消息渲染 + Markdown + 代码高亮 |
| **Phase 7** | Flutter 会话管理 | 列表页 + 新建/删除 + 文件浏览器 |
| **Phase 8** | 推送通知 | FCM 集成 + 通知跳转 |
| **Phase 9** | 联调 + 体验优化 | 完整测试 + 断连重连 + 错误处理 |

## 九、未决事项

- Claude Code 的 ANSI 输出格式需实战观察后细化解析规则
- ConPTY 与 Claude Code 的兼容性需验证（Windows 10 19045 支持 ConPTY）
- Tailscale 手机端后台连接稳定性需测试
