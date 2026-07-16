# 解析产物存储与返回 功能规范

## 1. 背景与现状

WeKnora 解析文档后，完整的解析结果没有被保留：

- 解析引擎（builtin / MinerU / PaddleOCR-VL / WeKnora Cloud 等）输出的完整 Markdown，在图片地址归一化后直接传给分块器，随后即被丢弃。系统中不存在文档的无损解析结果。
- 图片：解析器输出相对路径引用 + 图片字节，系统将图片逐张上传对象存储并把 Markdown 中的引用改写为 `provider://` URL。图片本身已持久化，但**没有独立清单**——删除文档时需要扫描 chunks 表的 `ImageInfo` 字段反推图片列表，依赖 OCR/Caption 子 chunk 的存在。
- 布局数据：MinerU 可返回 `content_list` / `middle_json` / `model_output` 等版面结构数据，目前请求时即被关闭或仅用于日志后丢弃。
- `GET /knowledge/{id}/preview` 返回的是**原始上传文件**（PDF/DOCX 等），不是解析后的 Markdown；手工创建的 Markdown 知识存于 `Knowledge.metadata`，属于特例，不覆盖文件类文档。
- 从 chunks 重建完整 Markdown 不可靠：受 overlap、父子分块、chunk 被编辑/禁用/删除等影响，不是无损方案。

因此需要引入"解析产物"（Artifact）概念：持久化每次解析的完整结果，并通过 API 返回。

## 2. 产物模型

### 2.1 基本概念

- 一个 Knowledge 的一次解析尝试（attempt，含首次解析和每次 reparse）产生**一组产物**。
- attempt 编号为**按 Knowledge 单调递增的整数**；编号分配必须串行化（并发触发同一知识的 reparse 不得产生重复编号）。
- 每个 Knowledge 有一个"当前 attempt"指针，读取产物时默认命中当前版本。
- 产物按类型分为两层：**规范层**与**引擎原生层**。

### 2.2 规范层

系统承诺跨引擎语义一致、格式稳定，仅两种：

| 类型 | 内容 | 说明 |
|------|------|------|
| `markdown` | 完整解析 Markdown | 图片地址归一化后（即引用为 `provider://` URL）、传给分块器之前的那份内容，与 chunk 内容中的图片引用一致 |
| `image_manifest` | 图片清单 | 由 WeKnora 自身在**图片提取与上传阶段**生成（数据来源是引擎交付给系统的图片引用与字节，而非解析原生产物获得），因此格式与引擎无关。按唯一存储 URL 每张图片一条记录：存储 URL、原始引用列表（同一图片可能被多处引用，如 `images/{hash}.jpg`）、MIME 类型、大小 |

> 测试: `TestImageManifestHasSize`, `TestArtifactManifestExistsForEmptyDoc`

关于原始引用与原生产物的关联：清单中的"原始引用"是引擎交付图片时使用的相对路径。当引擎在其原生产物内部（如 MinerU `content_list` 的 `img_path`）使用相同路径时，客户端可据此关联；系统**不解析原生产物**来提取或校验这些内部引用，不保证所有原生产物内部引用都能在清单中命中。

规范层产物是**解析成功的必要组成部分**：无条件保存，不提供开关。

### 2.3 引擎原生层（engine-native）

引擎特有的附加输出（如 MinerU 的 `content_list.json`、`middle.json`、`model.json`），处理原则：

- **不透明直存、原样返回**：系统不解析、不转换、不做跨引擎格式统一，只保证存取无损。消费方需自行理解对应引擎的格式。
- 类型标记为 `engine_native`，并携带 `native_kind`（引擎自定义的产物种类名，如 `content_list`、`middle_json`、`model_output`）。
- `native_kind` 的值由实现在写入时根据引擎输出映射确定（如 MinerU 的 `middle_json` 响应字段 → `native_kind=middle_json`，zip 内 `*_content_list_v2.json` → `native_kind=content_list_v2`）。调用方无需知晓底层文件路径或命名规则，通过产物列表端点发现可用值后直接按名读取即可。
- 原生产物中的图片引用（如 `img_path` 指向 zip 内相对路径）**原样保留、不做改写**。客户端可用 `image_manifest` 中"原始引用 → 存储 URL"的映射自行关联——这是 image_manifest 存在的核心理由之一。
- 原生产物采集**默认关闭**，由租户级配置控制（租户的解析引擎配置中设有开关）。关闭导致的原生产物缺失属预期行为。
- 原生产物是 best-effort：采集或保存失败仅记录告警，不阻断解析。

