# Umans Transparent Gateway

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![API](https://img.shields.io/badge/API-Anthropic%20%7C%20OpenAI-blue)](#api-surface)
[![Keys](https://img.shields.io/badge/API%20keys-passthrough%20only-green)](#security-boundary)

Self-hosted transparent gateway for Umans-compatible coding model APIs.

It lets you point Claude Code, OpenAI SDK clients, ccswitch, or other tools at a server you control, while preserving the visible behavior exposed by the Umans CLI: compatible API paths, server-side search header forwarding, raw image/tool payload passthrough, model suffix normalization, per-key concurrency protection, and short retries for transient upstream failures.

> Copyright ©️生🐟

## Language

- [English](#english)
- [中文](#中文)

---

## English

### Why This Exists

The Umans CLI is useful, but some users do not want to run external installer or launcher scripts on their primary machine. This gateway moves the integration point to a server-side Go service:

- clients call standard-compatible HTTP APIs;
- user API keys are passed through per request;
- the gateway does not implement login or key acquisition;
- local machines do not need to run the Umans CLI.

### Features

- **Anthropic Messages API passthrough**: `POST /v1/messages`
- **Anthropic token counting passthrough**: `POST /v1/messages/count_tokens`
- **OpenAI Chat Completions passthrough**: `POST /v1/chat/completions`
- **OpenAI Responses compatibility layer**: `POST /v1/responses`
- **Model catalog passthrough**: `GET /v1/models`, `GET /v1/models/info`
- **SSE streaming**: preserved for Messages and Chat Completions
- **WebSocket bridge**: `GET /ws`
- **Image/tool/reasoning payload preservation**: unknown fields and image blocks are forwarded
- **Server-side search header forwarding**: `X-Umans-Websearch-Provider`
- **Per-key concurrency queue**: default 4 active requests per API key
- **Transient upstream retry**: default 2 retries for temporary unavailable, `429`, `5xx`, `502`, `503`, `504`, and `529`
- **Claude Code model suffix cleanup**: `umans-glm-5.2[1m]` is forwarded as `umans-glm-5.2`

### Quick Start

```bash
go run ./cmd/gateway
```

Health check:

```bash
curl http://127.0.0.1:8080/healthz
```

Example Anthropic Messages call:

```bash
curl http://127.0.0.1:8080/v1/messages \
  -H "x-api-key: $UMANS_API_KEY" \
  -H "content-type: application/json" \
  -d '{
    "model": "umans-glm-5.2",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Say hello."}]
  }'
```

Example OpenAI Chat Completions call:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "authorization: Bearer $UMANS_API_KEY" \
  -H "content-type: application/json" \
  -d '{
    "model": "umans-glm-5.2",
    "messages": [{"role": "user", "content": "Say hello."}]
  }'
```

### Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `UMANS_GATEWAY_LISTEN` | `0.0.0.0:8080` | HTTP listen address |
| `UMANS_UPSTREAM_BASE_URL` | `https://api.code.umans.ai` | Upstream Umans-compatible API base URL |
| `UMANS_SEARCH_MODE` | `auto` | `auto`, `native`, `exa`, or `none` |
| `UMANS_BUDGET_POLICY` | `error` | `error` or `clamp-visible` for output token budget handling |
| `UMANS_KEY_CONCURRENCY_LIMIT` | `4` | Active request limit per API key |
| `UMANS_KEY_QUEUE_TIMEOUT` | `10m` | Max time a request waits for a per-key slot |
| `UMANS_UPSTREAM_RETRY_MAX` | `2` | Retry count after the first upstream attempt |
| `UMANS_UPSTREAM_RETRY_BASE_DELAY` | `2s` | Initial retry delay |
| `UMANS_UPSTREAM_RETRY_MAX_DELAY` | `5s` | Maximum retry delay |
| `UMANS_CATALOG_TTL` | `10m` | Model catalog cache TTL |

### Claude Code / ccswitch

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-xxxx",
    "ANTHROPIC_BASE_URL": "http://your-server:8080",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "umans-glm-5.2",
    "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "GLM 5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "umans-glm-5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "GLM 5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "umans-glm-5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "GLM 5.2",
    "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "90",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "405504",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "131071",
    "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING": "1",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0",
    "DISABLE_NON_ESSENTIAL_MODEL_CALLS": "1",
    "ENABLE_TOOL_SEARCH": "false"
  },
  "includeCoAuthoredBy": false,
  "model": "sonnet",
  "effortLevel": "xhigh"
}
```

`CLAUDE_CODE_AUTO_COMPACT_WINDOW=405504` is the GLM 5.2 context window setting. `CLAUDE_CODE_MAX_OUTPUT_TOKENS=131071` is the output cap, not the context window.

### API Surface

```text
POST /v1/messages
POST /v1/messages/count_tokens
POST /v1/chat/completions
POST /v1/responses
GET  /v1/models
GET  /v1/models/info
GET  /v1/usage
GET  /healthz
GET  /ws
```

`/v1/messages` and `/v1/chat/completions` are raw proxy paths. `/v1/responses` is a compatibility layer that converts OpenAI Responses-shaped input to Chat Completions upstream and converts the response back to a Responses-shaped object.

### Security Boundary

- API keys are accepted per request via `Authorization: Bearer <key>` or `x-api-key: <key>`.
- API keys are forwarded to the upstream service and are not persisted by this gateway.
- Per-key concurrency uses an in-memory HMAC bucket, not the plaintext key.
- The gateway does not run Umans installer scripts.
- The gateway does not write `~/.umans`, `~/.claude`, or `/usr/local/bin`.
- Image recognition, server-side search, and compaction are not reimplemented locally; the gateway forwards the request semantics exposed by the Umans-compatible upstream.

### Deployment

See [DEPLOYMENT.md](DEPLOYMENT.md) for direct binary, systemd, and Docker Compose deployment.

### Development

```bash
go test ./...
go build ./cmd/gateway
```

### License

MIT. See [LICENSE](LICENSE).

### Disclaimer

This project is an independent transparent gateway implementation. It is not affiliated with Umans, Anthropic, or OpenAI.

---

## 中文

### 项目定位

Umans CLI 本身能用，但如果你不想在主力机器上运行外部 installer 或 launcher 脚本，可以把接入点移到服务器侧：客户端只调用标准兼容 API，API key 每次请求透传，本机不需要运行 Umans CLI。

### 功能

- **Anthropic Messages API 透传**：`POST /v1/messages`
- **Anthropic token counting 透传**：`POST /v1/messages/count_tokens`
- **OpenAI Chat Completions 透传**：`POST /v1/chat/completions`
- **OpenAI Responses 兼容层**：`POST /v1/responses`
- **模型目录透传**：`GET /v1/models`、`GET /v1/models/info`
- **SSE 流式输出**：保留 Messages 和 Chat Completions 流式行为
- **WebSocket bridge**：`GET /ws`
- **图片、工具、reasoning 字段保留**：未知字段和图片块原样转发
- **服务器搜索 header 转发**：`X-Umans-Websearch-Provider`
- **按 key 并发队列**：默认每个 API key 同时 4 个请求
- **上游瞬断自动重试**：默认对临时不可用、`429`、`5xx`、`502`、`503`、`504`、`529` 重试 2 次
- **Claude Code 模型后缀清洗**：`umans-glm-5.2[1m]` 会按 `umans-glm-5.2` 转发

### 快速开始

```bash
go run ./cmd/gateway
```

健康检查：

```bash
curl http://127.0.0.1:8080/healthz
```

Anthropic Messages 示例：

```bash
curl http://127.0.0.1:8080/v1/messages \
  -H "x-api-key: $UMANS_API_KEY" \
  -H "content-type: application/json" \
  -d '{
    "model": "umans-glm-5.2",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Say hello."}]
  }'
```

OpenAI Chat Completions 示例：

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "authorization: Bearer $UMANS_API_KEY" \
  -H "content-type: application/json" \
  -d '{
    "model": "umans-glm-5.2",
    "messages": [{"role": "user", "content": "Say hello."}]
  }'
```

### 配置

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `UMANS_GATEWAY_LISTEN` | `0.0.0.0:8080` | HTTP 监听地址 |
| `UMANS_UPSTREAM_BASE_URL` | `https://api.code.umans.ai` | Umans-compatible 上游 API base URL |
| `UMANS_SEARCH_MODE` | `auto` | `auto`、`native`、`exa` 或 `none` |
| `UMANS_BUDGET_POLICY` | `error` | 输出 token 预算策略：`error` 或 `clamp-visible` |
| `UMANS_KEY_CONCURRENCY_LIMIT` | `4` | 每个 API key 的同时活跃请求上限 |
| `UMANS_KEY_QUEUE_TIMEOUT` | `10m` | 同 key 请求排队等待上限 |
| `UMANS_UPSTREAM_RETRY_MAX` | `2` | 首次上游请求失败后的重试次数 |
| `UMANS_UPSTREAM_RETRY_BASE_DELAY` | `2s` | 初始重试等待时间 |
| `UMANS_UPSTREAM_RETRY_MAX_DELAY` | `5s` | 最大重试等待时间 |
| `UMANS_CATALOG_TTL` | `10m` | 模型目录缓存时间 |

### Claude Code / ccswitch

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-xxxx",
    "ANTHROPIC_BASE_URL": "http://your-server:8080",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "umans-glm-5.2",
    "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME": "GLM 5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "umans-glm-5.2",
    "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "GLM 5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "umans-glm-5.2",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME": "GLM 5.2",
    "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE": "90",
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "405504",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "131071",
    "CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING": "1",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0",
    "DISABLE_NON_ESSENTIAL_MODEL_CALLS": "1",
    "ENABLE_TOOL_SEARCH": "false"
  },
  "includeCoAuthoredBy": false,
  "model": "sonnet",
  "effortLevel": "xhigh"
}
```

`CLAUDE_CODE_AUTO_COMPACT_WINDOW=405504` 是 GLM 5.2 上下文窗口设置。`CLAUDE_CODE_MAX_OUTPUT_TOKENS=131071` 是输出上限，不是上下文窗口。

### API Surface

```text
POST /v1/messages
POST /v1/messages/count_tokens
POST /v1/chat/completions
POST /v1/responses
GET  /v1/models
GET  /v1/models/info
GET  /v1/usage
GET  /healthz
GET  /ws
```

`/v1/messages` 和 `/v1/chat/completions` 是 raw proxy，保留未知字段、工具、thinking/reasoning、图片和 SSE。`/v1/responses` 是兼容层：对外接受 OpenAI Responses 风格，内部转换到 Chat Completions，再转回 Responses 风格。

### 安全边界

- API key 通过 `Authorization: Bearer <key>` 或 `x-api-key: <key>` 每请求传入。
- API key 只转发给上游，不由本服务持久化保存。
- 同 key 并发控制使用内存 HMAC bucket，不使用明文 key 做桶 ID。
- 不运行 Umans installer 脚本。
- 不写 `~/.umans`、`~/.claude` 或 `/usr/local/bin`。
- 图片识别、服务器搜索和 compaction 不在本地重做；网关只转发 Umans-compatible 上游暴露的请求语义。

### 部署

直接二进制、systemd 和 Docker Compose 部署方式见 [DEPLOYMENT.md](DEPLOYMENT.md)。

### 开发

```bash
go test ./...
go build ./cmd/gateway
```

### License

MIT，见 [LICENSE](LICENSE)。

### Disclaimer

本项目是独立的透明网关实现，不隶属于 Umans、Anthropic 或 OpenAI。
