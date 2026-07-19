# 无状态 Chat Endpoint 规格说明

`POST /api/v1/knowledge-chat-stateless`

## 1. 目的

提供完整的 RAG 问答能力（检索 → 重排 → 上下文拼接 → LLM 流式生成），不要求、也不产生任何服务端持久化状态。调用方自行管理对话历史和生成结果的存储。

## 2. 请求

### 2.1 请求体

```json
{
  "query": "transformer 中 self-attention 的计算复杂度是多少？",

  "knowledge_base_ids": ["kb-ml-001", "kb-dl-002"],
  "knowledge_ids": ["kn-file-12", "kn-file-34"],

  "summary_model_id": "gpt-4o",
  "web_search_enabled": false,

  "history": [
    {"role": "user",      "content": "什么是 attention 机制？"},
    {"role": "assistant", "content": "Attention 是一种让模型在处理序列时动态关注不同位置的方法..."},
    {"role": "user",      "content": "self-attention 和 cross-attention 的区别是什么？"},
    {"role": "assistant", "content": "Self-attention 的 Q/K/V 来自同一序列，cross-attention 的 Q 来自一个序列而 K/V 来自另一个..."}
  ],

  "images": [
    {"data": "data:image/png;base64,iVBOR..."}
  ],

  "attachment_uploads": [
    {"data": "...base64...", "file_name": "notes.docx", "file_size": 307200}
  ],

  "system_prompt": "请使用中文回答。回答时标注引用的课件页码。",

  "tag_ids": ["tag-impt", "tag-exam"]
}
```

### 2.2 字段语义

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `query` | string | **是** | 用户自然语言问题 |
| `knowledge_base_ids` | string[] | 否 | 限定检索的知识库。与 `knowledge_ids` 均为空时进入纯对话模式（不检索） |
| `knowledge_ids` | string[] | 否 | 进一步在 KB 内限定到具体文件。若提供则 `knowledge_base_ids` 不可为空 |
| `summary_model_id` | string | 否 | 指定对话模型。接受 UUID 或 Tenant 内唯一的模型名称，解析顺序：UUID 精确匹配优先，其次按名称查找。解析范围限定为当前 Tenant 的模型列表。不传则使用 Tenant 默认对话模型。引用不存在的模型返回 403 |
| `web_search_enabled` | boolean | 否 | 是否补充网络搜索结果。默认 false |
| `history` | Message[] | 否 | 调用方传入的历史对话轮次 |
| `images` | Image[] | 否 | 多模态图片附件（base64） |
| `attachment_uploads` | AttachmentUpload[] | 否 | 临时文件附件（不持久化到知识库，仅本次对话使用）。单个文件最大 5 MB。大文件应先通过 Knowledge API 上传至 KB 后以 `knowledge_ids` 引用 |
| `system_prompt` | string | 否 | 追加到服务端基础 System Prompt 之后的指令。系统级指令的唯一注入点——`history` 中不允许 `role: "system"` |
| `tag_ids` | string[] | 否 | 按标签过滤检索范围。作用域为 `knowledge_base_ids` 限定的 KB 集合内。仅提供 `tag_ids` 而 `knowledge_base_ids` 为空时返回 400 |

**Message 结构**：

```json
{
  "role": "user | assistant",
  "content": "消息文本"
}
```

`role` 仅允许 `user` 和 `assistant`。系统级指令通过请求体顶层的 `system_prompt` 字段传入，不允许在历史消息中混入 `system` 角色。

**Image 结构**：

```json
{
  "data": "data:image/png;base64,iVBOR..."
}
```

| 字段 | 说明 |
|------|------|
| `data` | base64 data URI（必填） |

**AttachmentUpload 结构**：

