# cronCodex

本地管理控制台：用隔离 Profile 调 Codex CLI 聊天，并按 cron 定时跑 prompt。单二进制，状态在一份 SQLite。不调模型 HTTP API，只 `exec` 本机 `codex`。

UI 品牌名 Codex Control。默认 `:8080`，数据 `./data/`。

## 命令

`task` / `task --list`。常用：`setup`、`run`、`dev`、`test`、`lint`、`check`。

- 发布目标：`CGO_ENABLED=0` 静态二进制，UI 经 `//go:embed all:web/dist` 打进 `main.go`。先 `task build:web` 再 `go build`。
- 集成测试花 token：`CRONCODEX_INTEGRATION=1 go test -count=1 -v -run TestIntegrationRealCodex ./internal/codex/`
- 探测 App Server 账号接口：`task appserver-probe`（打印 `account/read`、`account/rateLimits/read`、`account/usage/read` 的原始 JSON）

## 布局

```
main.go                 启动、嵌入 UI、SIGINT 关停
internal/config         CRONCODEX_* 与 KEY=VALUE 文件
internal/store          SQLite、迁移、DAO
internal/codex          唯一执行 Codex CLI 的包
internal/scheduler      到期轮询；cron 库只解析表达式
internal/httpapi        REST + 静态前端
web/                    React 19 + Vite，产物 web/dist
data/                   db、每 Profile 的 CODEX_HOME 与 workspace
```

改 CLI 调用只动 `internal/codex`。调度到期决策在 `scheduler` 循环，不把任务注册进 cron 运行时。HTTP 只校验表达式并走 `RunNow`。

## 对象

| 对象 | 要点 |
|---|---|
| Profile | 一份 Codex 配置。`name` 即 `data/codex-home/<name>/` 目录名。启动时生成 `config.toml`，把 `AUTH_SOURCE`（默认 `~/.codex/auth.json`）symlink 进 home，不复制 token。 |
| Conversation | 绑一个 Profile。`thread_id` 用于 `codex exec resume`。聊天 `POST /api/chat/stream`（SSE）。 |
| Task | prompt + Profile + cron（五字段或 `@daily` / `@every 1h`）+ 超时。`next_run_at` 写库。 |
| TaskRun | 一次执行：`schedule` / `manual`，输出、错误、token、耗时。 |

磁盘：`data/croncodex.db`、`data/codex-home/<profile>/`、`data/workspace/<profile>/`。Profile 可覆盖 `work_dir`。scratch workspace 默认空，避免吃到仓库 `AGENTS.md`。

## 不变量

- 调度状态在库：到期先推进 `next_run_at` 再启动，避免长任务被下一拍再派。同一 Task 禁止重叠。手动跑不改 `next_run_at`。坏表达式把 `next_run_at` 置空以停转。
- 并发默认 4（`MAX_CONCURRENT_RUNS`）。默认超时 5 分钟。轮询默认 10 秒。
- 启动把上次留下的 `running` 任务和聊天回复标失败。
- 调用：`codex exec --json --skip-git-repo-check`，prompt 走 stdin（`-`）。续聊 `exec resume --json <thread_id> -`（resume 无 `-s`，sandbox 靠生成的 config.toml）。超时杀进程组。
- App Server：进程随服务启动 `codex app-server --listen stdio://`，stdin/stdout 走 JSON-RPC JSONL（无 `jsonrpc` 版本字段），先 `initialize` 再 `initialized`。服务退出时关掉子进程。
- Profile `name`：`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`。显式默认：`sandbox_mode=read-only`、`approval_policy=never`、`reasoning_effort=low`。`include_*` 四项默认 `false`。精简模式强制这四项为 false，并写 `[skills] include_instructions = false`，再关一批重型 feature；不能配 `danger-full-access`。
- SQLite 单连接、WAL、纯 Go 驱动（`modernc.org/sqlite`）。时间存 UTC RFC3339Nano。
- 删 Profile CASCADE 其对话与任务，并删对应 home/workspace。

## 配置

前缀 `CRONCODEX_`。也可 `-config` 或 `CRONCODEX_CONFIG` 指向 `KEY=VALUE` 文件（可省略前缀）。后源覆盖先源。未知键忽略。

`ADDR` `DATA_DIR` `DB_PATH` `CODEX_BIN` `CODEX_HOME_ROOT` `WORKSPACE_ROOT` `AUTH_SOURCE` `SCHEDULER_INTERVAL` `DEFAULT_TIMEOUT` `MAX_CONCURRENT_RUNS` `LOG_LEVEL`

未显式设置时，`DB_PATH` / `CODEX_HOME_ROOT` / `WORKSPACE_ROOT` 都挂在 `DATA_DIR` 下。

## API

全在 `/api`。JSON；未知字段拒绝。聊天为 POST SSE。前端路由刷新回退 `index.html`。

```
GET  /health  /dashboard  /account  /history  /runs
CRUD /profiles  GET /profiles/{id}/config  POST /profiles/{id}/test  POST /profiles/{id}/prompt-preview
CRUD /conversations  PATCH /conversations/{id}
POST /chat/stream
CRUD /tasks  POST /tasks/{id}/run  GET /tasks/{id}/runs
```

`web/src/api.ts` 是前端唯一入口。开发：Vite `:5173` 代理 `/api` → `:8080`。
