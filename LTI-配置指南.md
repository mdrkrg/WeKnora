# LTI 1.3 部署配置指南（Tool + Platform）

本文档描述 WeKnora 作为 **LTI 1.3 工具（Tool）** 与 **平台（Platform，如 Canvas）** 对接所需的全部配置，并收录实际部署中遇到过的典型错误及其根因/解法。适用于自建 Canvas + 内网部署 WeKnora 的场景。

---

## 1. 总体流程（先理解再配置）

```
用户点击工具
  → Canvas 生成 launch JWT（iss 取自 security.yml 的 lti_iss）
  → 302 到工具 /lti/login_initiations（带 iss/client_id/login_hint/target_link_uri/lti_message_hint）
  → 工具回跳 Canvas /api/lti/authorize_redirect（必须原样回传 lti_message_hint 等参数）
  → Canvas 校验参数，生成 id_token，form_post 到工具的 launch 端点（= redirect_uri）
  → 工具验签（平台 JWKS 缓存）→ 四步匹配 → 签发一次性 ticket → 302 handoff
  → 回环方式二选一（见 3.6）：
      A. 外部 web S2S：web 服务端持共享密钥调 POST /lti/tickets/redeem 换 JWT
      B. 自托管 handoff：302 回 WeKnora 自己的 GET /lti/handoff，
         浏览器在 Canvas iframe 内直接建会话（/#lti_result hash 投递 SPA）
```

任一步配置不一致都会在**触达工具之前**或**验签时**失败。下面按两侧分别说明。

---

## 2. 平台（Platform / Canvas）侧配置

### 2.1 Developer Key（管理后台 → Developer Keys）

| 字段 | 值 | 说明 |
|---|---|---|
| Redirect URIs | `https://<weknora>/lti/launch` | 必须与工具发出的 `redirect_uri` **逐字节精确匹配** |
| OIDC Initiation URL | `https://<weknora>/lti/login_initiations` | 工具接收 login initiation 的地址 |
| Public JWKS URL | `https://<weknora>/.well-known/jwks.json` | Canvas 保存 developer key 硬性要求工具公钥或其地址 |
| Target Link URI | `https://<weknora>/lti/launch` | 平台回传 id_token 的默认落点；工具端仅透传不做校验 |

- 保存后 Canvas 生成 **client_id**（= `lti_registrations.client_id`）
- 注意 **Test/Production 模式**与你的环境匹配（`developer_key.rb` 的 `test_cluster_only?`），否则 launch 被拒

### 2.2 issuer（关键）

Canvas 写入 launch id_token 的 `iss` 取自 **`config/security.yml` 的 `lti_iss`**（`lib/lti/messages/jwt_message.rb:115`）。它**必须与 `lti_registrations.issuer` 完全一致**，否则工具验签报 iss 不匹配。

自建实例请在 `config/security.yml`：

```yaml
production:
  encryption_key: <%= ENV.fetch("ENCRYPTION_KEY") %>
  lti_iss: 'https://canvas.test.internal'   # 与 lti_registrations.issuer 一致
```

> 模板 `config/security.yml.example` 默认是 `https://canvas.instructure.com`，自建需改成自己的域名。

### 2.3 deployment_id

`deployment_id` 是**每次"安装"的唯一标识**：同一个 Developer Key 在账号层/课程层各装一次就各有一个 id。launch 时经 `https://purl.imsglobal.org/spec/lti/claim/deployment_id` 带给工具。

获取方式：
- 后台：课程/账号 Settings → Apps → 工具详情中的 deployment id
- API：`GET /api/v1/courses/:id/external_tools/:id`（或账号级）
- 抓包：触发一次 launch，解出 id_token 中的 deployment_id claim

### 2.4 常见错误与排查（Canvas 侧）

