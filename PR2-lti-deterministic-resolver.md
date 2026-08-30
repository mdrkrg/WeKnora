<!-- Title: feat(lti): deterministic directory-key identity resolver (stacked on the embed-self PR) -->

## Description

在 `feat/lti-embed-self`（PR1）之上，把 launch 的身份解析从「email 匹配」升级为 WeKnora 自身的**确定性键解析参考实现**：学工号（`sis_user_id`）是跨服务唯一身份权威，email 仅是其确定性编码，账号的唯一创建方是外部 daemon sweep。本 PR 为可选第二 PR——若上游只收 PR1，PR1 的 email resolver 即可独立成立；合入本 PR 则解析策略收紧为确定性键查找。

**两分支解析**（`internal/lti/matcher.go`，替换 PR1 的 email resolver）：

```
分支一：DirectoryUID 为空（自定义 claim 未注入 / 注册行 directory_claim 未配置 / 平台用户无 SIS pseudonym）
    → ErrIdentityMisconfigured → 渲染「服务配置错误」失败页
      （面向运维：失败率突增即 claim 链路断了）

分支二：GetUserByEmail("<DirectoryUID>@<LTI_PLACEHOLDER_DOMAIN>") 精确等值查找
    命中 → 返回账号 → 签发票据
    未命中 → ErrIdentityNotFound → 渲染「名单尚未同步」失败页
    （fail-closed：不查真实邮箱、不建号、不读写绑定表）
```

- **launch 路径零账号创建**：matcher 仅依赖 `UserCatalog.GetUserByEmail`，无 `Register` 能力；账号与成员资格由 daemon sweep 预置（`ensureUser("<sis>@users.lti.invalid", ...)`），解析与名单同步天然最终一致。
- **`UserCatalog` 收窄**：移除 `Register`，只保留 `GetUserByEmail`（`interfaces.go` + `adapters.go` 适配器，惰性类型断言、`ErrUserServiceCapability` 降级）。
- **失败态区分**：`handlers.go` Launch 新增 `ErrIdentityMisconfigured` 与 `ErrIdentityNotFound` 两条失败页，与既有 `ErrIdentityDisabled` 分支并存；配置漂移与名单未同步从失败页文案即可区分。
- **配置**：`LTI_PLACEHOLDER_DOMAIN`（默认 `users.lti.invalid`，RFC 2606 保留域），daemon 建号邮箱与 launch 解析同源，两侧必须一致。
- **删除**：PR1 的 email resolver（`resolver_email.go` / `resolver_email_test.go`）被确定性解析取代，避免保留无人接线的第二套身份策略。

**测试**：
- `matcher_test.go` 钉命中 / 未命中 / misconfig / 空 value / 查找错误传播等各路径
- `launch_identity_errors_test.go` 钉两个失败页文案
- `adapters_test.go` 钉 `UserCatalog` 单方法适配与能力降级
- `flow_test.go` 以真实 HTTP 跑通 launch → 确定性解析 → 自托管 handoff → `#lti_result`、票据重放拒绝、名单未同步 fail-closed

**Commits**（按顺序）：
1. `c75e7c138` feat(lti): deterministic directory-key identity resolver
2. `82e831ebc` test(lti): two-branch matcher and launch failure states
3. `9b1f531dc` test(lti): deterministic launch-to-session flow over real HTTP
4. `358d72c91` refactor(lti): drop the superseded email-match resolver

> 分支 `feat/lti-deterministic-resolver`，base = `feat/lti-embed-self`（stacked PR；请先合入 PR1 或以其为 base 审阅）。

## Type of Change
- [ ] 🐛 Bug fix
- [x] ✨ New feature
- [x] 💥 Breaking change
- [ ] 📚 Documentation update
- [ ] 🎨 Refactor
- [ ] ⚡ Performance improvement
- [x] 🧪 Test
- [ ] 🔧 Configuration / Build / CI

> Breaking 说明：身份解析从 email 匹配整体替换为确定性目录键查找；注册行需配置 `directory_claim`（如 `sis_user_id`）并由平台注入对应自定义 claim，账号需由 daemon sweep 预置。

## Related Issue
N/A（无关联 issue；承接 `lti-sis-identity-plan.md` 决策，daemon/web 侧另行落地）

## Testing
- 逐 commit 验证：`go build ./...` + `go test ./internal/lti/... ./internal/config/... ./internal/container/... ./internal/router/... ./internal/database/...` 通过
- 关键服务包：`go test ./internal/application/service/` 通过
- 前端 `npm run type-check` 通过（本 PR 不改前端）
- `go vet ./internal/lti/...`、`gofmt`、`git diff --check` 干净
- 覆盖：两分支解析各路径、`UserCatalog` 单方法适配与能力降级、Launch 两个失败页、全链路 HTTP flow（确定性解析 + 自托管 handoff + 名单未同步 fail-closed + 重放拒绝）

## Checklist
- [x] `git diff --check github/main...HEAD` passes
- [x] Changed source files are formatted
- [x] Targeted tests for the changed packages/components pass
- [ ] Diff-scoped lint passes where applicable（本会话以 `go vet` 验证；`golangci-lint` 未全仓执行）
- [x] Self-reviewed the code
- [x] Added/updated tests covering the change
- [x] Updated related documentation（`LTI_PLACEHOLDER_DOMAIN` 已写入 `.env.example` / docker-compose）
- [x] Breaking changes are clearly called out in the description above

## Screenshots / Recordings
N/A（无用户可见 UI 变更；失败页文案由 launch iframe 内 HTML 渲染）
