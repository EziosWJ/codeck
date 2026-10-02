# AGENTS.md

Codeck 是一个跑在本机的 Codex 控制台：Go 单二进制 + SQLite + React UI（`web/`，构建产物用 `//go:embed` 打进二进制）。入口 `main.go`，后端在 `internal/`。

## Agent skills

### Issue tracker

Issues 和 spec 以 GitHub Issue 形式存在 `EziosWJ/codeck`，用 `gh` CLI 操作；ADR 也以 Issue 形式发布（ADR-0001 是 #1）。见 `docs/agents/issue-tracker.md`。

### Triage labels

使用 `triage` skill 的五个默认标签：`needs-triage` / `needs-info` / `ready-for-agent` / `ready-for-human` / `wontfix`。见 `docs/agents/triage-labels.md`。

### Domain docs

单上下文仓库：根目录 `CONTEXT.md` 是术语表（中文界面 ↔ 英文 API/库字段的对照与 `_Avoid_` 反例）。见 `docs/agents/domain.md`。

## 本地约定

- 任务入口是 `Taskfile.yml`；验证用 `task test`、`task lint`、`task build:web`。
- `CGO_ENABLED=0` 是硬约束：SQLite 走纯 Go 驱动 `modernc.org/sqlite`，不要换成 CGO 驱动。
- `web/dist/` 是构建产物且被 gitignore；Go 构建前它必须存在，`task build` 会替你排好顺序。
- `data/` 是运行库（SQLite + 各 Profile 的 CODEX_HOME/workspace），不要提交，也不要在测试里指向真实库。
- 后端默认只监听 `127.0.0.1:8080`；非 loopback 必须配 `AUTH_USER`/`AUTH_PASSWORD`。
