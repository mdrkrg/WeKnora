<!-- Title: feat(lti): embed WeKnora in an LMS via LTI 1.3 — launch, session minting and built-in browser handoff -->

## Description

为 WeKnora 增加 LTI 1.3 **工具侧完整闭环**，以一条可交付的垂直切片为目标：平台 iframe 内 launch 成功后在 **WeKnora 自身**建立会话并落回原生 SPA。任何想把自己嵌入 LMS 的部署都可复用本 PR 的通用基础设施；课程→租户路由与外部前台消费刻意排除在外（随 fork 维护）。

**公开端点**（在全局 Auth 中间件之前注册，自身完成验签/校验）：
- `POST /lti/login_initiations` — OIDC 三方发起登录，生成签名 nonce/state 并 302 到平台 authorize 端点
- `POST /lti/launch` — 校验平台签名 `id_token`（`keyfunc/v3` + 平台 JWKS 缓存，仅 unknown-kid 时刷新），要求 `sub/nonce/message_type` 等强制 claim
- `GET /.well-known/jwks.json` — 工具自身签名公钥；LTI 未启用时 404，且不惰性生成密钥
- `GET /lti/handoff` — 内置浏览器换票端点（默认关闭，`LTI_SELF_HANDOFF_ENABLE` 开启）

**数据表**（迁移 `000112` / SQLite `000032`）：`lti_registrations`（含 `directory_claim` 可配置列）、`lti_tool_keys`（惰性生成工具密钥对，私钥经 `SYSTEM_AES_KEY` 加密入库）、`lti_tickets`（SHA-256 hash、`expires_at` / `consumed_at` 索引）。

**重放安全的票据生命周期**：`consumed_at` 即墓碑，消费后行保留 24h 重放检测窗口（两阶段 GC：未消费按 `expires_at`、墓碑按 `consumed_at + 24h`），过期重放仍可区分「已使用（409）」与「不存在/过期（410）」。

**审计**：launch 签发（`lti.ticket_issued`）与兑换（`lti.ticket_redeemed` / `lti.ticket_redeem_denied`，后者带 reason 重放信号）经窄接口 `AuditSink` 落库，nil 时降级 no-op；认证失败不审计（防噪声灌表）。

**身份解析（参考实现）**：`emailResolver` 以平台验签后的 `email` claim 精确匹配既有 WeKnora 账号——零建号、不读写绑定表。可被后续 PR 替换为确定性键解析。

**会话铸币**：`userService.IssueLTITokens`（默认 home tenant，tenantless 账号复用登录自愈链 `resolveFirstMembershipTenant`）+ `NewUserTokenMinter` 适配器（惰性类型断言，能力缺失时降级为请求级错误而非启动失败）。非零租户一律要求 active 成员资格，杜绝无校验的定向签发。

**内置浏览器 handoff**：消费 ticket → `IssueDefault` 签发 JWT 对 → 302 `/#lti_result=base64url(JSON)`（形状对齐 OIDC callback 的 `AuthOIDCCallbackResponse`）；失败 302 `/#lti_error=<code>`（`missing_ticket` / `invalid_ticket` / `server_error` / `no_workspace`），mint 失败 best-effort 退票（`Restore`）以便浏览器重试。

**SPA 消费**（`frontend/src/App.vue` + `router/index.ts`）：识别 `lti_result` / `lti_error`，复用既有 OIDC 落地逻辑（写 localStorage、跳 `/platform/knowledge-bases`），约 20 行。SPA 根 frame 策略在 `LTI_ENABLE && LTI_SELF_HANDOFF_ENABLE` 时由 `X-Frame-Options: SAMEORIGIN` 切换为 `CSP frame-ancestors`（`frontend/docker-entrypoint.sh`），`/lti/*` 由 `FrameAncestorsMiddleware` 覆盖。

**健壮性与安全**：
- unknown-kid 验签失败会触发 JWKS 刷新；刷新加 30s 冷却并在 fetch 层限流，避免未认证的 `/lti/launch` 被用来放大出站请求与 DB 写入
- handoff 目标用 `url.Parse` 拼接，保留运营方 URL 既有 query；成功重定向带 `Cache-Control: no-store`
- `deployment_id` claim 在注册行未配置 allowlist 时允许缺失（兼容省略该 claim 的平台）；一旦配置 allowlist，缺失即拒绝

**范围外（随 fork 维护）**：`POST /lti/tickets/redeem` S2S 共享密钥换票、`IssueForTenant` 定向签发、注册级 `handoff_url` / 多入口、课程→租户路由、确定性 SIS 解析（见下一个 PR）。

**Commits**（按顺序）：
1. `50045588d` chore(deps): add keyfunc/v3 for JWKS handling and LTI test deps
2. `71d1fe0ce` feat(lti): LTI 1.3 tool core (verification, keys, tickets, endpoints)
3. `fec1f98cc` feat(lti): audit launch ticket issuance and redemption
4. `a4618fd81` test(lti): launch/handoff/jwks error contracts and gorm store behavior
5. `25c477212` feat(lti): session token issuance for the browser handoff
6. `b058e1498` feat(lti): minimal email-match identity resolver
7. `491f1f7cc` feat(lti): built-in browser handoff endpoint
8. `c1d86f15a` feat(frontend): consume lti_result hash from self-handoff
9. `29d160ed4` test(lti): handoff error contracts
10. `02ba4dd00` refactor(lti): scope the upstream shell to the embed-self flow

> 分支 `feat/lti-embed-self`，base = 上游 `main`；另一分支 `feat/lti-deterministic-resolver` 以本分支为 base 堆叠。

## Type of Change
- [ ] 🐛 Bug fix
- [x] ✨ New feature
- [ ] 💥 Breaking change
- [ ] 📚 Documentation update
- [ ] 🎨 Refactor
- [ ] ⚡ Performance improvement
- [x] 🧪 Test
- [ ] 🔧 Configuration / Build / CI

## Related Issue
N/A（无关联 issue；承接 `canvas-lti-plan.md` 的垂直切片重排）

## Testing
- 逐 commit 验证：`go build ./...` + `go test ./internal/lti/... ./internal/config/... ./internal/container/... ./internal/router/... ./internal/database/...` 通过
- 关键服务包：`go test ./internal/application/service/` 通过（SQLite 迁移计数断言更新为 32，对应本 PR 的 `000032`）
- 前端 `npm run type-check` 通过
- `go vet ./internal/lti/...`、`gofmt`、`git diff --check` 干净
- 覆盖：state/nonce 签名、id_token 验签与错误分类、keyset 刷新 gating 与冷却、ticket 单次消费与墓碑两阶段 GC、launch/兑换审计、handoff 通道与错误契约、email resolver、config 安全默认值、`deployment_id` 缺失容忍

## Checklist
- [x] `git diff --check github/main...HEAD` passes
- [x] Changed source files are formatted
- [x] Targeted tests for the changed packages/components pass
- [ ] Diff-scoped lint passes where applicable（本会话以 `go vet` 验证；`golangci-lint` 未全仓执行）
- [x] Self-reviewed the code
- [x] Added/updated tests covering the change
- [x] Updated related documentation（`LTI_*` env 已写入 `.env.example` / docker-compose）
- [ ] Breaking changes are clearly called out in the description above（无 breaking change，本项 N/A）

## Screenshots / Recordings
N/A（无用户可见 UI 变更；handoff 经 URL hash 投递 SPA，复用既有 OIDC 落地逻辑）
