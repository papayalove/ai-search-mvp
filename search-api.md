# Search API：`POST /v1/search`

面向语义检索的 HTTP 接口。默认服务地址以配置为准（例如 `configs/api.yaml` 中 `http.addr: ":8080"`，即 `http://127.0.0.1:8080`）。

---

## 概述

| 项 | 说明 |
| --- | --- |
| 方法 / 路径 | `POST /v1/search` |
| Content-Type | `application/json` |
| 成功响应 | `200`，JSON 体为 `SearchResponse` |
| 流式响应 | 请求体 `stream: true` 时，响应为 `text/event-stream`（SSE） |

---

## 请求头（可选）

| 头 | 值 | 说明 |
| --- | --- | --- |
| `X-Search-Debug` | `1` | 开启时，成功响应中可能包含 `debug` 字段（改写子句、各路召回数量、合并条数等，视服务端实现与配置）。 |

---

## 请求体字段

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `query` | string | 是 | 用户检索语句；首尾空白会 trim。最大长度 **4096** 字符。 |
| `top_k` | int | 否 | 返回条数上限。`0` 或省略表示默认 **10**；合法范围为 **1～100**（`0` 仅表示「用默认」）。 |
| `source_types` | string[] | 否 | 每项经 trim 并转小写后须为 `web` 或 `pdf`，否则 `400`。当前版本**仅做校验**，检索管道未据此过滤 Milvus/ES（预留字段）。 |
| `filters` | object | 否 | 任意 JSON 对象；当前公开检索路径**未强制使用**，可预留扩展。 |
| `request_id` | string | 否 | 客户端追踪 ID；若为空，服务端可能使用中间件注入的请求 ID。 |
| `retrieval` | string | 否 | 召回策略：`hybrid`（默认，向量 + ES 实体等混合）、`milvus`、`es`。具体行为依赖服务端配置（如 ES/embedding 是否启用）。 |
| `stream` | bool | 否 | `true` 时走 SSE（见下文）；`false` 或省略为单次 JSON 响应。 |

---

## 响应体字段（`200`，非流式）

根对象：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `hits` | array | 检索命中列表；无结果时为 `[]`。 |
| `debug` | object | 仅当请求带 `X-Search-Debug: 1` 且服务端填充时出现。 |

`hits[]` 中每条 `SearchHit`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `chunk_id` | string | 分片唯一标识。 |
| `doc_id` | string | 文档 ID；可能省略。 |
| `snippet` | string | 摘要/高亮片段；**纯向量等路径可能为空**，ES 召回时更可能有内容。 |
| `score` | number | 相关度分数（实现相关，仅作排序参考）。 |
| `source_type` | string | 来源类型（如 `web`、`pdf`，与入库一致）。 |
| `url_or_doc_id` | string | 展示用 URL 或文档标识。 |
| `pdf_page` | int \| null | PDF 页码；非 PDF 或未设置时可能省略或为 `null`。 |
| `title` | string | 标题；缺省时服务端可能回退为 `chunk_id` 等。 |
| `offset` | int64 | 分片在原文中的偏移（字节或字符语义以入库为准）。 |
| `page_no` | int | 逻辑页号（与 `pdf_page` 含义可能不同，以数据为准）。 |
| `source` | string | **可选**。可用于 `GET /v1/content` 拉取全文的地址（`http(s)://` 或 `s3://`，与入库 URL 对齐）；无可用源时省略。 |

`debug`（`SearchDebug`，可选）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `rewrites` | string[] | 查询改写产生的子查询等（若启用改写）。 |
| `recall_counts` | object | 各路召回条数映射（键名依实现，如 `milvus`、`es`）。 |
| `merged_count` | int | 合并去重后的中间条数等诊断信息。 |

---

## 错误响应

HTTP 状态与 JSON 体形如：

```json
{
  "error": {
    "code": "invalid_json | invalid_request | embed_required | es_disabled | search_failed | stream_not_supported",
    "message": "人类可读说明"
  }
}
```

常见情况：

