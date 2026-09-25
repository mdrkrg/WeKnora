# LTI 多链路 handoff 设计

> 状态：已实现（对应实现见 `internal/lti/types.go`、`internal/lti/handlers.go`、`migrations/versioned/000113_lti_registration_handoff_url.*` 与 `migrations/sqlite/000033_lti_registration_handoff_url.*`；v0.7.2 反向移植上对应 `900002_lti_registration_handoff_url`）
> 关联：单链路既有设计见部署方文档 `sjtu-knowledge/docs/auth/lti-handoff-*.md` 与 [`embed-secure-mode.md`](./embed-secure-mode.md)

本文说明如何让**同一个 WeKnora 工具实例**同时服务两类 Canvas 入口，并把身份按各自需要交接到不同前端：

- **课程导航（course navigation）** → 外部 web 应用，由其完成课程→租户路由，进入替代前端；
- **个人主页导航（user navigation）** → WeKnora 原生前端（浏览器自托管 handoff）。

## 1. 背景与问题

此前一个 WeKnora 实例只有一个全局 handoff 落点 `LTI_HANDOFF_URL`，且它与内置浏览器自托管 handoff（`GET /lti/handoff`，`LTI_SELF_HANDOFF_ENABLE`）在实例级互斥：

- 方案 A（S2S 兑票）：`LTI_HANDOFF_URL=<外部 web>`，WeKnora 签发 ticket 后 302 到 web，由 web 用共享密钥 `POST /lti/tickets/redeem` 换票并做课程→租户路由。**WeKnora 不感知 course/租户映射。**
- 方案 B（自托管）：`LTI_HANDOFF_URL=<weknora>/lti/handoff` + `LTI_SELF_HANDOFF_ENABLE=true`，浏览器侧消费 ticket、签发默认租户 JWT，经 URL hash 落地原生 SPA。此路径专为「把官方 SPA 嵌进平台 iframe」设计，会把 SPA 根的 `X-Frame-Options` 换成 `CSP frame-ancestors`。

若要让「课程导航走 web、个人主页走原生 SPA」共存，唯一全局落点无法表达两种意图。目标是在**不改动课程链路**的前提下，按注册（registration / Developer Key）选择交接策略。

## 2. 术语

| 词 | 含义 |
|---|---|
| registration | `lti_registrations` 一行，按 `issuer + client_id` 唯一，对应 Canvas 侧一个 Developer Key |
| ticket | launch 成功后 WeKnora 签发的一次性短时凭据（默认 120s），承载 `user_id`、`context_id`、`roles` |
| 外部 web handoff | `LTI_HANDOFF_URL` 指向外部应用，S2S `redeem` 换票，需要 `LTI_HANDOFF_SHARED_SECRET` |
| 自托管 handoff | `GET /lti/handoff`，浏览器侧消费 ticket，签发默认租户 JWT，302 `/#lti_result=<base64url(JSON)>` |
| SPA | WeKnora 原生 Vue 前端，token 存 localStorage，`/` 默认重定向 `/platform/knowledge-bases` |

## 3. 现状与约束

1. **launch 落点全局唯一**。`launch` 成功后统一跳 `LTI_HANDOFF_URL`，无注册级差异。→ 本设计引入注册级覆盖。
2. **课程→租户映射只在外部 web**。WeKnora 只透传 `context_id`（不透明串），不做 grants 查询；课程路由属部署方语义。
3. **自托管 handoff 已存在**且只签发**默认租户** token（`IssueDefault`）。它对 `context_id` 不解释、不做课程路由。
4. **SPA 认证在 localStorage（非 cookie）**，对第三方 iframe 友好；嵌入前提是前端容器把 SPA 根 frame 策略切为 `frame-ancestors`，该切换由 `LTI_ENABLE && LTI_SELF_HANDOFF_ENABLE` 触发（`frontend/docker-entrypoint.sh`）。
5. **角色仅透传**。`roles` 原样进 ticket / redeem 响应，WeKnora 内部不据此授权。

## 4. 决策