> 测试: `TestArtifactEngineNativeEnabled`, `TestArtifactEngineNativeDisabled`, `TestArtifactList`

### 2.4 产物元数据

每个产物必须记录：

- 所属租户、所属 Knowledge、attempt 编号
- **解析引擎类型**（`engine`：builtin / mineru / mineru_cloud / paddleocr_vl / weknora_cloud 等）——所有产物必备，不限于原生层
- 产物类型（`markdown` / `image_manifest` / `engine_native`）及 `native_kind`（仅原生层）
- 格式（markdown / json / zip 等）、大小、sha256、存储位置、创建时间

## 3. 行为规范

### 3.1 解析成功时

- 图片地址归一化完成后、分块之前，系统必须将该份 Markdown 完整保存为当前 attempt 的 `markdown` 产物。
- 系统必须同时生成并保存 `image_manifest` 产物（若该文档无图片，清单为空列表，产物仍存在）。
- 规范层产物保存失败（含存储后端错误、配额不足）→ **该次解析尝试判定为失败**，与原始文件上传失败导致解析失败的现有语义一致。不产生"解析完成但无产物"的状态。
- 若开启了原生产物采集且引擎有对应输出，系统应将其作为同一 attempt 的 `engine_native` 产物保存；失败仅告警。
- 引擎未产出某类产物时，该类产物不存在（不生成占位物）。

> 测试: `TestArtifactManifestExistsForEmptyDoc`, `TestArtifactQuotaExhausted`

### 3.2 重新解析时

- 新 attempt 的产物写入独立位置，**不覆盖**旧版本产物。
- 仅在该次解析成功后，"当前 attempt"指针切换到新版本；解析中途或末尾失败时，上一个可用版本必须完好、可继续读取。
- 失败 attempt 已写入的部分产物**不得**通过读取 API 可见（不存在"仅部分产物可见"的 attempt）；这些残留对象必须被清理并释放配额，清理可延后（如由后续版本轮换或后台补漏机制完成）。
- 保留策略：保留**当前版本 + 上一个成功版本**，更旧版本的产物应被清理，清理时释放对应配额。
- 指针切换与旧版本清理的顺序约定：先切换指针，清理异步进行；**清理失败不得撤销或阻塞指针切换**，且必须有重试或后台补漏机制回收孤儿产物，不允许永久泄漏存储与配额。

> 测试: `TestArtifactReparseDefaultToNewAttempt`, `TestArtifactReparseHistoryAvailable`, `TestArtifactReparseFailureKeepsCurrent`, `TestArtifactReparsePartialInvisible`, `TestArtifactVersionRetentionCleansOldAttempt`, `TestArtifactRetentionOldCleaned`, `TestArtifactRetentionPrevKept`

### 3.3 读取时

提供以下 API 端点，所有端点共享相同的权限模型（见下方权限小节）：

#### 产物内容读取

`GET /knowledge/{id}/artifact`

返回指定类型产物的内容与元数据。

> 测试: `TestArtifactReadMarkdown`, `TestArtifactTypeDefaults`

**查询参数：**

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `type` | string | `markdown` | 产物类型：`markdown`、`image_manifest`，或 `engine_native`（此时须同时传 `native_kind`） |
| `native_kind` | string | — | 仅 `type=engine_native` 时生效，指定原生产物种类（如 `content_list`） |
| `attempt` | int | `0`（当前版本） | 指定历史 attempt 编号 |
| `resolve_images` | bool | `false` | 为 `true` 时将内容中的 `provider://` URL 替换为限时预签名 HTTP URL（复用现有文件预签名机制）。注意：此时返回的 `content` 为重写后的形态，`sha256` 仍描述**存储态**产物，调用方仅能在未开启该参数时用 `sha256` 校验返回字节 |