```json
{
  "data": "...base64...",
  "file_name": "notes.docx",
  "file_size": 307200
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `data` | string | base64 编码的文件内容（必填） |
| `file_name` | string | 原始文件名（必填） |
| `file_size` | int | 原始文件大小（字节）。用于解码前快速拦截明显超限的请求；服务端最终以解码后的实际字节数为准，不以声明值信任 |

### 2.3 认证与 Tenant 识别

与现有 WeKnora API 一致的认证方式：

- **JWT Bearer Token**：`Authorization: Bearer <token>`。Token 内含 `active_tenant_id`，由登录或 `/auth/switch-tenant` 时签发
- **X-API-Key**：`X-API-Key: <key>`。API Key 签发时即绑定到一个 Tenant

Tenant 完全由认证层确定，**不在请求体中传递**。服务端 Middleware 从 Token/Key 提取 tenant 后注入请求上下文，后续所有资源解析均在此 Tenant 范围内：

```
认证 Token → Tenant ID
    ├── summary_model_id   → 在 Tenant 的模型列表中查找
    ├── knowledge_base_ids → 在 Tenant 自有 KB + Organization 共享 KB 中校验
    ├── 向量存储配置        → 读取 Tenant 的 retriever 配置
    └── 频率限制            → 按 Tenant 计数
```

调用方无需感知 Tenant ID——选择合适的 JWT 或 API Key 即隐式选定了操作域。

## 3. 响应（SSE 流）

Content-Type: `text/event-stream`

### 3.1 事件序列

一次典型的有检索问答的完整事件流：

```
event: agent_query
data: {"request_id": "req-abc123", "query": "transformer 中 self-attention 的计算复杂度是多少？"}

event: tool_call
data: {"tool_call_id": "tc-1", "tool_name": "knowledge_search", "arguments": {"knowledge_base_ids": [...], "knowledge_ids": [...]}}

event: tool_result
data: {
  "tool_call_id": "tc-1",
  "tool_name": "knowledge_search",
  "output": {
    "chunks_found": 15,
    "total_duration_ms": 230
  },
  "references": [
    {
      "id": "chunk-xyz",
      "content": "The computational complexity of self-attention is O(n²·d)...",
      "knowledge_id": "kn-file-12",
      "knowledge_title": "Lecture 5: Transformers",
      ...
      // 字段结构与 final_answer.references 完全一致，此处省略
    }
  ]
}

event: answer
data: {"delta": "Self-attention"}

event: answer
data: {"delta": " 的计算复杂度是 O(n²·d)，其中 n 是序列长度"}

event: answer
data: {"delta": "，d 是每个 token 的维度..."}

event: answer
data: {"delta": ""}

event: final_answer
data: {
  "content": "Self-attention 的计算复杂度是 O(n²·d)...",
  "done": true,
  "references": [                          // 所有 tool_result 的合并结果；与各自 tool_result.references 字段结构一致
    {
      "id": "chunk-xyz",
      "content": "The computational complexity of self-attention is O(n²·d)...",
      "knowledge_id": "kn-file-12",
      "knowledge_title": "Lecture 5: Transformers",
      "knowledge_filename": "lec5-transformers.pdf",
      "chunk_index": 3,
      "start_at": 1420,
      "end_at": 1680,
      "score": 0.94,
      "match_type": "vector"
    },
    ...
  ]
}