1. **注册级 handoff 覆盖**：`lti_registrations` 增可空列 `handoff_url`；launch 取 `reg.handoff_url`，为空回退全局 `LTI_HANDOFF_URL`。一次实例即可为不同 placement 选择不同交接策略。
2. **双 Developer Key**：
   - key1（现有）：`course_navigation`，注册行 `handoff_url` = 外部 web 兑票入口；
   - key2（新增）：`user_navigation`，注册行 `handoff_url` = 空 → 回退全局 = WeKnora 内置 `/lti/handoff`。
3. **全局默认取自托管**：`LTI_HANDOFF_URL=https://<weknora>/lti/handoff`、`LTI_SELF_HANDOFF_ENABLE=true`，使 key2 走原生 SPA，并让 SPA 可被 Canvas iframe 嵌入。key1 由注册级覆盖回外部 web。
4. **课程链路零改动**：key1 继续走 web handoff，web 侧路由/换票/会话逻辑不变。

## 5. 详细设计

### 5.1 数据模型与迁移

`Registration` 增列（`internal/lti/types.go`）：

```go
// HandoffURL optionally overrides the global LTI_HANDOFF_URL for launches
// resolved against this registration. Empty means "use the global default".
HandoffURL string `gorm:"type:text" json:"handoff_url"`
```

迁移 `000113_lti_registration_handoff_url`（versioned）与 `000033_lti_registration_handoff_url`（sqlite），各一对，可空默认空，`down` 删列：

```sql
-- versioned (PostgreSQL)
ALTER TABLE lti_registrations ADD COLUMN IF NOT EXISTS handoff_url TEXT NOT NULL DEFAULT '';
-- sqlite
ALTER TABLE lti_registrations ADD COLUMN handoff_url TEXT NOT NULL DEFAULT '';
```

> LTI 核心表的迁移编号随分支基线而变：main 上为 `000112_lti_core` / `000032_lti_core`，v0.7.2 反向移植上为 `900001_lti_core`，故 handoff_url 在 v0.7.2 上为 `900002`。LTI 表只存在于 versioned 与 sqlite 两个迁移路径；mysql init 脚本不含 LTI 表，故 handoff_url 同样只落这两处。

### 5.2 launch 落点解析

`internal/lti/handlers.go`：

```go
func (h *Handler) handoffTarget(reg *Registration) string {
    if reg != nil {
        if override := strings.TrimSpace(reg.HandoffURL); override != "" {
            return override
        }
    }
    if h.cfg == nil {
        return ""
    }
    return strings.TrimSpace(h.cfg.HandoffURL)
}
```

launch 在验签、身份解析、签发 ticket 之后：

```go
handoff := h.handoffTarget(reg)
if handoff == "" {
    h.renderFailure(c, http.StatusInternalServerError, "配置错误", "未配置 handoff 地址。")
    return
}
c.Redirect(http.StatusFound, handoff+"?ticket="+url.QueryEscape(raw))
```

### 5.3 两条链路

```
课程导航（key1，course_navigation，context_id 非空）
  Canvas → POST /lti/launch → 验签 / 身份解析 → ticket
    → reg.handoff_url（外部 web）→ 302 <web>/api/auth/lti/handoff?ticket=…
    → web S2S redeem → 只读 grants 按 context_id 路由课程租户 → switch-tenant
    → 建 web 会话 → 进入替代前端（课程工作区）

个人主页（key2，user_navigation，无 context）
  Canvas → POST /lti/launch → 验签 / 身份解析 → ticket
    → reg.handoff_url 空 → 全局 LTI_HANDOFF_URL = /lti/handoff
    → 消费 ticket → IssueDefault 签发默认租户 JWT 对
    → 302 /#lti_result=<base64url(JSON)> → SPA 读 hash 写 localStorage → /platform/knowledge-bases
```

两条链路身份权威一致（`directory_claim` = `sis_user_id` 的确定性解析），互不影响。

### 5.4 自托管 handoff 行为

`GET /lti/handoff`（`internal/lti/handlers.go` `Handoff`）：

