# Redis 刷新会话迁移到 PostgreSQL

本文说明 XIASS 的显式手动运维工具：把当前 Redis authority 中仍有效的网页登录刷新会话迁移到已经发布的 PostgreSQL 持久会话 schema，并将会话 authority 单向切换为 PostgreSQL。

这不是普通升级步骤，也不是自动 HA、Redis 切换或数据库升主工具。未经完整盘点和维护窗口验收，不要执行。

## 关键边界

- 默认启动、`-setup`、网页配对、在线更新和多节点启用都不会迁移会话，也不会修改 Redis ACL。
- 只有同时显式提供 `-migrate-refresh-sessions` 与 `-offline-maintenance` 时，server 二进制才进入迁移命令；该分支不会启动正常 HTTP server、执行 setup、启动隧道或自动运行数据库 migration。
- 工具只从清单中固定的原始 Redis primary 读取会话。它不会把 replica 中缺失于 primary 的 token 补回，不会合并多份快照，也不会把 Sentinel 地址当作数据来源。
- 成功后 PostgreSQL 是唯一刷新会话 authority。应用不会在 PostgreSQL 查询失败、token 缺失或数据库回退时重新读取 Redis。
- 该迁移保留可验证的 token 身份、绑定、family、原始绝对到期时间和七天上限；已撤销或已从 primary 消失的会话不会复活。
- 工具不提供 PostgreSQL 同步复制、旧主隔离、Redis Sentinel 部署、自动选主、Witness 仲裁、DNS 切换或整机容灾。它也不能证明一份外部恢复的 Redis 快照从未丢失过历史撤销。

## 两种维护模式

### v1：离线认证迁移

`version: 1` 是严格离线模式。执行前必须确认登录、刷新、退出撤销请求和后台会话操作均已排空，并停止所有会访问该 Redis 会话 authority 的应用进程。仅从公网入口摘除但仍保持应用运行不满足要求。

迁移会禁用清单中旧 Redis 用户的全部权限。已经建立的旧连接也会失去权限。如果清单带有 `runtime`，工具可在 PostgreSQL authority 激活后创建新的受限应用/复制身份和私密环境输出；这不改变 v1 必须离线执行的要求。

### v2：认证入口阻断，保留受限推理 runtime

`version: 2` 用于不能同时停止所有推理流量的维护窗口。它要求：

1. 所有相关 XIASS 节点已升级到理解 PostgreSQL authority 和迁移 readiness 的同一版本。
2. 节点在 Redis 配置下显式启用默认关闭的 `JWT_REFRESH_TOKEN_MIGRATION_READINESS=true`，并验证 `/readyz`。
3. 外部入口已经阻断登录、刷新 token、退出和其他撤销入口，并确认这些请求及后台会话操作已排空。
4. 清单显式设置 `session_fence.preserve_runtime_access=true` 和 `auth_endpoints_blocked_and_drained=true`，同时提供完整 `runtime`。

`JWT_REFRESH_TOKEN_MIGRATION_READINESS=true` 只让已经启动的 provider 在完整 witness 提交后单向观察 PostgreSQL authority；它不会导入会话、修改 Redis ACL 或在后台启动迁移。`auth_endpoints_blocked_and_drained=true` 同样只是操作员对外部流量隔离结果的明确声明，工具不会替入口执行阻断或排空。

v2 会把每个已盘点旧 Redis 用户限制到 XIASS 固定的非认证 key/command/channel 白名单。该白名单用于已知的推理缓存、计费缓存、并发、队列、调度、监控和失效通知；它明确排除三个刷新会话命名空间、ACL、CONFIG、FLUSH 和复制管理命令。未知 Redis keyspace 或无法安全表达的自定义布局会在 fence 前拒绝。

v2 只能减少推理侧停机范围。认证入口在维护期间仍不可用，刚好处于轮换中的请求仍可能失败，因此不能宣称完全无中断、完整 HA 或生产零丢失。

## 执行前条件

执行前逐项确认：

1. 正常升级已安装 XIASS 发布的持久会话、authority 和 transition schema。不要新建、编辑、重排或重复使用已发布 migration。
2. PostgreSQL 是可写 primary，不处于 recovery/read-only；authority 当前仍为 `redis`，PostgreSQL 持久会话表没有另一套签发或撤销历史。该工具不是两套 authority 的合并器。
3. 已确认 Redis 原始 primary 的来源可信。不要使用刚提升的 replica、不可信备份、恢复后的旧 RDB 或临时发现的 Sentinel 当前主节点冒充原始来源。
4. primary 和最多八个直接 replica 都可通过固定 `redis://` 或 `rediss://` TCP 地址访问。清单覆盖所有节点、ACL 用户、支持的 module、run ID、复制 ID 和通告地址；不能遗漏离线副本。
5. 每个节点配置了可写、可持久保存的 ACL 文件，且 `ACL SAVE` 可成功。带 replica 的 runtime 恢复还要求每个潜在角色都有可写的持久 `redis.conf`，以便 `CONFIG REWRITE` 保存替换后的复制凭据。
6. 已备份 PostgreSQL、Redis、当前 `.env`、签名密钥和运行配置。备份本身不授权回滚 authority，也不能用旧 Redis 快照恢复已确认撤销的会话。
7. 已选择 v1 或 v2，并在所有应用节点和外部入口实施对应维护边界。命令行的 `-offline-maintenance` 只是显式确认，不会替运维系统停止、阻断或排空请求。