| 症状 | 根因 | 解法 |
|---|---|---|
| Canvas 500，日志 `ActiveModel::ValidationError (Validation failed: Iss can't be blank)` | `security.yml` 缺 `lti_iss`（或为空） | 补 `lti_iss`，见 2.2；改后重启 web 进程/容器 |
| 请求未触达工具、Canvas 出站报错 | Canvas **SSRF 防护**默认禁止访问内网 IP（`10.0.0.0/8`、`192.168.0.0/16`、`172.16/12` 等，`gems/canvas_http`） | 在 Canvas `config/initializers/canvas_http.rb` 放开：`CanvasHttp.blocked_ip_ranges = []` |
| 工具 URL 为纯 http + 内网 IP | Canvas 对工具 URL 的 scheme 校验 | 能开 https 尽量开；自建可放行（见上一条） |
| launch 被拒、Tools 不显示 | Developer Key 的 **Test/Production** 模式与实例不符，或 binding 未 `on` / 账号不对 | 核对 `developer_key_account_bindings.workflow_state = 'on'` |

**日志位置**：`log/production.log` / `log/error.log`；docker 部署用 `docker logs <web容器>`。500 的 Ruby 堆栈就在里面，先看异常类型和 controller/action。

---

## 3. 工具（Tool / WeKnora）侧配置

### 3.1 环境变量（.env / docker-compose）

| 变量 | 默认 | 说明 |
|---|---|---|
| `LTI_ENABLE` | `false` | 总开关，`true` 启用 |
| `LTI_HANDOFF_URL` | 无 | launch 成功后 302 跳转的 web 地址。方案 A（S2S）：外部 web 的兑票入口；方案 B（自托管）：`https://<weknora>/lti/handoff` 指向自身，见 3.6 |
| `LTI_HANDOFF_SHARED_SECRET` | 无 | 方案 A **必设**，web S2S redeem 用的专用共享密钥（Bearer 头恒定时间比对），生产强随机。方案 B 纯浏览器回环**不需要** |
| `LTI_SELF_HANDOFF_ENABLE` | `false` | 开启内置浏览器 handoff 端点 `GET /lti/handoff`（方案 B 必开，否则该端点 404）；同时使 SPA 根可被平台 iframe 嵌套（见 3.6） |
| `LTI_LAUNCH_URL` | 按请求 Host 推导 | **工具 launch 完整端点，必须含 `/lti/launch` 路径**（如 `https://weknora-lti.test.internal/lti/launch`）。直接作为 `redirect_uri` 发出，不再拼接 |
| `LTI_FRAME_ANCESTORS` | `'self'` | 允许 iframe 嵌套工具页面的来源，填平台域名 |
| `LTI_NONCE_MAX_AGE` | `10m` | 签名 nonce 态有效期 |
| `LTI_TICKET_TTL` | `120s` | 一次性 ticket 有效期 |
| `LTI_PLACEHOLDER_DOMAIN` | `users.lti.invalid` | 无目录/邮箱 claim 时合成邮箱的占位域 |

### 3.2 注册表行（`lti_registrations`，v1 无管理界面，SQL 直写）

```sql
INSERT INTO lti_registrations
  (issuer, client_id, deployment_ids, auth_endpoint, jwks_uri, directory_claim, enabled)
VALUES
  ('https://canvas.test.internal', '<client_id>', '["<deployment_id>"]',
   'https://canvas.test.internal/api/lti/authorize_redirect',
   'https://canvas.test.internal/api/lti/security/jwks', '', TRUE);
```

| 列 | 说明 |
|---|---|
| `issuer` | 必须等于 Canvas 的 `lti_iss`（见 2.2） |
| `client_id` | Canvas Developer Key 生成的 client_id |
| `deployment_ids` | **JSON 数组字符串**（如 `["id1"]`）；留空 = 允许任何 deployment |
| `auth_endpoint` | 平台 OIDC 授权端点（Canvas：`/api/lti/authorize_redirect`），login_initiations 302 跳这里 |
| `jwks_uri` | 平台公钥地址；工具缓存，unknown-kid 时刷新。也可用 `public_keyset` 列静态注入 |
| `directory_claim` | id_token 自定义 claim 中承载目录 uid（如 SIS id）的键；空则跳过目录匹配步 |