- 仅在 `LTI_ENABLE && LTI_SELF_HANDOFF_ENABLE` 时可用，否则 404；
- 入参单查询参数 `ticket`；无 ticket → `/#lti_error=missing_ticket`；
- 消费 ticket（单次）：已消费 → `invalid_ticket`；不存在/过期 → `invalid_ticket`；服务错误 → `server_error`；
- 签发**默认租户** token（`IssueDefault`，含 tenantless 自愈）；无可用工作区 → `no_workspace`，非成员 → `not_a_member`；
- 成功：`302 /#lti_result=<base64url(JSON)>`，JSON 形如 `{success, token, refresh_token, user_id, context_id}`；
- 失败会 best-effort 退票（`Restore`），浏览器仍持有 handoff URL 时可重试。

SPA 侧（`frontend/src/App.vue` `handleGlobalOIDCCallback`）读 `lti_result`/`lti_error`，成功后写 localStorage 并路由到 `/platform/knowledge-bases`（或 `/onboarding/workspace`）。

### 5.5 SPA 可嵌入

前端容器入口脚本按全局开关渲染 SPA 根 frame 策略：

```
LTI_ENABLE=true && LTI_SELF_HANDOFF_ENABLE=true
  → SPA 根：X-Frame-Options 置空，Content-Security-Policy: frame-ancestors ${LTI_FRAME_ANCESTORS}
默认 → X-Frame-Options: SAMEORIGIN，无 CSP 头
```

`LTI_FRAME_ANCESTORS` 同时用于 `/lti/*` 与 SPA 根，须包含 Canvas origin。前置反代不得输出 `X-Frame-Options: DENY`。

## 6. Canvas 配置

| Key | placement | 注册行 | 交接目标 |
|---|---|---|---|
| key1（现有） | `course_navigation` | `client_id=key1`，`handoff_url=https://<web>/api/auth/lti/handoff` | 外部 web（课程路由） |
| key2（新增） | `user_navigation` | `client_id=key2`，`handoff_url=''` | 全局默认 → 内置 `/lti/handoff` |

两把 Key 共用同一工具公钥（`GET /.well-known/jwks.json`）、同一 `oidc_initiation_url`（`/lti/login_initiations`）与同一 `redirect_uris`（`/lti/launch`）；custom 参数注入 `sis_user_id=$Canvas.user.sisSourceId`。注册行示例：

```sql
INSERT INTO lti_registrations
  (issuer, client_id, deployment_ids, auth_endpoint, jwks_uri, directory_claim, handoff_url, enabled)
VALUES
  ('<canvas_issuer>', '<key2_client_id>', '<["<deployment_id>"]或留空>',
   'https://<canvas_url>/api/lti/authorize_redirect',
   'https://<canvas_url>/api/lti/security/jwks', 'sis_user_id', '', TRUE);

UPDATE lti_registrations
   SET handoff_url = 'https://<web>/api/auth/lti/handoff'
 WHERE issuer = '<canvas_issuer>' AND client_id = '<key1_client_id>';
```

## 7. 环境变量

| 变量 | 值 | 说明 |
|---|---|---|
| `LTI_ENABLE` | `true` | 总开关 |
| `LTI_SELF_HANDOFF_ENABLE` | `true` | 开启 `/lti/handoff`，并使 SPA 可嵌入 |
| `LTI_HANDOFF_URL` | `https://<weknora>/lti/handoff` | 全局默认，服务 key2 |
| `LTI_HANDOFF_SHARED_SECRET` | 与 web 同值 | 仅 key1 的外部 web redeem 使用 |
| `LTI_FRAME_ANCESTORS` | `https://<canvas>` | `/lti/*` 与 SPA 根 |
| `LTI_LAUNCH_URL` | 可选 | 反代后显式配置 |
| `LTI_PLACEHOLDER_DOMAIN` | `users.lti.invalid` | 合成邮箱保留域，与外部 web 侧同值 |

外部 web 侧配置（`LTI_HANDOFF_SHARED_SECRET`、`PROVISIONING_DATABASE_URL`）不变。

## 8. 安全与失败语义