**成功响应：**

```json
{
  "knowledge_id": "...",
  "parse_attempt": 2,
  "engine": "mineru",
  "artifact_type": "markdown",
  "format": "markdown",
  "sha256": "abc123...",
  "size": 12345,
  "content": "# 标题\n..."
}
```

- 无产物时（功能上线前解析的存量文档、解析尚未完成、解析失败且无历史版本）：返回明确的"产物不存在"错误（404 语义），错误信息应提示可通过 reparse 补齐，不返回空内容冒充产物。
- 手工 Markdown 知识：同一端点透明返回其 `metadata` 中的内容，行为对调用方透明；不强制迁移其存储方式。元数据字段映射：`engine` 固定为哨兵值 `manual`，`parse_attempt` 取 `ManualKnowledgeMetadata.Version`，`format` 为 `markdown`，`sha256`/`size` 按实际返回内容计算。
- 响应有大小上界：超过上界的产物内容不得内联返回，此时 API 返回明确错误（含可读的消息与错误码），指引调用方改用流式下载方式取回（见下方下载端点）。上界数值在实现时确定并写入 API 文档。

> 测试: `TestArtifactReadMarkdown`, `TestArtifactReadResolveImages`, `TestArtifactNotFound`, `TestArtifactManualKnowledge`, `TestArtifactOversizedContent`, `TestArtifactTypeDefaults`

#### 产物列表

`GET /knowledge/{id}/artifacts`

返回指定知识在某 attempt 下实际存在的所有产物元数据（不含内容体），供调用方发现可用产物后再按需读取。

> 测试: `TestArtifactList`

**查询参数：**

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `attempt` | int | `0`（当前版本） | 指定历史 attempt 编号 |

**成功响应：**

```json
[
  {"artifact_type": "markdown",                                 "format": "markdown", "sha256": "...", "size": 12345, "created_at": "..."},
  {"artifact_type": "image_manifest",                           "format": "json",     "sha256": "...", "size": 234,   "created_at": "..."},
  {"artifact_type": "engine_native", "native_kind": "content_list", "format": "json",     "sha256": "...", "size": 5678,  "created_at": "..."}
]
```

#### 产物下载

`GET /knowledge/{id}/artifact/download`

流式下载产物完整内容（无大小限制）。查询参数与读取端点一致（`type`、`native_kind`、`attempt`、`resolve_images`）。响应为二进制流，Content-Type 按产物格式设置。

> 测试: `TestArtifactDownload`

#### 权限

所有产物端点的权限语义与 preview/download 完全一致：Viewer 角色 + 经 knowledge_id 解析的知识库读权限校验，覆盖自有、组织共享、共享 Agent 三种访问路径。禁止仅凭产物 ID 或存储路径绕过知识库权限直读。

> 测试: `TestArtifactPermissionDenied`

### 3.4 删除时

- 删除 Knowledge：该知识**所有 attempt 的所有产物**必须随之删除（可异步），产物总大小从租户 `StorageUsed` 扣减。
- 删除知识库、租户清理：级联执行同样的产物清理与配额扣减。
- 版本轮换（3.2 保留策略）淘汰旧版本时，同样删除对象并扣减配额。

### 3.5 配额

- 所有产物大小计入租户 `StorageQuota` / `StorageUsed`，与现有原始文件、图片占用配额同一套语义，无特殊分支。
- 保存规范层产物时配额不足 → 解析失败，错误信息须指明配额原因。
- 保存原生产物时配额不足 → 放弃该原生产物并告警，不阻断解析。

> 测试: `TestArtifactQuotaExhausted`

## 4. 非功能要求

