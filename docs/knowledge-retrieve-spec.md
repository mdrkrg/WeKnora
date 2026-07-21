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
| Filter Top K | yes | yes | yes |
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
| `tag_ids` | string[] | 否 | — | Tag ID 列表，用于 KB 内过滤。仅在同时指定 `knowledge_base_id[s]` 时生效 |
| `mentioned_items` | MentionedItem[] | 否 | — | scoped tag mentions，每个 item 指定绑定的 KB 上下文 |
| `enable_query_understand` | bool | 否 | `true` | 是否启用 LLM 查询理解（查询改写 + 意图分类 + 实体提取） |
| `enable_query_expansion` | bool | 否 | `true` | 是否启用本地查询扩展（低召回时触发，无 LLM 调用） |
| `chat_model_id` | string | 否 | 自动选择 | 覆盖查询理解阶段使用的 chat model。不传时自动选择 Tenant 默认 KnowledgeQA 模型。接受 UUID 或 Tenant 内唯一的模型名称 |
| `history` | HistoryMessage[] | 否 | `[]` | 对话历史，用于查询改写的多轮上下文消解 |

> 必须指定 `knowledge_base_id` / `knowledge_base_ids` / `knowledge_ids` / `tag_ids` 中的至少一个。

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

### 2.6 认证

支持以下认证方式：

- `X-API-Key` header（API Key 必须有 `retrieve` 能力）
- Session cookie（已登录用户）

