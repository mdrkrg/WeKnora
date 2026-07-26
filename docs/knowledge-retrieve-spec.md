# 知识检索增强 API 规范

## 1. 概述

`POST /api/v1/knowledge-retrieve` 是 WeKnora 的增强检索端点。它在现有 `/knowledge-search` 基础上增加了 LLM 查询理解、并行检索（含知识图谱）、本地查询扩展等能力，但不执行 LLM 答案生成。

消费者可以使用此端点获取经过完整检索管线处理的结构化结果，然后自行调用第三方 LLM 生成回答。这种分离使消费者能完全控制 prompt 格式、citation 机制和句子级高亮策略。

### 1.1 与现有 API 的关系

| 阶段 | `/knowledge-search` | `/knowledge-retrieve` | `/knowledge-chat-stateless` |
|---|---|---|---|
| Query Understanding (LLM 改写 + 意图 + 实体) | no | yes (可选) | yes |
| Parallel Search (含知识图谱) | no | yes | yes |
| Local Query Expansion (本地启发式) | no | yes (可选) | yes |
| Rerank | yes | yes | yes |
| Merge | yes | yes | yes |
| MMR Select Top K | yes | yes | yes |
| LLM 答案生成 | no | no | yes |
| 返回格式 | `SearchResult[]` | `SearchResult[]` + 元数据 | SSE 流 |
| 无状态 | yes | yes | yes |
| 无 DB 写入 | yes | yes | yes |

`/knowledge-search` 保留不变，作为底层混合检索端点。

## 2. 请求

### 2.1 端点

```
POST /api/v1/knowledge-retrieve
```

Content-Type: `application/json`

### 2.2 请求体

```json
{
  "query": "怎么修登录bug",
  "knowledge_base_id": "kb-00000001",
  "knowledge_base_ids": ["kb-00000001", "kb-00000002"],
  "knowledge_ids": ["4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5"],
  "tag_ids": ["tag-001"],
  "mentioned_items": [],
  "enable_query_understand": true,
  "enable_query_expansion": true,
  "chat_model_id": "model-uuid",
  "rerank_model_id": "rerank-model-uuid",
  "history": [
    {"role": "user", "content": "登录功能怎么用"},
    {"role": "assistant", "content": "登录功能位于..."}
  ]
}
```

### 2.3 字段说明

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|---|---|---|---|---|
| `query` | string | **是** | — | 搜索查询文本 |
| `knowledge_base_id` | string | 否 | — | 单个知识库 ID（向后兼容）；与 `knowledge_base_ids` 合并 |
| `knowledge_base_ids` | string[] | 否 | — | 多个知识库 ID 列表，跨知识库检索 |
| `knowledge_ids` | string[] | 否 | — | 限定到指定知识（文件）；不传则在整库范围内搜索 |
| `tag_ids` | string[] | 否 | — | Tag ID 列表。服务端根据每个 Tag ID 的实际所属 KB 分组，并仅在该 KB 的检索范围内过滤；Tag ID 所属 KB 必须位于请求的有效 KB 范围内 |
| `mentioned_items` | MentionedItem[] | 否 | — | scoped tag mentions。用于显式指定 Tag ID 与所属 KB 的绑定关系 |
| `enable_query_understand` | bool | 否 | `true` | 是否启用 LLM 查询理解。启用后执行查询改写和意图分类；满足知识图谱条件时还会执行独立的实体提取。注意：当服务端全局 `enable_rewrite` 配置为 `false` 时，即便此字段为 `true`，查询理解阶段也会跳过（与 `/knowledge-chat-stateless` 行为一致） |
| `enable_query_expansion` | bool | 否 | `true` | 是否启用本地查询扩展（低召回时触发，无 LLM 调用） |
| `chat_model_id` | string | 否 | Tenant 默认模型 | 查询理解使用的 KnowledgeQA 模型。接受模型 ID 或当前 Tenant 可访问模型中的唯一名称；不传时使用 Tenant 唯一的默认 KnowledgeQA 模型。`enable_query_understand=false` 时忽略此字段 |
| `rerank_model_id` | string | 否 | Tenant 默认 rerank 模型 | 检索重排序使用的 Rerank 模型。接受模型 ID 或当前 Tenant 可访问 Rerank 模型中的唯一名称；不传时使用 Tenant RetrievalConfig 中的 `rerank_model_id`，再回退到自动探测第一个可用 Rerank 模型 |
| `history` | HistoryMessage[] | 否 | `[]` | 对话历史，用于查询改写的多轮上下文消解 |