event: complete
data: {
  "request_id": "req-abc123",
  "model_id": "gpt-4o",
  "usage": {
    "prompt_tokens": 1240,
    "completion_tokens": 89,
    "total_tokens": 1329
  },
  "elapsed_ms": 3420
}
```

### 3.2 事件类型

| 事件 | 触发时机 | data 结构 | 是否必定出现 |
|------|---------|-----------|-------------|
| `agent_query` | 请求被接受，管线开始 | `request_id`, `query` | **是** |
| `tool_call` | 进入检索阶段 | `tool_call_id`, `tool_name`, `arguments` | 有检索时出现 |
| `tool_result` | 检索完成 | `tool_call_id`, `tool_name`, `output` (含 `chunks_found`: 等于 `references.length`，作为 UI 便利汇总值; `total_duration_ms`), `references`: 完整 SearchResult 数组。客户端用 `tool_call_id` 将结果与对应的调用配对。无论后续是否中止，引用数据在此事件发出后即对客户端可用 | 有检索时出现 |
| `answer` | LLM 每次生成一个 token | `delta`: 字符串。可能为空串，用于连接保活（防止代理/CDN 在 LLM 生成长停顿期间断开连接），客户端应忽略空 `delta` 而非将其视为内容 | **是** |
| `final_answer` | 生成完成 | `content`: 完整回答, `done`: true, `references`: SearchResult[] | **是** |
| `complete` | 流正常结束 | `request_id`, `model_id`: 实际使用的模型 ID, `usage`: TokenUsage (prompt_tokens, completion_tokens, total_tokens), `elapsed_ms`: 端到端耗时 | **是** |
| `error` | 发生错误 | `code`, `message`, `request_id` | 异常时出现 |

### 3.3 SearchResult（引用）结构

`final_answer.references` 数组中的每个元素为 `SearchResult`，与 WeKnora 内部类型一致。完整字段如下：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | Chunk ID |
| `content` | string | Chunk 全文 |
| `knowledge_id` | string | 文件 Knowledge ID |
| `knowledge_title` | string | 文件标题 |
| `knowledge_filename` | string | 原始文件名 |
| `knowledge_source` | string | 知识来源类型，如 `"file"`、`"url"`、`"manual"` |
| `knowledge_description` | string | 文件描述/摘要 |
| `knowledge_base_id` | string | 所属 KB ID |
| `chunk_index` | int | Chunk 在文件中的序号 |
| `start_at` | int | 原文起始偏移（字节） |
| `end_at` | int | 原文结束偏移（字节） |
| `seq` | int | Chunk 在检索结果中的原始排序序号 |
| `score` | float | 重排序后相关性分数 |
| `match_type` | string | `vector` / `keyword` / `hybrid` |
| `chunk_type` | string | Chunk 类型：`text` / `table` / `image` 等 |
| `parent_chunk_id` | string | 父 Chunk ID（分层分块时使用） |
| `sub_chunk_id` | string[] | 子 Chunk ID 列表 |
| `image_info` | string | 图片 Chunk 的附加信息（JSON 字符串） |
| `metadata` | object | 文件级元数据，键值对 |
| `chunk_metadata` | object | Chunk 级元数据（如生成的关联问题） |
| `matched_content` | string | 实际匹配到的文本（FAQ 场景下为匹配的问题文本） |

应用层用 `knowledge_id` + `start_at` + `end_at` 实现原文高亮定位。

## 4. 纯对话模式（无检索）

当 `knowledge_base_ids` 和 `knowledge_ids` 均为空时，跳过检索管线，直接进行 LLM 对话。

事件流简化为：

```
event: agent_query
event: answer          (×N)
event: final_answer    (references = [])
event: complete
```

此时 `history` 起核心作用——它是唯一的上下文来源。

## 5. 错误处理

### 5.1 HTTP 级错误（同步返回，非 SSE）

| 状态码 | 条件 |
|--------|------|
| 400 | 请求体格式错误、`query` 为空、`knowledge_ids` 提供但 `knowledge_base_ids` 为空、仅提供 `tag_ids` 而 `knowledge_base_ids` 为空 |
| 401 | 未认证 |
| 403 | 无权访问指定的资源、或引用了不存在的 KB/Knowledge ID/模型。为防信息泄露，对无权访问和资源不存在统一返回 403，不区分 KB/模型/Knowledge 的具体原因 |
| 413 | 请求体超过 10 MB |
| 429 | 超过频率限制（每 Tenant 每分钟 60 次） |

### 5.2 SSE 流内错误

以下错误在 SSE 连接建立后的管线执行期间发生时，以流内 `error` 事件返回。429 频率限制在建立 SSE 连接前即触发，以 HTTP 429 直接返回（见 5.1）。

```
event: error
data: {
  "code": "SEARCH_FAILED | MODEL_UNAVAILABLE | STREAM_INTERRUPTED | UNKNOWN",
  "message": "向量存储连接超时",
  "request_id": "req-abc123"
}
```

错误事件后流关闭，不再有 `complete` 事件。

### 5.3 检索无结果

管线正常完成，`final_answer` 中 `references` 为空数组。LLM 应被告知"未找到相关资料"并基于自身知识回答（或按 Fallback 策略处理）。

## 6. 中止生成

### 6.1 端点

```
POST /api/v1/knowledge-chat-stateless/stop
```

### 6.2 请求体

```json
{
  "request_id": "req-abc123"
}
```

### 6.3 行为

1. 向指定 `request_id` 的管线注入停止信号
2. LLM 流式中断（若已开始生成）；检索阶段若仍在进行则一并取消
3. 在**原始 chat 的 SSE 连接**上发出一次 `final_answer` 事件：
   - 若 `tool_result` 已发出：`references` 包含完整的 SearchResult 数组，`content` 为已生成的部分文本
   - 若 `tool_result` 尚未发出（检索未完成）：`references` 为空数组 `[]`，`content` 为空字符串
   - `done` 始终为 `true`
4. 接着在原始 SSE 连接上发出 `complete` 事件后断开
5. 停止接口本身返回 200，确认停止信号已接收（此 200 是 `POST /stop` 请求的 HTTP 响应，独立于 SSE 流）
6. 对同一 `request_id` 重复调用 `POST /stop` 是幂等的，均返回 200

> `request_id` 来自 `agent_query` 事件的 `data.request_id`。

## 7. 与有状态端点的行为差异

| 行为 | 有状态 (`/knowledge-chat/:session_id`) | 无状态 (`/knowledge-chat-stateless`) |
|------|--------------------------------------|-------------------------------------|
| 会话前置条件 | Session 必须已创建 | 无需 |
| 消息持久化 | 自动保存 user + assistant 消息 | 不写入任何表 |
| 历史加载 | 从 Session 的 Message 表自动加载 | 从请求体 `history` 字段读取 |
| 标题生成 | 首次对话异步生成标题 | 不生成 |
| 停止后消息保留 | 已生成的 token 写回 Message 表 | 客户端自行保留已收到的 `answer` 事件 |
| 停止后引用可用性 | 引用持久化在 Message 中 | `tool_result` 事件提前发出引用；stop 后仍保证 `final_answer` 含 `references` |
| 聊天历史索引 | 异步索引到聊天历史 KB | 不索引 |
| `request_id` | 服务端生成 | 服务端生成，通过 `agent_query` 事件返回 |
| 引用 | 写入 `messages.knowledge_references` | 通过 `tool_result.references` 和 `final_answer.references` 返回 |

## 8. 请求示例

**带历史和文件限定的提问**：

```bash
curl -X POST https://weknora/api/v1/knowledge-chat-stateless \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{
    "query": "这个结论和上节课讲的 CNN 有什么联系？",
    "knowledge_base_ids": ["kb-229"],
    "knowledge_ids": ["kn-lec4", "kn-lec5", "kn-lec6"],
    "history": [
      {"role": "user", "content": "transformer 的优势是什么？"},
      {"role": "assistant", "content": "Transformer 相比 RNN 主要有三个优势：并行计算、长程依赖、可解释性..."}
    ],
    "system_prompt": "你是课程助教。回答控制在 200 字以内。"
  }'