- `400`：`invalid_json`（body 非 JSON）、`invalid_request`（校验失败，如缺 `query`、`top_k` 超界、`query` 超长、`source_types` 非法）。
- `503`：`embed_required`（需要向量/嵌入但未就绪）、`es_disabled`（配置关闭 ES 但请求依赖 ES）。
- `500`：`search_failed`（其他检索错误）。
- 流式模式下若 writer 不支持 flush：`500` + `stream_not_supported`。

---

## 流式模式（SSE）

当 `stream: true`：

- `Content-Type: text/event-stream; charset=utf-8`
- 事件类型与 `data`（JSON）大致顺序：
  1. `rewrite_query`：`{"query":"..."}` — 改写过程中每产生一条子查询可能推送一次（可为多次）。
  2. `rewrite_queries`：`{"queries":["原查询", ...]}` — 汇总后的查询列表。
  3. `done`：与非流式成功时相同的 `SearchResponse` JSON（`hits`、`debug` 等）。
  4. 失败时：`error`：`{"code":"...","message":"..."}`（HTTP 仍为 200 流式通道，以事件内 code 区分；与实现一致时请客户端以 `error` 事件为准）。

首行可能包含注释心跳（如 `: stream`），客户端应忽略以 `:` 开头的注释行。

---

## cURL 示例

### 1. 基础检索（JSON，默认 hybrid / 默认 top_k）

```bash
curl -sS -X POST "http://127.0.0.1:8080/v1/search" \
  -H "Content-Type: application/json" \
  -d '{"query":"什么是向量检索","top_k":5}'
```

### 2. 指定 `retrieval` 与 `request_id`，并打开 Debug

```bash
curl -sS -X POST "http://127.0.0.1:8080/v1/search" \
  -H "Content-Type: application/json" \
  -H "X-Search-Debug: 1" \
  -d '{
    "query": "Milvus 混合召回",
    "top_k": 10,
    "retrieval": "hybrid",
    "request_id": "demo-req-001"
  }'
```

### 3. 流式（SSE）

```bash
curl -sS -N -X POST "http://127.0.0.1:8080/v1/search" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{"query":"流式检索示例","stream":true,"top_k":8}'
```

Windows PowerShell 可使用相同 URL，注意 JSON 引号转义或使用 `--data-binary "@body.json"`。

---

## 返回示例

### 成功（`200`，节选）

```json
{
  "hits": [
    {
      "chunk_id": "doc-abc#00000001",
      "doc_id": "doc-abc",
      "snippet": "向量检索通过嵌入将查询与文档映射到同一空间……",
      "score": 0.87,
      "source_type": "web",
      "url_or_doc_id": "https://example.com/article",
      "title": "向量检索简介",
      "offset": 12040,
      "page_no": 0,
      "source": "https://example.com/article"
    },
    {
      "chunk_id": "pdf-xyz#00000002",
      "doc_id": "pdf-xyz",
      "snippet": "",
      "score": 0.72,
      "source_type": "pdf",
      "url_or_doc_id": "s3://bucket/reports/quarterly.pdf",
      "pdf_page": 3,
      "title": "Q1 报告",
      "offset": 5024,
      "page_no": 3,
      "source": "s3://bucket/reports/quarterly.pdf"
    }
  ]
}
```

### 成功 + Debug（请求头 `X-Search-Debug: 1`，字段示例）

```json
{
  "hits": [ ],
  "debug": {
    "rewrites": ["Milvus 混合召回", "milvus hybrid search"],
    "recall_counts": {
      "milvus": 20,
      "es": 15
    },
    "merged_count": 24
  }
}
```

### 客户端校验错误（`400`）

```json
{
  "error": {
    "code": "invalid_request",
    "message": "query is required"
  }
}
```

### SSE 片段（示意）

```text
: stream

event: rewrite_queries
data: {"queries":["用户原句","改写句1"]}

event: done
data: {"hits":[{"chunk_id":"...","snippet":"","score":0.9,"source_type":"web","url_or_doc_id":"https://...","title":"...","offset":0,"page_no":0}]}
```

---

## 相关接口

- 若 `hit.source` 存在，可用 **`GET /v1/content?source=...`**（URL 编码）拉取对应分片全文；详见内容 API 说明（若项目中有单独文档）。