> 必须至少指定一个知识库 ID、一个 Knowledge ID，或一个包含有效 `id` 和 `kb_id` 的 Tag mention。裸 `tag_ids` 不能在没有任何 KB 或 Knowledge 范围时单独构成检索范围。

检索范围按以下规则组合：

- `knowledge_base_id` 与 `knowledge_base_ids` 合并并去重；不同 KB 的最终范围取并集。
- `tag_ids` 按 Tag ID 查询实际所属 KB；多个 Tag 使用 OR 语义。同名 Tag 在不同 KB 中具有不同 ID，必须分别传入这些 ID。
- 请求的有效 KB 范围包括显式指定的 KB，以及由 `knowledge_ids` 和 scoped Tag mention 解析出的 KB。
- 裸 `tag_ids` 中的每个 Tag 所属 KB 必须位于请求的有效 KB 范围内，否则返回 400。
- 裸 `tag_ids` 与 `mentioned_items` 中的 Tag ID 合并去重；同一 Tag 若同时出现，`kb_id` 必须与该 Tag 的实际所属 KB 一致。
- `mentioned_items` 中用于本端点检索的 item 必须为 `type="tag"`，且 `id`、`kb_id` 均非空；其 `id` 与 `kb_id` 必须匹配，否则返回 400。
- 同一 KB 同时指定 Knowledge ID 和 Tag 时，取“指定 Knowledge”与“带任一指定 Tag 的 Knowledge”的交集。
- 如果某 KB 被无 Tag 条件地整库选中，则同一 KB 中重复出现的 `knowledge_ids` 不会缩小整库范围；属于其他 KB 的 `knowledge_ids` 作为其他检索目标加入并集。
- 最终范围为空是合法检索结果，返回 200 和空的 `results`；范围表达不合法才返回 400。

### 2.4 HistoryMessage 结构

```json
{"role": "user", "content": "登录功能怎么用"}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `role` | string | **是** | 只允许 `"user"` 或 `"assistant"`。**不允许 `"system"`** |
| `content` | string | **是** | 消息文本 |

约束：`history` 最多 100 条消息。超出返回 400。

### 2.5 MentionedItem 结构

```json
{"id": "tag-001", "name": "常见问题", "type": "tag", "kb_id": "kb-00000001", "kb_name": "帮助中心"}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 被 mention 的实体 ID |
| `name` | string | 显示名称 |
| `type` | string | 实体类型：`"kb"` / `"file"` / `"tag"` / `"mcp"` / `"skill"` |
| `kb_type` | string | KB 类型，仅 `type="kb"` 时使用：`"document"` 或 `"faq"` |
| `kb_id` | string | 父知识库 ID（对 file/tag mentions 有效） |
| `kb_name` | string | 父知识库显示名称 |
| `service_id` | string | 父 MCP service ID（对 MCP tool mentions 有效） |
| `skill_name` | string | preloaded agent skill 名称（对 skill mentions 有效） |

对于本端点，`mentioned_items` 仅接受 `type="tag"` 的 item；`kb`、`file`、`mcp`、`skill` 类型不参与知识检索范围，传入时返回 400。Tag item 必须同时提供非空的 `id` 和 `kb_id`，且该 Tag 实际属于该 KB。

### 2.6 认证

支持以下认证方式：

- `Authorization: Bearer <JWT>`：JWT 用户必须是当前 Tenant 的有效成员，且至少具有 Viewer 角色
- `X-API-Key: <key>`：API Key 必须具有 `retrieve` capability 或 `full_access` 权限

调用者应只提供一种认证凭证。同时提供时，有效的 Bearer JWT 优先；Bearer JWT 未通过认证时再尝试 `X-API-Key`。

缺失、无效或过期的认证凭证，或者凭证无法解析出有效 Tenant 时返回 401。身份有效但角色/capability 不足，或者无权访问目标 KB / Knowledge / Tag / 模型时返回 403。

## 3. 响应

### 3.1 响应体

响应为普通 JSON（非 SSE 流）：