- **ticket 单次 + 120s + 兑换即清参**：自托管不使用共享密钥，凭据即 ticket，强度等同 OIDC code；token 经 URL **hash** 传递，不进服务端日志与 Referrer（`Referrer-Policy` 已设）。
- **注册级 handoff 是运维配置**（DB 播种），非 launch 可控字段，不构成注入面。
- **成员/工作区判定仍在 WeKnora**：自托管 `IssueDefault` 校验默认工作区可用性；课程链路的定向重签由 web 触发、WeKnora 校验成员资格。
- **角色不用于授权**：`roles` 仅透传；原生 SPA 的可见性由 WeKnora RBAC 决定。
- **失败落点**：WeKnora 侧 launch 失败渲染自身中文失败页；自托管失败 `/#lti_error=<code>` 由 SPA 提示（`missing_ticket`/`invalid_ticket`/`server_error`/`no_workspace`/`not_a_member`）；外部 web 链路错误码由 web 侧收敛。
- **审计**：`lti.ticket_issued` / `lti.ticket_redeemed` / `lti.ticket_redeem_denied`（拒绝带 reason，重放即 `consumed`）。

## 9. 原生前端能力边界（跨租户）

明确原生 SPA 与聊天在租户上的边界，避免「一页看全所有已加入知识库」的预期错配：

- 每个请求只携带一个 `X-Tenant-ID`（`frontend/src/utils/request.ts`），会话行 `Session.TenantID` 单一（`internal/types/session.go`），模型/rerank 配置按所选租户解析。
- 一次检索**可以**包含来源租户不同的 KB，但仅限被**显式共享**进当前租户的 KB（组织/共享空间或共享智能体）：`buildSearchTargets` 按每个 KB 的真实 `TenantID` 解析并写入 `SearchTargets`（`internal/application/service/session_knowledge_qa.go`）。
- **共享智能体**把检索租户切换为智能体来源租户，并刻意排除调用方自己的共享 KB，避免跨组织泄漏。
- 不存在「自动聚合用户全部 membership」的能力：多课程用户需逐个切换工作区；若要让多课进同一对话，杠杆是供给策略（把课程 KB 共享进共同组织），与 handoff 无关。

## 10. 非目标

- 不引入跨租户/跨工作区自动聚合页面或接口。
- 不改动外部 web 的课程路由、会话与失败页语义。
- 不改变身份解析（仍为 `sis_user_id` 确定性查找、零建号）。
- 不改变 SPA 登录方式（localStorage + JWT）。

## 11. 验证

- 单元/流程测试（`internal/lti`）：`handoffTarget` 注册覆盖与回退全局；launch→自托管 handoff 全链路；ticket 重放拒绝（`flow_test.go`）。
- 迁移：`000113`/`000033` 在 versioned 与 sqlite 可上可下（v0.7.2 上为 `900002`）。
- 部署方（sjtu-knowledge）e2e：课程链路 T0–T3 不受影响；原生 SPA 浏览器路径不在其 e2e 范围（由 WeKnora 侧覆盖）。
- 手工核对：个人主页入口落 `/platform/knowledge-bases`；课程导航仍落对应课程工作区。

## 12. 回滚

- 配置级：将 key1 注册行 `handoff_url` 置空并保持 `LTI_SELF_HANDOFF_ENABLE=false`，即回到单链路；外部 web 侧清 `LTI_HANDOFF_SHARED_SECRET` 关闭课程入口。
- 结构级：执行 handoff_url 迁移 `down` 删除 `handoff_url` 列（可选，向后兼容的额外列不影响运行）。

## 13. 开放问题

1. **Canvas placement 受众**：`user_navigation` 对所有用户可见，教师会同时看到供给门户与知识库入口；是否需要用工具 `visibility`/账号范围收敛到学生。
2. **落地工作区**：自托管只签发默认租户，多课程学生进入后落在第一个课程，需要手动切换；是否符合预期。
3. **迁移路径**：是否需要为 mysql 补充 LTI 表与其 handoff_url 迁移（当前 LTI DDL 仅 versioned/sqlite）。
4. **上游化**：注册级 `handoff_url` 是否随 LTI 特性整体提交上游；若上游不收，列入 fork rebase 维护项。