### 3.3 关于 directory claim 的两个位置（易混淆）

- **生效点**：`lti_registrations.directory_claim` **表列**（`internal/lti/verify.go:158` 读取）。
- **无 env 覆盖**：目录 claim 键只来自注册行列，不提供 `LTI_DIRECTORY_CLAIM` 环境变量——配置必须落在表列上。

### 3.4 常见错误与排查（Tool 侧）

| 症状 | 根因 | 解法 |
|---|---|---|
| Canvas 返回 `400 {"errors":[{"message":"lti_message_hint is missing"}]}` | 工具回跳 Canvas 授权端点时**未原样回传 `lti_message_hint`**（Canvas 的 `REQUIRED_PARAMS` 要求） | 工具 `/lti/login_initiations` 需读 `lti_message_hint` 并原样带回。已修复并折入 PR1 |
| Canvas 返回 `400 Invalid redirect_uri` | 工具发出的 `redirect_uri` 与 Developer Key 注册的 Redirect URI 不一致（含多拼了一段路径，如 `/lti/launch/lti/launch`） | `LTI_LAUNCH_URL` 填**完整端点含 `/lti/launch`**，且与 Canvas Redirect URIs 逐字节一致 |
| 工具验签报 iss 不匹配 | `lti_registrations.issuer` ≠ Canvas `lti_iss` | 两侧统一（2.2 + 3.2） |
| launch 成功但 web redeem 401 | `LTI_HANDOFF_SHARED_SECRET` 与 web 侧不一致 | 两侧用同一强随机密钥 |
| launch 302 后 `/lti/handoff` 返回 404 | `LTI_SELF_HANDOFF_ENABLE` 未开启 | 置 `true`（方案 B，见 3.6） |
| handoff 成功后 iframe 白屏/拒绝渲染 | 自托管模式落地页是 SPA 根 `/`，`X-Frame-Options SAMEORIGIN` 拦截跨源 iframe | 捆绑部署随 `LTI_SELF_HANDOFF_ENABLE` 自动切换（见 3.6）；自定义反代需等效配置 frame-ancestors 白名单 |
| launch 400（工具端） | 工具容器**无法解析平台域名**（自建 hosts 只在宿主机生效）或平台 **jwks_uri 为自签证书**，出站抓取平台公钥失败且 `public_keyset` 为空 | 手动注入 `public_keyset`，见 3.5；生产建议同时修容器 DNS + 证书 |

### 3.5 手动注入平台公钥集（`public_keyset`）

当工具容器**无法解析平台域名**（如自建 hosts 只在宿主机生效）或平台 **jwks_uri 为自签证书**导致出站抓取失败时，`verifier.Verify` 会因 keyset 解析失败返回 400（`keysetResolver.build()` 在 `public_keyset` 为空时必走 `fetchAndPersist` 出站抓取）。此时可**静态注入平台公钥集**——工具会优先使用 `public_keyset` 缓存，完全绕过出站抓取（DNS / SSRF / TLS 校验都不再触发）。

1. 在**能解析平台域名的主机**（宿主机）上抓取 Canvas 公钥集：
   ```bash
   curl -s https://canvas.test.internal/api/lti/security/jwks
   # 期望 {"keys":[{...,"kty":"RSA",...}]}
   ```
2. 写入注册表（替换 `<client_id>` 与完整 JSON）：
   ```sql
   UPDATE lti_registrations
   SET public_keyset = '<上一步的完整 JSON>',
       keyset_fetched_at = NOW()
   WHERE client_id = '<client_id>';
   ```
3. 重试 launch。