```

## 9. 设计约束

1. **幂等性**：每次请求独立，重复相同请求产生一次完整的新生成（非缓存）
2. **并发**：同一 Tenant 可发起多个并发请求，互不干扰
3. **超时**：服务端整体超时默认 120 秒（覆盖检索 + LLM 生成全流程），超时后发出 `error` 事件并断开流。单次 LLM 调用超时由模型配置中的 `llm_call_timeout` 控制
4. **Token 裁剪**：`history` + `query` + `references` 的总 token 数若超过模型上下文窗口，从后向前保留最近轮次（始终保留最新对话）。调用方应自行控制 `history` 数组长度以优化 Token 消耗
5. **租约**：`request_id` 在服务端仅存活于本次请求期间。请求结束后立即释放
6. **不修改**：此端点对 WeKnora 的 Tenant、KnowledgeBase、Knowledge、Chunk 等任何已有数据**零副作用**
7. **请求体限制**：最大 10 MB。`history` 最多 100 条消息（50 轮）。单个 `attachment_uploads` 文件最大 5 MB（原始大小，服务端在 base64 解码后按实际字节数校验，不以声明值 `file_size` 为准）
8. **频率限制**：每个 Tenant 每分钟最多 60 次请求。超出返回 429
9. **模型标识**：`summary_model_id` 同时接受 UUID 和模型名称。名称唯一性由 WeKnora 现有约束保证（Tenant 内模型 `name` 不可重复），不存在歧义