```json
{
  "success": true,
  "data": {
    "rewrite_query": "登录认证失败 解决方案",
    "intent": "kb_search",
    "results": [
      {
        "id": "chunk-00000001",
        "content": "登录认证失败时，首先检查用户名和密码是否正确...",
        "knowledge_id": "knowledge-00000001",
        "knowledge_title": "登录模块文档",
        "knowledge_filename": "login.md",
        "knowledge_source": "file",
        "knowledge_description": "登录模块技术参考文档",
        "knowledge_channel": "api",
        "knowledge_base_id": "kb-00000001",
        "chunk_index": 0,
        "start_at": 120,
        "end_at": 500,
        "score": 0.95,
        "match_type": "vector",
        "chunk_type": "text",
        "parent_chunk_id": "",
        "sub_chunk_id": [],
        "image_info": "",
        "metadata": {},
        "chunk_metadata": {},
        "matched_content": "",
        "content_segments": [
          {
            "text": "登录认证失败时，首先检查用户名和密码是否正确...",
            "chunk_id": "chunk-00000001",
            "knowledge_id": "knowledge-00000001",
            "source_start": 120,
            "source_end": 500,
            "chunk_type": "text"
          }
        ]
      }
    ]
  }
}
```

成功响应的 JSON 结构固定为 `success` + `data`。`success` 必须为 `true`；`data.rewrite_query`、`data.intent` 和 `data.results` 始终存在。无检索结果时 `data.results` 返回空数组 `[]`，不返回 `null`，也不省略该字段。

`SearchResult` 的字段始终存在；没有值时使用以下表示：字符串为 `""`，整数或浮点数为 `0`，字符串数组为 `[]`，对象为 `{}`。因此 `metadata` 始终是字符串键值对象，`chunk_metadata` 始终是 JSON 对象，`sub_chunk_id` 始终是字符串数组，`image_info` 始终是 JSON 字符串。