> 代价与边界：Canvas 密钥轮换（kid 变化）时 `Verify` 会触发一次 `Refresh` → 再次出站抓取 → 若 DNS/TLS 问题未解决则又会 400。**静态注入适用于测试 / 内网快速跑通**；生产环境建议一并解决容器内 DNS（如 `extra_hosts`）与平台证书问题，以保留 `jwks_uri` 自动刷新能力。
>
> 另一个隐藏阻断：即便 DNS 与证书都修好，WeKnora 的 SSRF 防护默认会拦截 `.internal` / `.local` 等后缀（`internal/utils/security.go` 的 `restrictedHostSuffixes`），直接使用动态抓取时需在工具端 env 加白名单：`SSRF_WHITELIST_EXTRA=canvas.test.internal`（支持 `*.test.internal` 通配）。手动注入 `public_keyset` 则不受此影响。

### 3.6 自托管 handoff（方案 B：WeKnora 自己作为落点，浏览器回环）

不部署独立 web 兑票服务时，让 WeKnora 自己消费 ticket：launch 成功后 302 回工具
自身的 `GET /lti/handoff`，该端点消费 ticket → 按用户 home 空间签发 JWT →
302 到 `/#lti_result=<base64url(JSON)>`，SPA 复用 OIDC callback 的落地逻辑建
会话（`frontend/src/App.vue` 的 `handleGlobalOIDCCallback`，与 OIDC 登录行为
完全一致）。

**配置**（无需共享密钥，三行即可）：

```bash
LTI_ENABLE=true
LTI_SELF_HANDOFF_ENABLE=true
LTI_HANDOFF_URL=https://<weknora>/lti/handoff    # 指向自身
# LTI_FRAME_ANCESTORS=https://canvas.test.internal    # 照常配置
```

**流程**：

```
launch 302 → /lti/handoff?ticket=...
  （端点受 LTI_SELF_HANDOFF_ENABLE 门控，未开启时 404）
  → ticket 单次消费 → IssueDefault（home 空间）签发 JWT 对
  → 302 /#lti_result=<base64url(JSON)>
     payload: { success, token, refresh_token, user_id, context_id }
  → SPA 解 hash → persistOIDCLoginResponse → 跳 /platform/knowledge-bases

失败 → 302 /#lti_error=<code>
  missing_ticket（无 ticket）/ invalid_ticket（缺失/过期/已复用）/ server_error
  SPA 映射为用户可读中文提示后跳 /login
```

**安全模型**：与 S2S redeem 对称但**不需要 `LTI_HANDOFF_SHARED_SECRET`**——
ticket 本身即承载凭证（单次、短 TTL、仅经 launch 流程签发、全程 HTTPS），
强度与 OIDC code 相当；兑换审计复用 `lti.ticket_redeemed` /
`lti.ticket_redeem_denied` 事件。

**⚠️ iframe 阻断点（自托管模式必读）**：方案 B 下整个 WeKnora UI 都跑在
Canvas iframe 里，最终落地页是 SPA 根 `/`，而默认 `X-Frame-Options
SAMEORIGIN` 会拦截跨源 iframe（表现为 **handoff 成功后 iframe 白屏/被拒渲染**）。

**捆绑部署已内置处理**：`LTI_ENABLE` + `LTI_SELF_HANDOFF_ENABLE` 同时为 true
时，前端镜像入口脚本（`frontend/docker-entrypoint.sh`）在启动时把 SPA 根的
`X-Frame-Options` 切换为 `CSP frame-ancestors`（取值同 `LTI_FRAME_ANCESTORS`，
未设则 `'self'`）——即只放行配置的平台来源，其余照旧禁止嵌套；
docker-compose 的 frontend 服务已透传这三个变量，无需手工改 nginx。
实测行为：默认 `X-Frame-Options: SAMEORIGIN`；开启后
`Content-Security-Policy: frame-ancestors <LTI_FRAME_ANCESTORS|'self'>`。

**自定义反代**（不经捆绑 nginx 的部署）需等效配置：对 SPA 根 `/` 去掉
`X-Frame-Options`，改发
`Content-Security-Policy: frame-ancestors 'self' https://canvas.test.internal;`
——保留"其余来源禁止嵌套"的防护；仅放开整站不推荐（弱化防护）。