- **无损性**：读取到的产物内容与保存时字节一致，sha256 可供调用方校验；`markdown` 产物与当次传给分块器的内容一致。
- **多租户隔离**：产物的存储与读取严格限定在所属租户内。
- **可用性**：产物读取不应影响解析吞吐；解析高峰期读取历史产物应正常工作。图片预签名解析（3.3）在大文档（数百张图）场景下不得逐张串行请求对象存储导致端点饱和，应支持批量或并行处理。

## 5. MinerU 原生产物存储方式

MinerU 有两条接入路径，结果取回形态不同：

- **自建 API**（`/file_parse`、`/tasks`）：由 `response_format_zip` 参数决定——`false`（默认，WeKnora 当前所用）返回 JSON 字段（`md_content` / `middle_json` / `model_output` / `content_list` / `images`）；`true` 返回 zip 包。**JSON 响应不含 `content_list_v2`，zip 包含**（随 `return_content_list` 一并产出）。
- **MinerU Cloud**：任务结果经 `full_zip_url` 下载 zip 包（内含 `{文件名}.md`、`*_content_list.json`、`*_content_list_v2.json`、`*_middle.json`、`*_model.json`、`images/`）。

### 决策：拆存为多个原生产物

- zip 形态（Cloud 或自建 zip 模式）：解压后将关注成员各自存为一条 `engine_native` 产物；JSON 形态（自建默认）：直接从响应字段取相同内容。各形态产出**一致的产物模型**，调用方无需感知接入方式与取回形态。
- 成员 → `native_kind` 映射：`*_content_list.json` → `content_list`，`*_content_list_v2.json` → `content_list_v2`，`*_middle.json` → `middle_json`，`*_model.json` → `model_output`。
- `content_list_v2` 仅在 zip 形态可得（当前版本 MinerU 的 JSON 响应不返回它）。若实现沿用自建 API 的 JSON 模式，该产物缺失属预期，不视为错误；如需它，实现可改用 `response_format_zip=true` 取回。此约束随 MinerU 版本可变：未来 MinerU 的 JSON 响应若支持返回 `content_list_v2`，应直接从 JSON 保存，产物模型不受影响。
- **跳过 `images/` 目录**：图片已由图片管道单独入库并计入配额，不重复存储、不重复计费。
- 跳过 zip 内 `.md`：规范层 `markdown` 产物已保存归一化版本；若需保留引擎原始 Markdown，可另设 `native_kind=raw_markdown`。
- 无法识别的成员按 `native_kind=unknown:{文件名}` 原样直存，不丢数据——以此缓解对 zip 内部布局（解析方法子目录名 auto / txt / ocr / hybrid_* / vlm 等）变化的耦合。
- 单个成员保存失败：遵循原生产物 best-effort 语义（2.3），告警并跳过该成员，不影响其他成员与解析流程。

### 备选方案及未采纳理由

| 方案 | 未采纳理由 |
|------|-----------|
| 整包直存 zip（`native_kind=full_archive`） | 客户端取 content_list 须下载含全部图片的整包；zip 内 `images/` 与已入库图片双重计费；无成员级 sha256/size；自建 API 的 JSON 模式（当前所用）无 zip，需强制切换取回形态才能统一 |
| 拆存 + 保留原包 | 仅在有"逐字节复原原始 zip"的审计需求时有价值，且接受图片重复计费；当前无此需求 |

拆存的代价（写路径多次上传、清理需删多个对象）与现有图片管道的批量上传/批量删除模式一致，不引入新模式。

## 6. 验收场景

> 每个验收场景对应一个或多个测试。全部测试文件: `internal/handler/knowledge_artifact_test.go`

**首次解析**

- Given 一个新上传的 PDF 且解析引擎为 MinerU，When 解析成功，Then 存在当前 attempt 的 `markdown` 与 `image_manifest` 产物，元数据中 `engine=mineru`，且 Markdown 中图片引用与 image_manifest 的存储 URL 一致。

**读取**