### 3.2 顶层字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `success` | bool | 请求是否成功 |
| `data.rewrite_query` | string | LLM 改写后的查询。未启用查询理解时等于原始 `query` |
| `data.intent` | string | 意图分类结果。可能的值见 [3.3 意图分类](#33-意图分类)。未启用查询理解时固定为 `kb_search` |
| `data.results` | SearchResult[] | 检索结果数组。字段结构见 [3.4 SearchResult](#34-searchresult)。结果集合由 MMR 综合相关性与内容多样性选出；数组按已选结果的 `score` 降序排列，仅表示展示顺序 |

### 3.3 意图分类

| Intent | 说明 | 是否触发检索 |
|---|---|---|
| `kb_search` | 知识库检索 | yes |
| `clarification` | 需要进一步澄清 | yes |
| `summarize` | 请求文档总结 | yes |
| `chitchat` | 闲聊 | no |
| `greeting` | 问候语 | no |
| `follow_up` | 追问（依赖历史上下文） | no |
| `web_search` | 要求网络搜索 | no（本端点不做网络搜索） |
| `image_only` | 仅含图片的查询 | no（本端点不做图片理解） |
| `doc_only` | 仅含附件的查询 | no（本端点不做附件理解） |

当 `enable_query_understand=false` 时，不执行查询改写、意图分类或实体提取：`rewrite_query` 等于原始 `query`，`intent` 固定为 `kb_search`，`history` 不参与查询处理，并跳过 Entity Search。Chunk Search、Rerank、Merge 和 MMR Select Top K 继续执行；若 `enable_query_expansion=true`，Local Query Expansion 仍可按触发条件执行。

当意图判定为无需检索时（如 `chitchat`），管线跳过所有检索阶段，`results` 返回空数组，`rewrite_query` 和 `intent` 正常返回。消费者可通过 `intent` 决定是否将 query 直接传递给 LLM。

### 3.4 SearchResult

`results` 数组中每个元素的完整字段：

本文所称**分块输入文本**，是指知识摄入过程中完成文档解析、格式转换和图片引用处理后，实际传给分块器的完整文本。例如，PDF / DOCX 对应解析后的 Markdown，CSV / JSON 对应转换后的 Markdown，手动录入内容对应去除首尾空白并完成图片引用处理后的文本。分块输入文本不是原始文件的二进制内容，也不一定与原始文件的可见文本逐字符一致。

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | Chunk ID |
| `content` | string | Chunk 全文 |
| `knowledge_id` | string | 所属 Knowledge（文件）ID |
| `chunk_index` | int | Chunk 在所属 Knowledge 文件中的序号；不表示本次响应中的排名 |
| `knowledge_title` | string | 文件标题 |
| `start_at` | int | Chunk 对应范围在分块输入文本中的起始 rune 偏移，包含该位置；无可定位的源文本范围时为 `0` |
| `end_at` | int | Chunk 对应范围在分块输入文本中的结束 rune 偏移，不包含该位置；无可定位的源文本范围时为 `0` |
| `score` | float | Rerank 后归一化的最终相关性分数 |
| `match_type` | string | 匹配类型。见 [3.5 MatchType](#35-matchtype) |
| `sub_chunk_id` | string[] | 合并过程中累积的所有参与 chunk ID（parent-child 子 chunk、重叠区间合并 chunk、邻居扩展 chunk 等），去重后的集合 |
| `metadata` | object | 文件级自定义元数据（键值对） |
| `chunk_type` | string | Chunk 类型：`text` / `parent_text` / `image_ocr` / `image_caption` / `summary` / `entity` / `relationship` / `faq` / `web_search` / `table_summary` / `table_column` / `wiki_page` |
| `parent_chunk_id` | string | 父 Chunk ID（parent-child 分块时使用） |
| `image_info` | string | 图片 Chunk 的附加信息（JSON 字符串） |
| `knowledge_filename` | string | 原始文件名 |
| `knowledge_source` | string | 知识来源：`"file"` / `"url"` / `"manual"` |
| `knowledge_channel` | string | 摄入渠道：`"web"` / `"api"` / `"wechat"` 等 |
| `chunk_metadata` | object | Chunk 级元数据（如生成的关联问题） |
| `matched_content` | string | 实际匹配到的文本（FAQ 场景为匹配的问题文本） |
| `knowledge_description` | string | 知识文件的描述/摘要 |
| `knowledge_base_id` | string | 所属知识库 ID |
| `content_segments` | ContentSegment[] | `content` 的构成段落列表。结构见 [3.4.1 ContentSegment](#341-contentsegment) |

### 3.4.1 ContentSegment

描述 `content` 中一个连续文本段落的来源信息。`content_segments` 数组始终至少包含一个元素；数组中各 segment 的 `text` 按原始顺序连接后等于 `content`。

| 字段 | 类型 | 说明 |
|---|---|---|
| `text` | string | 该 segment 在 `content` 中的文本内容。所有 segment 的 `text` 连接后等于完整的 `content` |
| `chunk_id` | string | 该 segment 文本在原文中所属的 chunk ID |
| `knowledge_id` | string | 所属 Knowledge（文件）ID |
| `source_start` | int | Segment 文本在分块输入文本中的起始 rune 偏移（含）。意义与顶层 `start_at` 一致，但范围仅覆盖该 segment 的文本 |
| `source_end` | int | Segment 文本在分块输入文本中的结束 rune 偏移（不含）。意义同上 |
| `chunk_type` | string | 来源 chunk 的类型，值同顶层 `chunk_type` |

**用途**：Merge、邻居扩展、父子分块解析等阶段会合并多个 chunk 的文本到统一的 `content` 字段。`content_segments` 将合并后的文本逐段映射回各自的原始 chunk 和源文本范围。

示例（两个 chunk 合并后的 `content_segments`）：

```json
{
  "content": "Chunk A 的文本内容...Chunk B 的文本内容...",
  "content_segments": [
    {
      "text": "Chunk A 的文本内容...",
      "chunk_id": "chunk-a",
      "knowledge_id": "knowledge-00000001",
      "source_start": 0,
      "source_end": 480,
      "chunk_type": "text"
    },
    {
      "text": "Chunk B 的文本内容...",
      "chunk_id": "chunk-b",
      "knowledge_id": "knowledge-00000001",
      "source_start": 500,
      "source_end": 1020,
      "chunk_type": "text"
    }
  ]
}
```

`segment` 内的文本字符全部来源于对应 chunk 的源文本；`content` 不包含仅由合并产生的合成字符（如分隔符、连接符等）。`content` 中每个 rune 恰好属于一个 segment。

**定位指引**：对于 `content` 中 rune 区间 `[pos, pos+len)` 内的任意子串，若其全部位于某个 segment `s` 的覆盖范围内，则该子串在分块输入文本中的对应 rune 区间为 `[s.source_start + offset, s.source_start + offset + len)`，其中 `offset` 是该子串在 `s.text` 内的起始 rune 偏移（以 0 为起点）。若子串跨越多个 segment 边界，原文范围由所属各 segment 的对应区间拼接表达。

**Overlap 裁切**：当 Merge 阶段处理相邻 chunk 存在源范围重叠时，合并后 `content` 中的重叠文本仅保留一次。`content_segments` 必须满足以下不变式：

- 所有 segment 的 `text` 按顺序连接后等于 `content`，相邻 segment 之间无重叠文本；
- 对于每个 segment，`source_end - source_start == runeLen(text)` 恒成立；
- 若某个 chunk 的全部内容已被前一 chunk 重叠覆盖（即去重后无独占文本），则该 chunk 不产生 segment。

> `content_segments` 是 `content` 文本的无重叠分区，不是参与合并的全部 chunk 的完整列表。被完全重叠覆盖的 chunk 不产生 segment，其语义内容已由覆盖它的 segment 承载。需要检索完整参与 chunk 列表的消费者应读取顶层 `sub_chunk_id` 字段。

因此消费者可基于 segment 长度累加计算子串在 `content` 中的准确位置，再获取对应 segment 的 `source_start` / `source_end` 计算原文绝对偏移。整个定位流程是 rune 级确定性的，无需依赖文本匹配或处理多个候选 segment 的二义性。

对于来自单个未经合并的 chunk 的结果，`content_segments` 仍包含一个 segment，其 `source_start` / `source_end` 与顶层的 `start_at` / `end_at` 一致。消费者可以始终使用 `content_segments` 进行定位，无需根据合并状态切换路径。

`chunk_type = "summary"` 或 `"entity"` 等生成型结果的 segment 中 `source_start` / `source_end` 均为 `0`；消费者应将其视为仅快照可用。`source_start` / `source_end` 为 `0`（即 `[0, 0)`）的 segment 的 `text` 必须非空；伪 range 不得用于在分块输入文本中切片。

> `content_segments` 中每个 segment 的 `text` 是 `content` 的真实组成部分。消费者使用 segment 信息定位时，`source_start` / `source_end` 指向的是分块输入文本，不是 `content` 本身。顶层 `start_at` / `end_at` 在存在 content 合并时不能代表 `content` 完整范围，此时应使用 `content_segments`。详见 [8.9 offset 语义](#89-offset-语义)。

### 3.5 MatchType

本端点不直接序列化仓库内部的整数 `MatchType`。响应层将内部值转换为下列字符串；无法识别的内部值统一返回 `unknown`。本端点不返回 `hybrid`。

| 值 | 说明 |
|---|---|
| `vector` | 向量语义匹配 |
| `keyword` | 关键词匹配 |
| `nearby_chunk` | 临近 Chunk 匹配 |
| `history` | 历史对话匹配 |
| `parent_chunk` | 父 Chunk 匹配 |
| `relation_chunk` | 关联 Chunk 匹配（知识图谱） |
| `graph` | 知识图谱直接匹配 |
| `web_search` | 网络搜索结果 |
| `direct_load` | 直接加载匹配（本端点不使用此类型；所有结果均经 hybrid 检索 + rerank 产出） |
| `data_analysis` | 数据分析匹配 |
| `unknown` | 无法识别的内部匹配类型 |

---

## 4. 检索管线

### 4.1 整体流程

```
QUERY_UNDERSTAND? → CHUNK_SEARCH_PARALLEL → CHUNK_RERANK → CHUNK_MERGE → MMR_SELECT_TOP_K → DISPLAY_SORT → 返回
```

`QUERY_UNDERSTAND` 仅在 `enable_query_understand: true` 时执行。其余阶段始终执行。

### 4.2 Query Understanding（查询理解）

当 `enable_query_understand: true` 时执行。注意：此阶段额外受服务端全局 `enable_rewrite` 配置控制；当全局 `enable_rewrite` 为 `false` 时，查询理解整体跳过（与 stateless chat 行为一致）。启用时，此阶段首先调用 1 次 LLM（chat model）完成查询改写和意图分类；满足实体提取条件时，再调用 1 次 LLM 提取实体。

当 `enable_query_understand: false` 时跳过整个阶段，不调用 LLM，且不读取 `history`。管线在进入后续阶段前设置 `rewrite_query` 为原始 `query`、`intent` 为 `kb_search`、实体为空。

**① 查询改写**：将口语化/模糊查询改写为检索友好形式。若 `history` 非空，利用对话历史做指代消解和上下文补充。

示例：`history` 中上轮为「登录功能怎么用」，当前 `query` 为「它怎么配置」，改写后可能是「登录功能怎么配置」。

结果写入 `rewrite_query`。

**② 意图分类**：将查询分为九种意图之一（见 [3.3 意图分类](#33-意图分类)）。下游据此决定是否跳过检索。结果写入 `intent`。

**③ 实体提取（独立的可选 LLM 调用）**：从原始 `query` 中提取关键实体（人名、产品名、术语等），供知识图谱检索使用。仅当以下条件全部满足时执行：

- `enable_query_understand=true`
- 环境变量 `NEO4J_ENABLE=true`
- 至少一个目标知识库的实体抽取配置已启用

提取的实体写入内部状态，不在响应中公开。

**模型选择**：仅在 `enable_query_understand=true` 时解析模型。传入 `chat_model_id` 时，模型 ID 精确匹配优先；未匹配 ID 时按模型名称精确匹配。按名称必须恰好匹配一个当前 Tenant 可访问且可用的 KnowledgeQA 模型；匹配多个模型返回 400，未匹配或模型不可访问/不可用返回 403。未传 `chat_model_id` 时，当前 Tenant 必须恰好配置一个默认 KnowledgeQA 模型；没有默认模型或存在多个默认模型均返回 500。查询改写与意图分类、实体提取两次调用使用同一个已解析模型。模型选择不读取目标 KB 的 `summary_model_id`。

**降级行为**：若查询改写和意图分类调用失败（网络错误、模型不可用等），或者调用结果无法解析、缺少有效字段、返回了 [3.3 意图分类](#33-意图分类) 之外的 `intent`，`rewrite_query` 回退为原始 `query`，`intent` 回退为 `kb_search`；满足实体提取条件时，实体提取仍使用原始 `query` 独立执行。若实体提取调用失败，实体为空并跳过 Entity Search。两种失败均不阻断 Chunk Search 和后续阶段。

### 4.3 Parallel Search（并行检索）

并行执行两路检索，结果合并去重：

**① Chunk Search**：使用 `rewrite_query` 做**向量 + 关键词**混合检索（hybrid vector + keyword search）。所有结果均通过 hybrid 检索产出，不使用直接加载（direct load）。检索参数（`vector_threshold`、`keyword_threshold`、`embedding_top_k`）来自 Tenant RetrievalConfig。

**② Entity Search**：使用独立实体提取调用返回的实体，在知识图谱中查找关联的 chunk。仅在实体非空时执行。内部流程：

- 对每个实体调 graph repository 搜索关联的 `GraphNode` + `GraphRelation`
- 通过 graph node 反查关联 chunk（通过 graph → chunk 映射表）
- 找到的 chunk 作为补充检索结果并入候选集

Chunk Search 是核心检索阶段。任一目标 KB / Knowledge 的 Chunk Search 执行失败时，整个请求失败并返回 500，不返回其他目标的部分结果。检索成功但没有命中不是错误。

**跳过条件**：若查询理解阶段判定无需检索（意图为 `chitchat` / `greeting` / `follow_up` / `web_search` / `image_only` / `doc_only`），整个并行检索跳过，`results` 为空数组。

### 4.4 Local Query Expansion（本地查询扩展）

当 `enable_query_expansion: true` 且初始召回不足（结果数 < `embedding_top_k`）时触发。此阶段**不调用 LLM**。

生成最多 5 个查询变体，采用本地启发式方法：

- 停用词移除（中英文常用停用词表）
- 引号内短语提取
- 分隔符切分取最长段
- 疑问词移除（`什么是` / `如何` / `怎么` / `为什么` / `哪个` / `who` / `how` 等）

对每个变体以降低的 keyword threshold（0.8 倍）再次执行混合检索，追加到候选集。并发度上限 16。

### 4.5 Rerank（重排序）

使用 rerank 模型对候选集重新打分，并按 Tenant RetrievalConfig 的 `rerank_threshold` 过滤低相关性候选。此阶段产生最终 `score`，但不执行 Top K 选择；Top K 由 Merge 后的 MMR 阶段完成。`rerank_model_id` 传入时，模型 ID 精确匹配优先；未匹配 ID 时按模型名称精确匹配（必须恰好匹配一个当前 Tenant 可访问且可用的 Rerank 模型）。未传 `rerank_model_id` 时，rerank 模型从 Tenant RetrievalConfig 解析；未配置时自动选择第一个可用的 rerank 模型。没有可用 rerank 模型、模型加载失败或 rerank 调用失败时返回 500；rerank 成功但没有候选通过阈值时返回空结果。

### 4.6 Merge（合并）

对 rerank 后的结果做合并与标准化处理：

- 按 `knowledge_id` + `chunk_type` 分组，合并重叠区间
- Parent-child 分块展开：子 chunk 的 `parent_chunk_id` 指向父 chunk，merge 阶段将子 chunk 的内容范围展开到父 chunk
- FAQ 类型结果的 `content` 为格式化后的标准问题与答案；`matched_content` 为检索实际命中的标准问题或相似问题
- 短上下文 chunk 通过 `pre_chunk_id` / `next_chunk_id` 链表扩展邻居 chunk
- 最终去重（按 chunk ID + content signature）

以上每种合并操作（重叠区间合并、父子分块展开、邻居扩展等）在修改 `content` 的同时，对应地在 `content_segments` 中追加或更新 segment，详见 [3.4.1 ContentSegment](#341-contentsegment)。

### 4.7 MMR Select Top K 与展示排序

Merge 完成后，使用 MMR（Maximal Marginal Relevance，最大边际相关性）从最终候选中选择最多 K 条结果。MMR 同时考虑候选的最终 `score` 和候选与已选结果之间的内容相似度，优先保留与查询相关且彼此不重复的结果。K 值 = Tenant RetrievalConfig 的 `rerank_top_k`。

因此，最终入选集合不保证等于按 `score` 直接排序得到的前 K 条。MMR 完成选择后，仅对已经入选的结果按以下规则排序，以提供稳定的展示顺序：

1. `score` 降序
2. `knowledge_id` 升序
3. `start_at` 升序
4. `id` 升序

展示排序不改变 MMR 已经确定的入选集合。返回数组保持该展示顺序；`chunk_index` 仅表示 Chunk 在文件中的位置，不表示响应排名。

## 5. 错误处理

### 5.1 错误响应格式

所有 HTTP 错误以非 SSE 的普通 JSON 返回，结构固定为：

```json
{
  "success": false,
  "error": {
    "code": 1000,
    "message": "query cannot be empty",
    "details": null
  }
}
```

`success` 必须为 `false`；错误响应不包含 `data`。`error.code` 为平台错误码整数，`error.message` 为人类可读文本；没有附加信息时 `error.details` 为 `null`。

### 5.2 HTTP 级错误

| 状态码 | `error.code` | 条件 |
|---:|---:|---|
| 400 | 1000 | `query` 为空、没有知识库/Knowledge/有效 Tag mention 作为检索范围、仅提供裸 `tag_ids` 而无 KB 或 Knowledge 范围、Tag 所属 KB 不在请求范围内、Tag mention 结构非法或类型不支持、`chat_model_id` 或 `rerank_model_id` 按名称匹配到多个模型、`history` 含非法 role（非 `user`/`assistant`）、`history` 超过 100 条 |
| 401 | 1001 | 缺失、无效或过期的认证凭证，或认证凭证无法解析出有效 Tenant |
| 403 | 1002 | 身份有效但 Tenant 角色低于 Viewer、API Key 既无 `retrieve` capability 也非 `full_access`，或无权访问指定的 KB / Knowledge / Tag / 模型。为防信息泄露，对无权访问和资源不存在统一返回 403，不区分具体原因 |
| 413 | 1011 | 请求体超过 10 MB |
| 429 | 1006 | 超出频率限制（见 [6. 频率限制](#6-频率限制)） |
| 500 | 1007 | Chunk Search 任一目标执行失败、没有可用 rerank 模型、rerank 模型加载/调用失败、Merge 或 MMR 失败、其他管线内部错误；或 `enable_query_understand=true`、未传 `chat_model_id` 时 Tenant 没有默认 KnowledgeQA 模型或存在多个默认 KnowledgeQA 模型；或 `rerank_model_id` 传入但在 Tenant 可访问模型中无法匹配 |
| 504 | 1009 | 请求超过服务端整体超时时间 |

### 5.3 管线级降级

Query Understanding、Entity Extraction / Entity Search 和 Local Query Expansion 是可选增强阶段，失败时按下表降级。Chunk Search、Rerank、Merge 和 MMR Select Top K 是核心阶段，失败时不降级，也不返回部分或未经完整管线处理的结果。

以下场景不返回 HTTP 错误，而是降级处理：

| 场景 | 行为 |
|---|---|
| Query Understanding 未启用 | `rewrite_query` = 原始 `query`，`intent` = `kb_search`，`history` 不参与处理，跳过实体提取和 Entity Search，继续 Chunk Search |
| 服务端全局 `enable_rewrite` 配置为 `false` | 等同于 Query Understanding 未启用（即使请求中 `enable_query_understand=true`），行为同上 |
| Query Understanding LLM 调用失败 | `rewrite_query` = 原始 `query`，`intent` = `kb_search`，管线继续 |
| Query Understanding 输出无法解析、缺少有效字段或返回未知 `intent` | `rewrite_query` = 原始 `query`，`intent` = `kb_search`，管线继续 |
| Entity Extraction LLM 调用失败 | 实体为空，跳过 Entity Search，Chunk Search 和后续阶段继续 |
| 意图判定无需检索 | `results` = `[]`，`rewrite_query` 和 `intent` 正常返回 |
| Entity Search 无可用知识图谱或执行失败 | 仅使用 Chunk Search 结果继续管线 |
| Local Query Expansion 的部分或全部扩展查询失败，或未额外召回 | 忽略失败或无新增结果的扩展，使用初始 Chunk Search 结果继续管线 |
| 检索结果为空 | `results` = `[]`，仍返回 `rewrite_query` + `intent` |
| Neo4j 未启用 | 跳过实体提取和 entity search |

## 6. 频率限制

每个 Tenant 每分钟最多 60 次请求。与 `POST /knowledge-chat-stateless` 共享配额池。

超出时返回：

```
HTTP 429 Too Many Requests
```

Retry-After header 包含建议重试秒数。

## 7. 无状态约束

此端点完全无状态，满足以下约束：

1. **不创建 Session**：不调用 `CreateSession`
2. **不创建/更新 Message**：不调用 `CreateMessage` / `UpdateMessage`
3. **不持久化检索结果**：不写入 `messages.knowledge_references`
4. **不修改已有数据**：对 Tenant、KnowledgeBase、Knowledge、Chunk 等任何已有数据零副作用
5. **`request_id` 短生命周期**：仅在单次请求期间存活，请求结束后立即释放

## 8. 约束与限制

1. **请求体大小**：最大 10 MB
2. **History 限制**：最多 100 条消息，仅 `user` / `assistant` role
3. **LLM 调用次数**：
   - `enable_query_understand: false` → 0 次 LLM 调用
   - `enable_query_understand: true` 且未触发实体提取 → 1 次 LLM 调用（查询改写 + 意图分类）
   - `enable_query_understand: true` 且触发实体提取 → 最多 2 次 LLM 调用（查询改写 + 意图分类、实体提取各 1 次）
4. **知识图谱依赖**：Entity Search 需同时满足 `enable_query_understand=true` + `NEO4J_ENABLE=true` + KB 已启用实体抽取 + 独立实体提取调用成功返回至少一个实体
5. **模型标识**：Tenant 可以配置多个 KnowledgeQA 模型，但必须恰好有一个默认 KnowledgeQA 模型。`chat_model_id` 接受模型 ID 或名称；名称必须在当前 Tenant 可访问的可用 KnowledgeQA 模型中唯一。名称匹配多个模型返回 400，ID 和名称均不匹配或模型不可访问/不可用返回 403。`enable_query_understand=false` 时不解析或校验 `chat_model_id`
6. **超时**：服务端整体超时默认 120 秒（覆盖查询理解 + 检索全流程），超过时返回 504。单次 Query Understanding / Entity Extraction LLM 调用超时按可选增强阶段失败降级；单次 LLM 调用超时由模型配置中的 `llm_call_timeout` 控制
7. **Token 裁剪**：本端点不涉及答案生成的 token 裁剪。查询改写和意图分类调用的 max completion token 为 150；实体提取是独立调用，不计入该 150 token 预算
8. **分句**：WeKnora 不对返回的 `content` 做分句。消费者可以自行拆分 `content`；`start_at` / `end_at` 仅用于定位分块输入文本中的范围，不是 `content` 内的相对偏移
9. **offset 语义**：

   `start_at` / `end_at` 使用分块输入文本的 rune 索引，表示左闭右开区间 `[start_at, end_at)`。当结果不存在可定位的源文本范围时（例如生成型 Chunk），两者均为 `0`。

   经过 Merge、邻居扩展、父子分块解析等合并阶段后，`content` 可能由多个 chunk 的文本拼接而成。顶层 `start_at` / `end_at` 保存合并前主导 chunk（首个）的原始范围，不包括后续追加 chunk 的文本覆盖范围。消费者必须使用 `content_segments` 进行精确定位：

   - 找到子串所属的 segment 在 `content_segments` 数组中的位置和偏移；
   - 使用该 segment 的 `source_start` / `source_end` 计算子串在分块输入文本中的绝对 rune 偏移；
   - 不直接将顶层 `start_at` / `end_at` 作为 `content` 的切片下标使用。

   对于来自单个未经合并的 chunk 的结果，`content_segments` 仍包含一个 segment，其 `source_start` / `source_end` 与顶层的 `start_at` / `end_at` 一致。消费者可以始终使用 `content_segments` 进行定位，无需根据合并状态切换路径。