> 若不接受 SPA 根可被平台 iframe 嵌套这一前提，请使用方案 A（S2S redeem），
> web 侧在自己的域名内完成换票，WeKnora 页面无需可被嵌套。

**与方案 A 怎么选**：
- 已有独立 web 前端/服务端、要在**外部 app 内**换票建会话 → 方案 A（共享密钥）
- 想要"点开课程即进入 WeKnora"、零额外组件 → 方案 B（本节）

### 3.7 目录绑定推送/删除（provisioning daemon）

SIS/目录 uid → WeKnora 账号的绑定由内部 daemon 经平台 API key（需
`manage_members` 能力）维护，端点挂在 `/api/v1/lti/bindings`：

**推送** `POST /api/v1/lti/bindings`：

```json
{"registration_id": 1, "external_uid": "20240001", "user_id": "<weknora 账号 ID>"}
```

- `registration_id` 必须对应 `lti_registrations` 已有行（authority
  `sis:{iss}` 由服务端从注册行推导，daemon 不自行拼装）
- 幂等重推（upsert），成功 200 返回落库值
- **写入时校验目标账号**：不存在 → 400 `unknown user`；已停用 →
  422 `user inactive`；无主工作区 → 422 `user has no workspace`

**删除** `DELETE /api/v1/lti/bindings`：

```json
{"registration_id": 1, "external_uid": "20240001", "scope": "directory"}
```

- `scope` 缺省 `directory`：删 `sis:{iss}` 行；`launch`：删
  `lti:{client_id}` 行（launch sub 绑定唯一的管理入口——该绑定异常时
  删除后该用户重走四步匹配重新建绑）
- 成功 204（无 body）；目标行不存在 404；`registration_id` 无法解析或
  scope 非法 400
- 幂等语义：删除即"不存在即可"，重复 DELETE 第二次得 404，非错误

推送与删除均落审计（`lti.binding_pushed` / `lti.binding_deleted`）。

---

## 4. 联通性验证清单

在 Canvas 所在主机执行（模拟 Canvas 出站）：

```bash
curl -i https://<weknora>/.well-known/jwks.json          # 200 + RSA JWK
curl -i -X POST https://<weknora>/lti/login_initiations
curl -i https://<weknora>/lti/launch
curl -i https://<weknora>/lti/handoff   # 方案 B 探针：开启时 302 /#lti_error=missing_ticket；未开启 404
```

在 WeKnora 所在机器抓包确认请求是否到达：

```bash
sudo tcpdump -i any -nn port <port> -w /tmp/weknora.pcap
# 点一次工具后 Ctrl-C，再：
tcpdump -r /tmp/weknora.pcap
```

- **无任何包** → 500 纯在 Canvas 生成阶段（查 2.4）
- **有 SYN 无响应** → WeKnora 端口/防火墙不通

---

## 5. 快速排查 SOP（按优先级）

1. 看 Canvas `production.log` 的 500 堆栈，确定异常类型与 controller/action
2. 核对 `lti_iss` = `lti_registrations.issuer`（最常见根因）
3. 核对 Canvas SSRF 放行内网 + 工具 URL 可达（curl）
4. 核对 Developer Key 三要素（Redirect URIs / OIDC Initiation / Public JWKS）与注册表、env 一致
5. 再拉一次日志，把新堆栈给到开发侧定位

---

## 6. 相关代码位置（供开发侧参考）

- 工具端：`internal/lti/`（handlers/verify/state/ticket/matcher/minter）
- 注册表：`migrations/versioned/000090_lti_core.up.sql`（+ 000091 身份绑定）
  （主分支编号；**backport（v0.7.2）分支为 900001/900002**）
- 平台端（Canvas）：`app/models/developer_key.rb`、`app/controllers/lti/ims/authentication_controller.rb`、`lib/lti/messages/jwt_message.rb`、`gems/canvas_http/lib/canvas_http.rb`