- Given 解析完成的文档，When 调用 `GET /knowledge/{id}/artifact?type=markdown`，Then 返回 `content` 与 `sha256`，且 sha256 与内容实际哈希一致。
  > `TestArtifactReadMarkdown`
- Given 调用方要求解析图片 URL，When `GET /knowledge/{id}/artifact?type=markdown&resolve_images=true`，Then 内容中 `provider://` 引用被替换为限时可访问的 HTTP URL。
  > `TestArtifactReadResolveImages`

**产物列表**

- Given 解析完成的文档（引擎为 MinerU 且开启了原生产物采集），When `GET /knowledge/{id}/artifacts`，Then 返回的列表包含 `markdown`、`image_manifest` 以及 `engine_native`（如 `content_list`）。
  > `TestArtifactList`, `TestArtifactEngineNativeEnabled`

**大文件下载**

- Given 产物内容超过内联大小上界，When `GET /knowledge/{id}/artifact?type=markdown`，Then 返回错误并指引调用方改用 `GET /knowledge/{id}/artifact/download?type=markdown`。
  > `TestArtifactOversizedContent`

**reparse**

- Given 文档已有 attempt 1 的产物，When reparse 成功产生 attempt 2，Then `GET /knowledge/{id}/artifact` 默认返回 attempt 2，且 `GET /knowledge/{id}/artifact?attempt=1` 仍可读取。
  > `TestArtifactReparseDefaultToNewAttempt`, `TestArtifactReparseHistoryAvailable`
- Given 文档已有 attempt 1 的产物，When reparse 失败，Then 当前版本仍为 attempt 1，读取不受影响。
  > `TestArtifactReparseFailureKeepsCurrent`
- Given reparse 失败前已写入部分产物，When 读取该失败 attempt 的产物，Then 返回"产物不存在"，且残留对象最终被清理、配额释放。
  > `TestArtifactReparsePartialInvisible`
- Given 文档已有 attempt 1、2 的产物，When attempt 3 解析成功，Then attempt 1 的产物被清理且配额相应扣减。
  > `TestArtifactVersionRetentionCleansOldAttempt`, `TestArtifactRetentionOldCleaned`, `TestArtifactRetentionPrevKept`

**无产物**

- Given 功能上线前解析完成的存量文档，When `GET /knowledge/{id}/artifact`，Then 返回"产物不存在"错误并提示可 reparse。
  > `TestArtifactNotFound`

**权限**

- Given 调用方对该知识所在知识库无读权限，When `GET /knowledge/{id}/artifact`，Then 请求被拒绝，行为与 preview 端点一致。
  > `TestArtifactPermissionDenied`

**删除与配额**

- Given 文档有多个 attempt 的产物，When 删除该 Knowledge，Then 所有产物对象被清理，租户 `StorageUsed` 扣减产物总大小。
  > 服务层: `knowledge_delete.go` 删除流程中的 artifact 清理逻辑
- Given 租户剩余配额小于 Markdown 产物大小，When 解析执行到产物保存，Then 解析失败且错误指明配额不足。
  > `TestArtifactQuotaExhausted`

**原生产物**

- Given 租户开启原生产物采集且引擎为 MinerU，When 解析成功，Then `GET /knowledge/{id}/artifacts` 列表中包含 `engine_native` 产物（如 `content_list`），且 `GET /knowledge/{id}/artifact?type=engine_native&native_kind=content_list` 返回的内容与引擎输出逐字节一致。
  > `TestArtifactEngineNativeEnabled`
- Given 未开启原生产物采集，When 解析成功，Then 仅存在规范层产物，`GET /knowledge/{id}/artifact?type=engine_native&native_kind=content_list` 返回"产物不存在"。
  > `TestArtifactEngineNativeDisabled`

**手工知识**

- Given 一个手工创建的 Markdown 知识，When `GET /knowledge/{id}/artifact`，Then 返回其 metadata 中的内容与版本号，调用方无需区分知识类型。
  > `TestArtifactManualKnowledge`