任一来源、拓扑、ACL、schema 或流量隔离条件无法证明时，应停止迁移，不要绕过安全检查。

## 私密输入

清单、恢复密钥和 runtime 密码必须存放在仅操作员可读的普通文件中，权限为 `0400` 或 `0600`。不要使用符号链接、共享目录、聊天记录、工单附件或 shell 历史保存真实 URL、token、数据库密码或 Redis 密码。

恢复密钥和每个 runtime 密码文件均包含独立的 32 字节随机值，以十六进制编码。可以在受保护的运维环境中生成，例如：

```sh
umask 077
openssl rand -hex 32 > /private/xiass-refresh/recovery.hex
openssl rand -hex 32 > /private/xiass-refresh/app.hex
```

有 replica 或 backup 身份时，为它们分别生成不同文件。不得复用恢复密钥、现有 Redis 密码、应用密码、复制密码或 backup 密码。本文示例中的尖括号全部是占位符，不是可直接使用的凭据。

## v1 清单示例

```json
{
  "version": 1,
  "database_url": "postgresql://<user>:<password>@<writable-primary>:5432/<database>?sslmode=<verified-mode>",
  "recovery_secret_file": "/private/xiass-refresh/recovery.hex",
  "primary": {
    "url": "rediss://<acl-user>:<password>@<direct-primary-host>:6379/0",
    "run_id": "<40-hex-run-id>",
    "acl_users": ["<exact-existing-user>"],
    "modules": []
  },
  "primary_replication_id": "<40-hex-master-replid>",
  "primary_address": "<primary-host:port-seen-by-replicas>",
  "replicas": []
}
```

即使是无 replica 的 standalone，也要填写已检查的 `primary_replication_id` 和有效 `primary_address`。有 replica 时，每项还要提供其固定直连 URL、run ID、完整 ACL/module 清单，以及 primary `INFO replication` 中对应的 `replica_address`：

```json
{
  "url": "rediss://<acl-user>:<password>@<direct-replica-host>:6379/0",
  "run_id": "<40-hex-run-id>",
  "replica_address": "<advertised-replica-host:port>",
  "acl_users": ["<exact-existing-user>"],
  "modules": []
}
```

直连 URL 与通告地址可以因 NAT 或容器网络不同，但都必须在执行前固定并逐项核实。module 清单必须是实际名称和版本；工具只接受已审核的 `ReJSON`、`search`、`timeseries`、`bf` 和 `vectorset`，未知 module 会拒绝迁移。

## v2 runtime 扩展示例

v2 使用与 v1 相同的数据库和完整 Redis 拓扑字段，并额外加入：

```json
{
  "version": 2,
  "session_fence": {
    "preserve_runtime_access": true,
    "auth_endpoints_blocked_and_drained": true
  },
  "runtime": {
    "app_password_file": "/private/xiass-refresh/app.hex",
    "replica_password_file": "/private/xiass-refresh/replica.hex",
    "backup_password_file": "/private/xiass-refresh/backup.hex",
    "backup_credentials_file": "/private/xiass-refresh/backup-credentials.json",
    "environment_file": "/private/xiass-refresh/postgres-authority.env",
    "dashboard_prefix": "<current-dashboard-prefix>",
    "queue_ready_key": "<current-ready-key>",
    "queue_delayed_key": "<current-delayed-key>",
    "queue_active_key": "<current-active-key>",
    "inflight_prefix": "<current-inflight-prefix>",
    "lock_prefix": "<current-lock-prefix>",
    "alert_lock_key": "<current-alert-lock-key>"
  }
}
```

- 无 replica 时省略 `replica_password_file`。
- backup 身份是可选项，但 `backup_password_file` 与 `backup_credentials_file` 必须同时提供或同时省略。
- 使用默认 Redis key 布局时可省略对应 key 字段；使用自定义值时必须与当前应用配置完全一致。
- `environment_file` 和可选的 `backup_credentials_file` 必须是新的绝对路径，不能是安装目录中的 `.env`，也不能与任何输入文件重合。
- 工具以 `0600` 原子创建输出，绝不覆盖内容不同的既有文件。输出包含实际凭据，禁止打印到聊天、工单或普通日志。
- v1 若不提供 `runtime`，迁移完成后旧 Redis 用户仍保持禁用；在另行安全配置受限应用凭据前，不得直接重启应用。

## 执行命令

