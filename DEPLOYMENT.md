# 部署说明

本文面向服务器部署。服务不保存 API key，客户端请求时透传 `Authorization: Bearer <key>` 或 `x-api-key: <key>`。

## 方式一：直接二进制

Linux amd64:

```bash
install -m 0755 umans-gateway-linux-amd64 /usr/local/bin/umans-gateway
UMANS_GATEWAY_LISTEN=0.0.0.0:8080 /usr/local/bin/umans-gateway
```

Linux arm64:

```bash
install -m 0755 umans-gateway-linux-arm64 /usr/local/bin/umans-gateway
UMANS_GATEWAY_LISTEN=0.0.0.0:8080 /usr/local/bin/umans-gateway
```

## systemd

模板在：

```text
deploy/systemd/umans-gateway.service
```

安装示例：

```bash
sudo useradd --system --home /var/lib/umans-gateway --shell /usr/sbin/nologin umans-gateway
sudo mkdir -p /etc/umans-gateway /var/lib/umans-gateway
sudo cp .env.example /etc/umans-gateway/env
sudo install -m 0755 umans-gateway-linux-amd64 /usr/local/bin/umans-gateway
sudo cp deploy/systemd/umans-gateway.service /etc/systemd/system/umans-gateway.service
sudo systemctl daemon-reload
sudo systemctl enable --now umans-gateway
```

## Docker Compose

模板在：

```text
deploy/docker/docker-compose.yml
deploy/docker/Dockerfile
```

运行：

```bash
cp .env.example .env
docker compose -f deploy/docker/docker-compose.yml up -d --build
curl http://127.0.0.1:8080/healthz
```

## Claude Code / ccswitch

把 Claude Code base URL 指向服务器：

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

说明：

- `CLAUDE_CODE_AUTO_COMPACT_WINDOW=405504` 是 GLM 5.2 上下文窗口。
- `CLAUDE_CODE_MAX_OUTPUT_TOKENS=131071` 是输出上限，不是上下文窗口。
- `ENABLE_TOOL_SEARCH=false` 复刻 Umans CLI。Umans server-side search 由 `X-Umans-Websearch-Provider` 控制。
- 同 key 并发由网关限制，默认 `4`；不同 key 独立排队。
- `umans-glm-5.2[1m]` 这类 Claude Code 时长后缀会由网关清洗成基础模型 ID 再转发。

## 配置

```text
UMANS_GATEWAY_LISTEN=0.0.0.0:8080
UMANS_UPSTREAM_BASE_URL=https://api.code.umans.ai
UMANS_SEARCH_MODE=auto          # auto | native | exa | none
UMANS_BUDGET_POLICY=error       # error | clamp-visible
UMANS_KEY_CONCURRENCY_LIMIT=4
UMANS_KEY_QUEUE_TIMEOUT=10m
UMANS_UPSTREAM_RETRY_MAX=2
UMANS_UPSTREAM_RETRY_BASE_DELAY=2s
UMANS_UPSTREAM_RETRY_MAX_DELAY=5s
UMANS_CATALOG_TTL=10m
```
