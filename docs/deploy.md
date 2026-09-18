# 本机 systemd 部署

长期跑用 **user systemd**，数据目录与仓库分开。开发仍用 `task run` / `task dev`。

Codeck 是常驻进程：HTTP + 调度器 + `codex app-server` 子进程。已经吃 `SIGTERM`，关停超时 20s。SQLite 单连接 + WAL，同一时刻只能有一个进程打开那份库。

## 何时用

| 场景 | 怎么跑 |
|---|---|
| 改代码、热重载 | `task dev` |
| 前台验证一次 | `task run` |
| 定时任务、开机自启、崩溃拉起 | `systemctl --user` |

用 user unit，不用 `/etc/systemd/system/`。服务要读 `~/.codex/auth.json`，并 `exec` 用户 PATH 里的 `codex`（常见是 nvm 下的 `#!/usr/bin/env node` 包装）。root unit 会丢这两样。

## 布局

推荐安装根 `$HOME/.local/lib/codeck/`，不要把 unit 的 `DATA_DIR` 指到仓库 `./data`。

```
~/.local/lib/codeck/
  bin/codeck                 静态二进制
  etc/codeck.env             CODECK_*（无前缀的 KEY=VALUE）
  etc/proxy.env              HTTP(S)_PROXY；登录 shell 有代理时必填
  data/                      运行库：db、codex-home、workspace
~/.config/systemd/user/codeck.service
```

`auth.json` 保持 symlink 到 `$HOME/.codex/auth.json`，不复制 token。

## 安装

### 1. 二进制

仓库内：

```bash
task build:release
```

或用 `task package` 产出的 `dist/codeck-linux-amd64` / `dist/codeck-linux-aarch64`。`CGO_ENABLED=0`，纯 Go SQLite（`modernc.org/sqlite`），不要换成 CGO 驱动。

```bash
install -D -m 0755 ./codeck "$HOME/.local/lib/codeck/bin/codeck"
```

### 2. 数据

源实例先停掉，再拷。WAL 打开时要连 `codeck.db-wal` / `codeck.db-shm` 一起带走；只有 `codeck.db` 也可以。

```bash
mkdir -p "$HOME/.local/lib/codeck/data"
rsync -a --delete \
  --exclude='*.db-journal' \
  data/ "$HOME/.local/lib/codeck/data/"
```

空数据目录也可以：首次启动会建库。从开发树复制是为了带走已有 Profile / 任务。

拷完检查 `data/codex-home/<profile>/auth.json` 仍指向 `$HOME/.codex/auth.json`。

### 3. 配置

`~/.local/lib/codeck/etc/codeck.env`：

```
ADDR=127.0.0.1:3000
DATA_DIR=/home/USER/.local/lib/codeck/data
CODEX_BIN=/home/USER/.nvm/versions/node/v24.16.0/bin/codex
AUTH_SOURCE=/home/USER/.codex/auth.json
LOG_LEVEL=info
```

把 `USER` 和 `CODEX_BIN` 换成本机值。`CODEX_BIN` 用绝对路径。`ADDR` 写成 `127.0.0.1:端口`，不要只写 `:3000`（会绑到所有网卡）。