从受保护的运维环境运行同版本 XIASS server 二进制：

```sh
umask 077
/app/xiass-api \
  -migrate-refresh-sessions /private/xiass-refresh/manifest.json \
  -offline-maintenance \
  > /private/xiass-refresh/result.json
```

成功结果只包含 transition ID、导入/过期数量、激活时间和 snapshot digest，不包含原始 token 或连接密码。错误会隐藏连接详情，但仍应把命令输出视为私密运维记录。

迁移按以下顺序执行：

1. 获取 PostgreSQL 独占 advisory lock，核实 Redis authority 和空的 PostgreSQL 会话/撤销历史。
2. 固定并复核每个 Redis 进程、拓扑、ACL、module、keyspace 和持久化能力。
3. 创建仅由本次操作持有的恢复身份，并持久化后再限制旧身份。group 模式先处理 replica，最后处理原 primary。
4. 从原 primary 重新读取有界会话快照；不从 replica 补齐缺失 token。
5. 在一次要求同步提交的 PostgreSQL 事务内导入会话、写入不可变 witness，并把 authority 从 `redis` 切换为 `postgres`。
6. 若提供 `runtime`，复核 immutable fence 后创建受限应用、复制和可选 backup 身份，持久化复制凭据，并原子写入私密输出文件。

## 失败与重试

安全恢复原则是“保持 fence，使用相同输入重试”，不是回退权限：

- fence 前的清单、拓扑、schema、ACL 持久化或 keyspace 预检失败，不会开始禁用旧用户。修正前置条件后仍应重新核实来源。
- 一旦任一 Redis ACL fence 已开始，后续失败不会自动恢复旧用户权限。节点可能处于部分已限制状态；继续保持 v1 应用停止，或保持 v2 认证入口阻断和排空。
- 必须保留并复用完全相同的 manifest、recovery 文件、runtime 密码文件、固定 endpoint、run ID 和输出路径。不得重新生成密钥、换到另一副本或使用跳过验证的替代参数。
- 如果 PostgreSQL authority 已成功激活而 runtime 恢复或输出失败，会话迁移不会被撤销。使用相同输入重试以完成并取得 witness/output；不要把配置改回 Redis。
- 如果结果输出失败或提交确认不明确，仍使用相同输入重试。已完成的重试只读取持久 witness，不会重复导入 token。
- 丢失 recovery 文件、Redis 进程重启导致 run ID 改变、ACL 文件被外部替换，或原始来源无法继续证明时，应停止并进行单独授权的人工恢复；不得绕过 fence。

工具不会擦除当前 Redis 会话 key，但 ACL fence 会阻止旧应用继续读写这些命名空间。不要用恢复旧 ACL、`FLUSHDB`、删除 PostgreSQL transition 记录或恢复异步副本来“撤销”迁移。

## 成功后的切换与验收

1. 私下保存 `result.json`、manifest、recovery 文件和新 runtime 凭据；备份 PostgreSQL authority、transition、会话、签发与撤销数据。
2. v1：将所有节点明确配置为 `JWT_REFRESH_TOKEN_STORE=postgres`，接入新受限 Redis 应用凭据并在隔离入口启动。
3. v2：已启用 readiness 的现有 provider 在观察到完整 PostgreSQL witness 后单向选择 PostgreSQL。随后逐台部署 `JWT_REFRESH_TOKEN_STORE=postgres`、`JWT_REFRESH_TOKEN_MIGRATION_READINESS=false` 和新受限 Redis 凭据；不要求同时重启全部推理节点。
4. 在解除认证入口限制前，用隔离合成用户验证登录、刷新轮换、退出撤销、用户级撤销、七天到期、绑定校验和 `/readyz`；同时验证推理缓存、并发、队列、调度、监控和 Team 邮箱等实际使用的 Redis 功能。
5. 验证所有节点使用同一 PostgreSQL authority、同一 JWT/TOTP 等应用密钥和兼容版本。任何节点配置为 Redis authority 都应拒绝启动或拒绝会话操作，而不是成为回退路径。
6. 验收通过后才解除登录、刷新和撤销入口限制。保留旧 Redis 会话数据和 fence 证据，按另行批准的保留策略处理，不要在迁移窗口内删除。

## 不可宣称的保证

即使工具成功，也不能据此宣称：

- 登录、刷新和退出在迁移期间完全无中断。
- PostgreSQL 或 Redis 已具备自动 HA、同步零丢失、旧主 fencing 或自动升主能力。
- 任意 restored Redis snapshot、遗漏副本或外部控制器都已被证明安全。
- 已发送到上游的模型请求可以跨应用透明重放。
- 可以回滚到 Redis authority、旧二进制或旧数据库快照而不复活已撤销会话。

本工具解决的是一次受控、可审计、fail-closed 的刷新会话 authority 迁移。状态层复制、故障接管、备份恢复和公网入口可用性仍需独立设计、测试和批准。
