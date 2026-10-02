# Codeck

跑在本机的 Codex 控制台：把本机 `codex` CLI 包一层 Web UI，管隔离聊天、定时执行和额度成本。本文件只是术语表，不含实现。

## Language

### 界面结构

**侧栏导航**:
桌面端左侧固定栏，以及它在窄屏下的替身。7 个页面入口的唯一集合。
_Avoid_: 菜单、nav、左侧菜单

**顶栏**:
窄屏（≤820px）下替代侧栏导航的横向条，承载抽屉入口、当前页名与仓库链接。
_Avoid_: header、导航栏

**抽屉**:
窄屏下由顶栏按钮唤出的覆盖层，条目与侧栏导航一一对应。

### 页面 ↔ 实体

**档案**:
一份独立 Codex 配置（模型、沙箱、批准策略、CODEX_HOME、workspace）。API 与库里叫 `profile`。
_Avoid_: 配置、账号

**对话**:
与某个档案绑定的一串消息。API 里叫 `conversation`。
_Avoid_: 会话、聊天记录

**任务**:
prompt + 档案 + 计划的调度单元。API 里叫 `task`。
_Avoid_: 作业、job

**运行**:
一次任务执行或一次对话回合的落库记录。API 里叫 `task_run` / `message`。
_Avoid_: 执行记录、日志

**重置时间线**:
展示额度窗口重置时刻的页面。与 `web/src/timeline.ts` 里那根横向标尺组件不是一回事。
_Avoid_: 时间轴、timeline

### 额度

**额度窗口**:
账号配额的一段计量区间，通常 5 小时或 7 天。
_Avoid_: 限额、quota 周期

**等价成本**:
把本机 session 的 token 按 `model_prices` 折算出的 Standard API 美元数。不是 ChatGPT credits。
_Avoid_: 花费、消费