认证失败返回 401。无权访问目标 KB / Knowledge / 模型返回 403。

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
        "seq": 1,
        "score": 0.95,
        "match_type": "hybrid",
        "chunk_type": "text",
        "parent_chunk_id": "",
        "sub_chunk_id": [],
        "image_info": "",
        "metadata": {},
        "chunk_metadata": {},
        "matched_content": ""
      }
    ]
  }
}
```

### 3.2 顶层字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `success` | bool | 请求是否成功 |
| `data.rewrite_query` | string | LLM 改写后的查询。未启用查询理解时等于原始 `query` |
| `data.intent` | string | 意图分类结果。可能的值见 [3.3 意图分类](#33-意图分类) |
| `data.results` | SearchResult[] | 检索结果数组。字段结构见 [3.4 SearchResult](#34-searchresult) |

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

当意图判定为无需检索时（如 `chitchat`），管线跳过所有检索阶段，`results` 返回空数组，`rewrite_query` 和 `intent` 正常返回。消费者可通过 `intent` 决定是否将 query 直接传递给 LLM。

### 3.4 SearchResult

`results` 数组中每个元素的完整字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | Chunk ID |
| `content` | string | Chunk 全文 |
| `knowledge_id` | string | 所属 Knowledge（文件）ID |
| `chunk_index` | int | Chunk 在文件中的序号 |
| `knowledge_title` | string | 文件标题 |
| `start_at` | int | Chunk 在原文中的起始偏移**（rune 索引，非字节）** |
| `end_at` | int | Chunk 在原文中的结束偏移**（rune 索引，非字节）** |
| `seq` | int | 检索结果中的原始排序序号 |
| `score` | float | Rerank 后归一化的最终相关性分数 |
| `match_type` | string | 匹配类型。见 [3.5 MatchType](#35-matchtype) |
| `sub_chunk_id` | string[] | 子 Chunk ID 列表（parent-child 分块时使用） |
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

### 3.5 MatchType

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
| `direct_load` | 直接加载匹配 |
| `data_analysis` | 数据分析匹配 |

---

## 4. 检索管线

### 4.1 整体流程

```
QUERY_UNDERSTAND? → CHUNK_SEARCH_PARALLEL → CHUNK_RERANK → CHUNK_MERGE → FILTER_TOP_K → 返回
```

`QUERY_UNDERSTAND` 仅在 `enable_query_understand: true` 时执行。其余阶段始终执行。

### 4.2 Query Understanding（查询理解）

当 `enable_query_understand: true` 时执行。此阶段调用 1 次 LLM（chat model），做三项处理：

**① 查询改写**：将口语化/模糊查询改写为检索友好形式。若 `history` 非空，利用对话历史做指代消解和上下文补充。

示例：`history` 中上轮为「登录功能怎么用」，当前 `query` 为「它怎么配置」，改写后可能是「登录功能怎么配置」。

结果写入 `rewrite_query`。

**② 意图分类**：将查询分为九种意图之一（见 [3.3 意图分类](#33-意图分类)）。下游据此决定是否跳过检索。结果写入 `intent`。

**③ 实体提取**：从查询中提取关键实体（人名、产品名、术语等），供知识图谱检索使用。仅当以下条件全部满足时执行：

- 环境变量 `NEO4J_ENABLE=true`
- 至少一个目标知识库的实体抽取配置已启用

提取的实体写入内部状态，不在响应中公开。

**模型选择**：使用 `chat_model_id` 指定的模型；未指定时自动选择 Tenant 默认 KnowledgeQA 模型（遍历 KB 的 `summary_model_id`，优先 Remote 来源模型，否则取第一个 KB 的配置模型）。

**降级行为**：若 LLM 调用失败（网络错误、模型不可用等），`rewrite_query` 回退为原始 `query`，`intent` 回退为 `kb_search`，实体为空。管线继续执行后续阶段，不中断请求。

### 4.3 Parallel Search（并行检索）

并行执行两路检索，结果合并去重：

**① Chunk Search**：使用 `rewrite_query` 做**向量 + 关键词**混合检索。检索参数（`vector_threshold`、`keyword_threshold`、`embedding_top_k`）来自 Tenant RetrievalConfig。

**② Entity Search**：使用查询理解阶段提取的实体，在知识图谱中查找关联的 chunk。仅在实体非空时执行。内部流程：

- 对每个实体调 graph repository 搜索关联的 `GraphNode` + `GraphRelation`
- 通过 graph node 反查关联 chunk（通过 graph → chunk 映射表）
- 找到的 chunk 作为补充检索结果并入候选集

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

使用 rerank 模型对合并后的候选集重新打分排序。rerank 模型从 Tenant RetrievalConfig 解析；未配置时自动选择第一个可用的 rerank 模型。

### 4.6 Merge（合并）

对 rerank 后的结果做合并与标准化处理：

- 按 `knowledge_id` + `chunk_type` 分组，合并重叠区间
- Parent-child 分块展开：子 chunk 的 `parent_chunk_id` 指向父 chunk，merge 阶段将子 chunk 的内容范围展开到父 chunk
- FAQ 类型 chunk 填充答案文本（`content` 为问题，`matched_content` 为匹配的问题文本）
- 短上下文 chunk 通过 `pre_chunk_id` / `next_chunk_id` 链表扩展邻居 chunk
- 最终去重（按 chunk ID + content signature）

### 4.7 Filter Top K

按 rerank 分数降序排序，取 Top K 条结果。K 值 = Tenant RetrievalConfig 的 `rerank_top_k`。

## 5. 错误处理

### 5.1 HTTP 级错误

所有 HTTP 错误以非 SSE 的普通 JSON 返回：

| 状态码 | 条件 |
|---|---|
| 400 | `query` 为空、无 `knowledge_base_ids` / `knowledge_ids` / `tag_ids`、仅提供 `tag_ids` 而无 KB、`history` 含非法 role（非 `user`/`assistant`）、`history` 超过 100 条 |
| 401 | 未认证 |
| 403 | 无权访问指定的 KB / Knowledge / 模型。为防信息泄露，对无权访问和资源不存在统一返回 403，不区分具体原因 |
| 413 | 请求体超过 10 MB |
| 429 | 超出频率限制（见 [6. 频率限制](#6-频率限制)） |
| 500 | 管线内部错误 |

### 5.2 管线级降级

以下场景不返回 HTTP 错误，而是降级处理：

| 场景 | 行为 |
|---|---|
| Query Understanding LLM 调用失败 | `rewrite_query` = 原始 `query`，`intent` = `kb_search`，管线继续 |
| 意图判定无需检索 | `results` = `[]`，`rewrite_query` 和 `intent` 正常返回 |
| Entity Search 无可用知识图谱 | 仅返回 chunk search 结果 |
| Local Query Expansion 未额外召回 | 不追加结果，管线继续 |
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
   - `enable_query_understand: true` → 1 次 LLM 调用（查询理解），token 预算 max 150
   - `enable_query_understand: false` → 0 次 LLM 调用
4. **知识图谱依赖**：Entity Search 需同时满足 `NEO4J_ENABLE=true` + KB 已启用实体抽取 + 查询理解成功提取实体
5. **模型标识**：`chat_model_id` 接受 UUID 或模型名称。名称唯一性由 Tenant 内模型 `name` 不可重复保证。若两者都不匹配，返回 403
6. **超时**：服务端整体超时默认 120 秒（覆盖查询理解 + 检索全流程）。单次 LLM 调用超时由模型配置中的 `llm_call_timeout` 控制
7. **Token 裁剪**：本端点不涉及答案生成的 token 裁剪。查询理解阶段有独立的 max token 限制（150）
8. **分句**：WeKnora 不做分句。消费者根据 `content` + `start_at` / `end_at` 自行处理句子拆分和高亮定位
9. **offset 语义**：`start_at` / `end_at` 为 rune 索引（非 UTF-8 字节），与原文 `content[start_at:end_at]` 字符级一致