键的完整说明见 [README 配置](../README.md#配置)。此文件由进程 `-config` 读取，**不会**变成子进程环境变量；代理不能写在这里。

### 4. 代理

user unit **不继承** 登录 shell 的 `HTTP_PROXY`。`account/read` 可以只靠本地 `auth.json` 成功；`account/rateLimits/read` 和 `account/usage/read` 要访问 `chatgpt.com` / `api.openai.com`。没代理时表现为：

- `account/rateLimits/read: context deadline exceeded`
- `account/usage/read: json-rpc error -32603: token usage profile fetch timed out`

这不是 App Server 没拉起。进程树里应有 `codex app-server --listen stdio://`，日志有 `codex app-server initialized`。

把 shell 里正在用的代理抄进 `~/.local/lib/codeck/etc/proxy.env`（大小写两套都写，Rust/Node 读取不一致）：

```
HTTP_PROXY=http://127.0.0.1:7897
HTTPS_PROXY=http://127.0.0.1:7897
http_proxy=http://127.0.0.1:7897
https_proxy=http://127.0.0.1:7897
NO_PROXY=127.*,localhost,<local>
no_proxy=127.*,localhost,<local>
```

直连外网的机器可以不建此文件，并把 unit 里的 `EnvironmentFile=` 删掉。

### 5. unit

`~/.config/systemd/user/codeck.service`：

```ini
[Unit]
Description=Codeck — local Codex console
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/lib/codeck/bin/codeck -config %h/.local/lib/codeck/etc/codeck.env
WorkingDirectory=%h/.local/lib/codeck
Environment=PATH=/home/USER/.nvm/versions/node/v24.16.0/bin:/home/USER/.local/bin:/usr/local/bin:/usr/bin:/bin
Environment=HOME=/home/USER
EnvironmentFile=%h/.local/lib/codeck/etc/proxy.env
Restart=on-failure
RestartSec=3
TimeoutStopSec=25
KillMode=mixed
KillSignal=SIGTERM
NoNewPrivileges=true
PrivateTmp=true
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

`PATH` 必须包含 `node`：`codex` 的 shebang 是 `#!/usr/bin/env node`。user unit 不会加载 nvm。

`KillMode=mixed`：先只给 Go 进程 `SIGTERM`，让它自己取消 `codex` 子进程；超时再 SIGKILL 整个 cgroup。`TimeoutStopSec` 略大于进程内 20s 关停窗口。

### 6. 启用

登出后仍要跑，先开 linger（只需一次）：

```bash
loginctl enable-linger "$USER"
```

然后：

```bash
systemctl --user daemon-reload
systemctl --user enable --now codeck
```

## 验证

```bash
systemctl --user is-active codeck
curl --noproxy '*' http://127.0.0.1:3000/api/health
curl --noproxy '*' http://127.0.0.1:3000/api/account
journalctl --user -u codeck -n 50 --no-pager
```

健康检查 `ok: true`，`db_path` 指向安装目录而不是仓库。`/api/account` 应在数秒内 `ok: true`，带 `rate_limits` 和 `usage`。日志应有：

- `codex CLI detected`
- `codex app-server initialized`
- `web UI and API listening`

浏览器打开 `http://127.0.0.1:3000/`。

## 日常

```bash
systemctl --user status codeck
journalctl --user -u codeck -f
systemctl --user restart codeck
systemctl --user stop codeck
```

改 `codeck.env` / `proxy.env` / unit 之后：`daemon-reload`（仅 unit）再 `restart`。

更新二进制：`task build:release`，覆盖 `bin/codeck`，`systemctl --user restart codeck`。数据目录不动。

开发占用同一端口时先 `stop`，或给开发换 `ADDR`。两边可以同时跑，只要 **端口和 `DATA_DIR` 都不同**。

生产（systemd）默认开着调度；开发只看页面时不要抢跑定时任务：

```bash
# 二选一：启动参数 --no-scheduler，或环境变量 SCHEDULER_ENABLED=false
ADDR=127.0.0.1:8080 go run . --no-scheduler
```

`task dev` 的后端已经默认 `CODECK_SCHEDULER_ENABLED=false`（到期不自动跑，手动「立即执行」仍可用）。
确认为关可用 `curl --noproxy '*' http://127.0.0.1:8080/api/health` 看 `scheduler_enabled: false`。
注意开发库（默认仓库 `./data`）和生产库务必分开，否则两进程同开一份 `codeck.db` 会报 SQLITE_BUSY。

## 故障

| 现象 | 原因 |
|---|---|
| 健康检查失败 / 打不开页面 | unit 没起来，或 `ADDR` 与访问地址不一致 |
| `Codex App Server 未启动` | `CODEX_BIN` / `PATH` 找不到 `codex` 或 `node` |
| `account/read` 成功，rateLimits / usage 超时 | unit 没有代理环境；补 `proxy.env` 后 restart |
| `failed to refresh available models: timeout waiting for child process` | 同上，App Server 子进程出网失败 |
| 开发 `task run` 报 SQLITE_BUSY 或 WAL 异常 | 两个进程打开了同一份 `codeck.db` |
| 重启后服务没了 | 未 `enable`，或未 `loginctl enable-linger` |
| Profile 聊天失败但账号页正常 | 查该 Profile 的 `CODEX_HOME` 与 `auth.json` symlink |

对照：同一二进制在登录 shell 里 `codeck -appserver-probe` 能打出三段 JSON，unit 里 `/api/account` 却超时 → 几乎总是代理没进 systemd 环境。
