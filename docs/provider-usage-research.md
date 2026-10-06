# DeepSeek 与 OpenRouter 用量、余额接入调研

调研日期：2026-10-06。依据公开官方文档；未使用真实凭据调用接口。以下产品建议是基于接口能力的设计判断。

## 能力与认证

| 平台与接口 | 凭据 | 可读取的数据 | 范围 |
| --- | --- | --- | --- |
| DeepSeek `GET https://api.deepseek.com/user/balance` | 普通 API key，Bearer | 可用状态、总余额、未过期赠金、充值余额、币种 | 账户当前余额 |
| OpenRouter `GET /api/v1/key` | 当前 API key，Bearer | 累计及日/周/月花费、额度、剩余额度、额度重置周期、BYOK 用量 | 当前 key |
| OpenRouter `GET /api/v1/credits` | Management API key，Bearer | `total_credits`、`total_usage` | 账户 credits |
| OpenRouter `GET /api/v1/activity` | Management API key，Bearer | 日期、模型、供应商、请求数、token、花费、BYOK 推理用量 | 最近 30 个已完成 UTC 日 |
| OpenRouter `POST /api/v1/analytics/query` | Management API key，Bearer | 指定指标、维度、过滤器、时间范围的统计 | 公开页面未明确数据保留期限 |

来源：[DeepSeek 查询余额](https://api-docs.deepseek.com/zh-cn/api/get-user-balance/)、[OpenRouter 当前 key](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key)、[账户 credits](https://openrouter.ai/docs/api/api-reference/credits/get-remaining-credits)、[activity](https://openrouter.ai/docs/api/api-reference/analytics/get-user-activity-grouped-by-endpoint)、[analytics query](https://openrouter.ai/docs/api/api-reference/analytics/query-analytics-data)。

## DeepSeek：第一期适合提供账户余额

余额的 `balance_infos` 是数组，`currency` 为 CNY 或 USD；金额是字符串。`total_balance` 已包含赠金与充值余额，应直接展示总额并允许展开组成，不能将三项相加。[官方余额字段](https://api-docs.deepseek.com/zh-cn/api/get-user-balance/)

本次对公开 API 文档与 FAQ 的检索未找到账户历史账单、日/月花费或按 key 汇总的公开查询接口。这是调研范围内的发现，不代表平台不存在其他接口。推理响应有 token `usage`，只能覆盖客户端实际记录的请求；不能因此宣称已读取整个账户花费。[官方 FAQ](https://api-docs.deepseek.com/faq/)、[Responses API usage](https://api-docs.deepseek.com/api/create-response/)

## OpenRouter：区分账户余额与 key 预算

`/key` 的 `limit_remaining` 是当前 key 的剩余预算。应显示“剩余预算”，不能标为“账户余额”；未配置额度时应显示“未设上限”，不能将缺失值写成 0。账户余额可根据 `/credits` 的 `total_credits - total_usage` 计算。当前官方文档明确 `/credits` 需要 Management key，普通 key 方案不应承诺账户余额。[当前 key](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key)、[账户 credits](https://openrouter.ai/docs/api/api-reference/credits/get-remaining-credits)

额度支持 daily、weekly、monthly 或无重置；重置采用 UTC，周一至周日为一周。界面应注明平台统计时区，避免与本机时区的日统计混用。[key 额度规则](https://openrouter.ai/docs/api/api-reference/api-keys/create-keys)

`/activity` 支持单日、key hash、组织成员与 workspace 过滤；默认跨 workspace 汇总，并且不含今天这个未完成 UTC 日。实现历史同步时可按日覆盖写入防止重复累加。新的 `/analytics/query` 支持更灵活查询，返回截断信息，默认最大行数为 1000；保留期限和更多指标能力需要在实际接入前确认，不应把 `/activity` 的 30 日限制推广到所有统计 API。[activity 范围](https://openrouter.ai/docs/api/api-reference/analytics/get-user-activity-grouped-by-endpoint)、[analytics query](https://openrouter.ai/docs/api/api-reference/analytics/query-analytics-data)

Management key 可以执行管理操作，也不能调用推理接口。建议把它作为用户主动配置的增强能力，独立于推理 key 存储；第一期普通 key 已能提供 key 用量与预算。[Management key 官方说明](https://openrouter.ai/docs/guides/overview/auth/management-api-keys)

BYOK 使用上游账户承担推理成本，OpenRouter 另有基于等价推理成本的费用和计划相关免费额度。`/key` 返回独立 `byok_usage*` 字段，并提供 `include_byok_in_limit`。保留这些口径，不将 BYOK 等价成本直接当作 OpenRouter 账户扣款或跨平台相加，以免与上游统计重复计费。[BYOK 官方规则](https://openrouter.ai/docs/guides/overview/auth/byok)、[当前 key 字段](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-api-key)

## Codeck 已确认的首期范围

首期仅配置平台 Key 并查询、展示当前账户余额，不包含用量统计、历史、档案、对话或任务集成。每条余额配置独立展示原币，不跨配置合计；只持久化最后成功结果，不把余额变化推断为真实花费。

DeepSeek 通过普通 API key 查询。OpenRouter 账户余额通过 `/credits` 查询，必须配置 Management key；该凭据具有管理 API key 的账户权限，在配置页需要清楚说明。普通 key 的剩余预算不是账户余额，不能用来填充账户余额显示。[OpenRouter `/credits`](https://openrouter.ai/docs/api/api-reference/credits/get-remaining-credits)

凭据按用户确认采用 AES-GCM 加密保存在本机 SQLite；`CODECK_BALANCE_ENCRYPTION_KEY` 由 Codeck 进程环境变量注入，使用 Base64 编码的 32 字节随机值。此密钥必须和数据库备份一起保留，不能写入普通 `KEY=VALUE` 配置文件。刷新失败保留最后成功余额并显示错误；换 Key 清除旧余额。
