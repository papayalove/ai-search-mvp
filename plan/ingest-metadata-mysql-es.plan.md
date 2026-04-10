---
name: 入库 Job Task 元数据与 MySQL ES 取舍
overview: 纠正 Job/Task 语义；MySQL Job + ES Task（含 started_at/finished_at）；**Task 写 ES 固定采用方案 A（bulk 占位 + partial update）**；含 DDL/mapping、§9 入库代码增改计划。
todos:
  - id: ddl-ingest-job
    content: 按 §7 建表 ingest_job；兼容目标 MySQL 版本（UTC 默认值可改应用层）
    status: pending
  - id: task-es-index
    content: 按 §8 创建 agentic_search_ingest_task_v1（started_at/finished_at）；Bulk + Update API
    status: pending
  - id: impl-code-plan
    content: 按 §9 实现方案 A：入队 bulk 占位、Worker partial update（running 写 started_at、终态写 finished_at）
    status: pending
  - id: task-storage-choice
    content: 亿级 Task 仅用 ES+Job 汇总 MySQL，避免 Task 行落 MySQL
    status: pending
  - id: worker-dual-write
    content: Worker bulk 后按文件 _update；running 带 started_at，终态带 finished_at
    status: pending
isProject: true
---

# 入库元数据：Job / Task 语义纠正与 MySQL vs ES

## 1. 业务语义（以管理端为准）

| 概念 | 定义 |
|------|------|
| **Job** | 在**管理端触发的一次入库运行**（一次入队对应一个 `job_id`）。与 [internal/queue/job.go](../internal/queue/job.go) 中「一次 Redis 队列消息」一致。 |
| **Task** | **隶属于某个 Job 的单个输入文件**（multipart 中的一个文件，或 S3 列表中的一个对象）。代码与索引中的 **`task_id` 即表示该文件级 Task**（与 [internal/queue/job.go](../internal/queue/job.go) 入队字段、`ES`/`Milvus` 中 keyword 字段一致），**无需**再引入「调用方业务 ID」与 `task_id` 的对立表述。 |

### 文件类型与「文档」粒度

| 类型 | 处理语义 |
|------|----------|
| **Markdown（.md）** | 视作文档 **1 个**逻辑源；切 chunk 后产生多条索引行，但 Task 仍对应 **一个文件**。 |
| **NDJSON / JSONL** | 一个文件内含 **多条**记录；每条记录对应管线中的一条（或多条 chunk）文档；Task 仍对应 **一个文件**，行级统计挂在 Task 元数据或 Job 汇总上。 |
| **纯 JSON 数组** | 若产品支持：可规范为「单文件多文档」与 JSONL 同类；实现时与 NDJSON 共用解析路径。 |

队列与索引侧：现有 **`job_id`** 贯穿 ES/Milvus；**`task_id` 即文件级 Task 的稳定标识**（多文件入队时 **每个文件** 应有独立 `task_id`，可由调用方传入或由 Worker/API 按文件生成），字段长度与 [configs/api.yaml](../configs/api.yaml) 中 `max_task_id_len` 等配置对齐。若仅需顺序，可额外用 `ordinal` 辅助展示，但**不与 `task_id` 语义冲突**。

### 1.1 Task 与 chunk、embedding 是否「统一」

**结论（与当前 [JobWorker](../internal/ingest/pipeline/job_worker.go) / [Runner](../internal/ingest/pipeline/runner.go) 一致）**：

- **一个 Task（一个文件）**对应 Worker 里 **一次**处理路径：要么 `RunNDJSON`（`.ndjson`/`.jsonl`），要么 `RunPlainText`（`.md`/`.txt` 等），**整文件**在同一轮调用里跑完。  
- **Chunk（切分）「统一」的含义**：对**该 Task 这一轮 Run** 使用 **同一套** 切分参数（来自 Job/队列与配置，如 `ingest.chunk_size`、`chunk_overlap`、`RecursiveChunkOptions` 等），**不会在同一个文件内部混用两套切分策略**。  
  - **不等于**「一个 Task 只有一个 chunk」：`.md` 通常被切成 **多个** chunk；`.ndjson` 往往是 **每行一条记录** 再各自成 chunk（或 `chunk_expand` 时再拆），最终 **多条 chunk 行** 共享同一个 **`task_id`**。  
- **Embedding「统一」的含义**：该 Run 使用 **同一个** `Embedder`（同一模型、同一 HTTP 端点、`expected_dim` 一致）；文本按 **batch** 多次请求嵌入服务，但 **模型与配置在整 Task 内不变**。  
- **跨 Task**：同一 Job 下多个文件依次处理时，一般 **共用** 同一 Runner 与同一套全局嵌入配置，因此 **Job 内** chunk/embedding 策略也是一致的（除非将来按文件类型在代码里分支，需在实现与文档中单列）。

**与 ES Task 文档字段**：`total_chunks` 表示该文件 Task 写入索引的 chunk 条数；`success_docs`/`fail_docs` 偏 **逻辑文档/行** 粒度，与 chunk 数可不同（见 §8）。

---

## 2. 为何先前 MySQL 设计容易「不对」

早期草案若将 **`ingest_task` 表**理解为「与文件无关的跨 Job 业务注册表」，则与代码/索引中 **`task_id` = 文件 Task** 的定义不一致；**以本文与现网字段语义为准**。

按本文定义：

- **Job 表**：与「一次管理端入库运行」一一对应，** cardinality 低**（日/周级可接受），适合 **MySQL 权威状态**。
- **Task 表（每文件一行）**：若每个 Job 含大量文件，且历史 Job 长期保留，**Task 总行数可达亿级**，MySQL 作为主存储会带来：
  - 表体积、索引维护、备份恢复成本；
  - 高频状态更新（running/succeeded）的行锁热点（若按文件更新）。

因此需要单独决策：**Task 级元数据主要放哪里**。

---

## 3. Task 级元数据：MySQL 还是 ES？

### 3.1 对比（亿级 Task 前提）

| 维度 | MySQL（每 Task 一行） | ES（每 Task 一条文档） |
|------|----------------------|-------------------------|
| 规模 | 亿级行压力大，需分区/归档/冷热分离 | 面向海量倒排与聚合，更适合「按 job_id / 状态 / 文件名」查询 |
| 典型查询 | 控制台「某 Job 下文件列表与状态」 | 同上 + 全文检索文件名、过滤失败原因 |
| 与检索索引关系 | 与 chunk 索引正交 | 可与运维查询统一技术栈（已有 ES 集群时边际成本低） |
| 强一致事务 | 强 | 近实时、最终一致 |

### 3.2 推荐策略（分阶段）

**推荐默认（含亿级 Task 风险场景）**

1. **MySQL：仅 Job 级**  
   - 字段示例：`job_id` PK、可选 `job_name`、`status`、`started_at`/`finished_at`、**汇总**（`total_files`/`success_files`/`fail_files`、`total_docs`/`success_docs`/`fail_docs`、`total_chunks`、`last_error` 截断）、`pipeline_params` JSON、`payload_type` 等（与 §7 DDL 一致）。  
   - Job 内进度通过 **计数器汇总** 更新，避免亿级子表。

2. **Task 级明细：优先 ES，而非 MySQL**  
   - 新建索引（概念名）如 **`agentic_search_ingest_task_v1`**：文档字段含 `job_id`、`task_id`、`filename`、`source_ref`、`format`、`status`、计数字段、`error_excerpt`、**`started_at`** / **`finished_at`**、`updated_at` 等（与 §8 一致；**方案 A** 见 §8.5）。  
   - Admin 控制台「Job 详情 → 文件列表」走 **ES 查询** `term job_id` + sort。  
   - 与 [design.md](../design.md) §4.2「实体倒排」索引**分离**，避免与 `entity_postings_v1` 混用 mapping。

3. **可选补强**  
   - 超高写入：Task 状态可先写 **Redis hash**（短 TTL 与队列一致）再异步 bulk 入 ES；最终以 ES 为准展示历史。  
   - 合规审计：Job 级必须在 MySQL；Task 级若需强审计，可对 ES 快照或定期导出对象存储，而不是强依赖 MySQL 亿级表。

**何时仍用 MySQL 存 Task 行**

- Task 总量有明确上限（例如每 Job &lt; 1e4 且 Job 保留期短）；或  
- 运维强制「单一关系库」；此时必须 **按 `job_id` HASH 分区** + **归档任务** 将冷 Job 迁出。

---

## 4. 与现有代码/文档的衔接

- [internal/queue/job.go](../internal/queue/job.go) 中 `TaskID` 与 `Files[]` / `S3URIs[]`：**每个文件对应一个 Task**；`task_id` 应在**文件粒度**赋值（**每个文件**独立，而非整 Job 共用一个无关 ID）。若当前入队消息仅在 Job 层带单个 `task_id`，实现阶段应扩展为 **按文件** 的 `task_id`（与 `Files` / S3 对象条目一一对应），与 ES/Milvus chunk 行一致。  
- ES/Milvus 中 **`task_id`**：**即文件级 Task**，与本文一致；**不需要**为「纠正误解」再做 `caller_task_id` 等重命名。主文档 [design.md](../design.md) §4.2 中「业务任务 ID」措辞易误导，**后续文档修订**建议改为「**文件级 Task ID（单次 Job 内单个输入文件）**」，与实现统一。

---

## 5. 验收与决策记录

- [ ] 产品确认：Admin 一次入队中，**multipart 多文件 = 多 Task**；remote **多 S3 key = 多 Task**。  
- [ ] 确认 Task 明细存储：**默认 ES 索引**；MySQL 仅 Job。  
- [ ] 若选 MySQL Task 表：需书面确认 **分区与保留策略**，否则不采纳亿级全量落库。

---

## 6. 与「检索 ES+Milvus」计划的关系

无冲突：本文仅讨论 **入库运维元数据**；检索侧实体倒排与 `agentic_search_ingest_task_*` **分索引**，职责分离。

---

## 7. MySQL：`ingest_job` 建表语句（草案）

**表名**：`ingest_job`（实现时可加库名前缀或租户前缀）。

**约定**：时间一律 **UTC**；`job_id` 与队列/API 返回的 UUID 字符串一致；`status` 与 Redis 侧状态机对齐（`queued` / `running` / `succeeded` / `failed` / `cancelled`）。

```sql
CREATE TABLE `ingest_job` (
  `job_id`            CHAR(36)     NOT NULL COMMENT '入队任务 ID，与 API 202 返回一致',
  `job_name`          VARCHAR(64)  NOT NULL COMMENT '入队任务 名称',
  `status`            VARCHAR(32)  NOT NULL DEFAULT 'queued' COMMENT 'pause|queued|running|succeeded|failed|cancelled',
  `payload_type`      VARCHAR(32)  NOT NULL COMMENT 'multipart_redis|s3，与 queue.Job.PayloadKind 一致',
  `request_id`        VARCHAR(128) NULL COMMENT 'HTTP X-Request-ID 或网关 ID',
  `started_at`        DATETIME(3)  NULL COMMENT 'Worker 开始处理 UTC',
  `finished_at`       DATETIME(3)  NULL COMMENT '终态时间 UTC',

  `total_files`       INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '本 Job 内文件(Task)总数',
  `success_files`     INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '成功文件数',
  `fail_files`      INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '失败文件数',
  `total_docs`        INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '文档数，对应docid',
  `success_docs`           BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT 'NDJSON/解析成功写入索引的行数累计',
  `fail_docs`         BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '解析或写入失败行数累计',
  `total_chunks`   BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '可选：chunk 写入条数累计',

  `last_error`        VARCHAR(2048) NULL COMMENT 'Job 级最后一次错误摘要（截断）',
  `pipeline_params`   JSON         NULL COMMENT 'partition/upsert/chunk_expand/source_type/lang 等入队快照',

  `created_at`        DATETIME(3)  NOT NULL DEFAULT (UTC_TIMESTAMP(3)),
  `updated_at`        DATETIME(3)  NOT NULL DEFAULT (UTC_TIMESTAMP(3)) ON UPDATE UTC_TIMESTAMP(3),

  PRIMARY KEY (`job_id`),
  KEY `idx_ingest_job_status_created` (`status`, `created_at`),
  KEY `idx_ingest_job_finished` (`finished_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
  COMMENT='入库 Job（管理端一次入队）元数据与汇总';
```

**说明**：

- **`job_name`**：管理端展示用短名；若无人填可与 `job_id` 前缀或「未命名」策略由产品定。  
- **`status`**：DDL 注释含 `pause` 时，与 Worker 约定是否支持暂停/恢复；未实现前可先不落库该状态。  
- **`payload_type`**：与 [queue.Job.PayloadKind](../internal/queue/job.go) 取值一致（`multipart_redis` / `s3`）；Go 字段名仍为 `PayloadKind`，ORM/DAO 层做映射即可。  
- **`total_files` / `success_files` / `fail_files`**：Worker 知晓文件列表后写 `total_files`；每完成或失败一个文件更新后两者之一。单文件 Job 则 `total_files=1`。  
- **`total_docs` / `success_docs` / `fail_docs`**：`total_docs` 可为预期/扫描得到的逻辑文档数（如 NDJSON 行数、md 记 1）；`success_docs`/`fail_docs` 为实际处理结果累计。若预期数未知，`total_docs` 可保持 `0`。  
- **`total_chunks`**：写入 Milvus/ES 的 chunk 条数累计；一期可不统计则保持 `0`。  
- **时间**：已用 `created_at` 代替「入队时刻」语义；若需与 Redis 入队时刻严格一致，可再加 `queued_at` 列或约定 `created_at` 在入队写入时即插入。  
- **MySQL 8**：`DEFAULT (UTC_TIMESTAMP(3))` 可用；若目标版本更低，改为应用层写入 `created_at`/`updated_at`。  
- **扩展**：多租户可加 `tenant_id` + 联合索引；归档可按 `finished_at` 分区（运维项）。

---

## 8. Elasticsearch：Task（文件）存储结构

### 8.1 索引与文档 ID

- **索引名（建议）**：`agentic_search_ingest_task_v1`（mapping 变更时新建 `v2` 或 reindex，与 `entity_postings_v1` 策略一致）。  
- **与 chunk 倒排索引关系**：**不同索引**；不写进 `entity_postings_*`。  
- **`_id` 策略**（二选一，实现时固定一种）：  
  - **A（推荐）**：`task_id` **在全局唯一**（可为每文件 UUID，或由 `job_id`+路径/对象键 **确定性 hash** 得到）时，**`_id` = `task_id`**，便于幂等 index/update。  
  - **B**：若 `task_id` 仅在 Job 内唯一，则 **`_id` = `{job_id}:{task_id}`**（或 URL 安全哈希），文档内仍存 `job_id`、`task_id`。

### 8.2 Mapping（properties 草案）

| 字段 | ES 类型 | 说明 |
|------|---------|------|
| `job_id` | `keyword` | 所属 Job |
| `task_id` | `keyword` | 文件级 Task，与 Milvus/ES chunk 文档中 `task_id` 一致 |
| `ordinal` | `integer` | 见下 **§8.2.1** |
| `payload_type` | `keyword` | `multipart_redis` / `s3`（与 [queue.Job.PayloadKind](../internal/queue/job.go) 一致） |
| `source_ref` | `keyword` | Redis payload key、`s3://bucket/key` 或对象 key，按需脱敏 |
| `filename` | `text` + `keyword` 子字段 | 原始文件名；聚合/排序用 `filename.keyword` |
| `format` | `keyword` | 见下 **§8.2.1**（约定取值，非 ES 硬枚举） |
| `status` | `keyword` | `queued` / `running` / `succeeded` / `failed` / `skipped` |
| `total_docs` | `long` | 可选：文件内预期行数（md 可为 1） |
| `success_docs` | `long` | 成功处理行/记录数 |
| `fail_docs` | `long` | 失败行数 |
| `total_chunks` | `long` | 可选：写入索引的 chunk 条数 |
| `error_excerpt` | `text` | 失败摘要，长度在应用层截断 |
| `created_at` | `date` | 首条文档写入时间 |
| `updated_at` | `date` | 最后更新 |
| `started_at` | `date` | **开始处理时间**：占位阶段为 `null`；Worker 将该 Task 置为 **`running`** 的第一次 `_update` 中 **必须写入** UTC（与 `finished_at` 配对算耗时） |
| `finished_at` | `date` | **终态时间**：`succeeded` / `failed` / `skipped` 时写入 UTC；`queued` / `running` 时为 `null` |

### 8.2.1 `ordinal` 与 `format` 释义

**`ordinal` 是什么**

- 表示：**在同一个 `job_id` 里，这是「第几个文件 / 第几个 Task」**，用于控制台列表 **稳定排序**（与入队时 `Files[]` 下标或 S3 keys 列表顺序一致）。  
- **0-based**：第一个文件为 `0`，第二个为 `1`，依此类推。  
- **为何可选**：若产品只依赖 `task_id` 或 `source_ref` 排序，可以不写 `ordinal`；写上后查询可简单 `sort: ordinal asc`，不必推断队列顺序。  
- **与 `task_id` 区别**：`task_id` 是稳定标识符；`ordinal` 只是 **人类可读的顺序号**，可随重新入队变化，**不作为主键**。

**`format` 是否只能写 md / ndjson / jsonl / json**

- ES 里 `keyword` **不会**在服务端限制只能取这几个值；表中列出的是 **产品约定 / MVP 建议枚举**，方便统计与筛选。  
- **可以扩展**：例如后续支持 `txt`、`html`、`pdf`（若走文件入口）等，只需在写入侧约定新取值并在文档里补充。  
- **未知扩展名**：可写实际归一化后缀（如 `csv`），或用 `unknown` / `other`，由实现统一策略。  
- **与 MIME**：若需要可同时加可选字段 `content_type`（keyword），与 `format` 并存。

**`started_at` / `finished_at`（本期与方案 A 绑定）**

- **占位（bulk，`status=queued`）**：`started_at`、`finished_at` 均为 **`null`**；可带 `created_at`/`updated_at`。  
- **开跑（`_update`）**：`status=running`，**必须设置 `started_at=now`**（UTC）。  
- **终态（`_update`）**：`status=succeeded|failed|skipped`，**必须设置 `finished_at=now`**，并刷新计数与 `error_excerpt`；`updated_at` 同步刷新。  
- **`finished_at`（Job 级）**：MySQL `ingest_job.finished_at` 仍为 **整 Job** 终态时间，与单 Task 的 `finished_at` 不同。

**JSON mapping 示例**（创建索引时用，可按集群版本微调）：

```json
{
  "settings": {
    "number_of_shards": 1,
    "number_of_replicas": 1
  },
  "mappings": {
    "properties": {
      "job_id": { "type": "keyword" },
      "task_id": { "type": "keyword" },
      "ordinal": { "type": "integer" },
      "payload_type": { "type": "keyword" },
      "source_ref": { "type": "keyword", "ignore_above": 1024 },
      "filename": {
        "type": "text",
        "fields": { "keyword": { "type": "keyword", "ignore_above": 512 } }
      },
      "format": { "type": "keyword" },
      "status": { "type": "keyword" },
      "total_docs": { "type": "long" },
      "success_docs": { "type": "long" },
      "fail_docs": { "type": "long" },
      "total_chunks": { "type": "long" },
      "error_excerpt": { "type": "text" },
      "created_at": { "type": "date" },
      "updated_at": { "type": "date" },
      "started_at": { "type": "date" },
      "finished_at": { "type": "date" }
    }
  }
}
```

### 8.3 示例文档

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "task_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "ordinal": 0,
  "payload_type": "multipart_redis",
  "source_ref": "ingest:p:550e8400-e29b-41d4-a716-446655440000:0",
  "filename": "batch.ndjson",
  "format": "ndjson",
  "status": "succeeded",
  "total_docs": null,
  "success_docs": 1200,
  "fail_docs": 3,
  "total_chunks": 1180,
  "error_excerpt": null,
  "created_at": "2026-04-09T08:00:00.000Z",
  "updated_at": "2026-04-09T08:05:12.345Z",
  "started_at": "2026-04-09T08:00:01.000Z",
  "finished_at": "2026-04-09T08:05:12.345Z"
}
```

**占位阶段（bulk 刚写入）**：同一文档在 `status=queued` 时 **`started_at`/`finished_at` 均为 `null`**，仅有 `created_at`/`updated_at`（与 §8.5 一致）。

### 8.4 常用查询（运维/控制台）

- 某 Job 下全部 Task：`{ "query": { "term": { "job_id": "<uuid>" } }, "sort": [ { "ordinal": "asc" } ] }`  
- 失败 Task：`bool` + `must` term `job_id` + `must` term `status`=`failed`  
- 文件名包含：`match` on `filename`（注意分词与中文需按集群分析器调整）  
- 按完成时间范围：`range` on `finished_at`（仅终态文档有值；需含未完成时可 `bool.should`：`exists finished_at` 与 `term status=running`）

### 8.5 Task 文档写入节奏：**本期固定方案 A（批量占位 + partial update）**

**已定案**：采用 **方案 A**，不再作为 A/B 二选一；mapping 不变，**时间字段**按上节 **`started_at` / `finished_at`** 规则写入。

**方案 A 流程**

1. **Bulk 占位**：在 **入队（API）** 或 **Worker 已拿到完整文件列表、尚未读正文** 时，对每一文件 **`_bulk` `index`** 一条文档：`status=queued`，**`started_at`/`finished_at` 为 `null`**，填好 `job_id`、`task_id`、`ordinal`、`filename`、`format`、`source_ref`、`payload_type`、`created_at`/`updated_at`。  
2. **开跑**：处理该文件前 **`_update`**：`status=running`，**`started_at=now`**（UTC），`updated_at=now`。  
3. **结束**：**`_update`**：`status=succeeded|failed|skipped`，**`finished_at=now`**，写入 `success_docs`/`fail_docs`/`total_chunks` 等，`updated_at=now`。

**S3 prefix**：对象列表 **列举完成后** 再执行步骤 1；列举前仅 MySQL Job 可见，ES 尚无 Task 文档。

**ES 能力说明**：占位使用官方 **`_bulk`**（注意逐条检查响应、`http.max_content_length`、必要时拆批）。

**与 MySQL**：`ingest_job.total_files` 与 bulk 占位 **同一时刻** 对齐写入/更新；Job 终态与 ES Task 终态计数在 §9 对齐。

---

**附录：方案 B（按顺序 lazy index，本期不采用）**

| 策略 | 说明 |
|------|------|
| **B. lazy** | 处理到文件时才首次 `index`，无 bulk 占位；**不采用**，仅作日后若需极简写入时的备选。 |

**与 MySQL**

- 若未来启用 B：Job 的 `total_files` 在 Worker 解析列表后写入；ES 与 MySQL 进度展示约定需另述。

---

## 9. 入库代码增改计划（实现顺序建议）

目标：入队时落 **MySQL `ingest_job`**；**方案 A**：**`_bulk` 占位** + 每文件 **`_update`（`running` 写 `started_at`，终态写 `finished_at`）** 写 **ES `agentic_search_ingest_task_v1`**；**`task_id` 按文件**；与现有 [JobWorker](../internal/ingest/pipeline/job_worker.go) / [Runner](../internal/ingest/pipeline/runner.go) 兼容。

### 9.1 配置与环境

- [`.env.example`](../.env.example)：`MYSQL_DSN`（或分字段 URL）、可选 `INGEST_JOB_ES_INDEX`（默认 `agentic_search_ingest_task_v1`）。**本期固定方案 A**，无需 `WRITE_MODE` 开关（若日后支持 B 再加配置）。  
- [`configs/api.yaml`](../configs/api.yaml) 或独立 [`configs/importer.yaml`](../configs/importer.yaml)：增加 `ingest_meta.mysql`、`ingest_meta.task_index`（或挂在 `elasticsearch` 下第二索引名），**与实体倒排 `index` 分离**。

### 9.2 队列与 DTO

- [`internal/queue/job.go`](../internal/queue/job.go)：`FileRef`（或并列结构）增加 **`TaskID`**（每文件）；顶层 `TaskID` 可标为废弃或仅单文件兼容。`Job` 可选 **`JobName`**。  
- 序列化 JSON 与 Redis 入队载荷同步扩展；**旧 Worker** 若读到无 `TaskID` 的文件项，Worker 内 **兜底生成**（UUID 或 hash），保证 ES 文档可写。

### 9.3 存储层

- **MySQL**（新建）：`internal/storage/mysql/` 或扩展 [`internal/storage/meta`](../internal/storage/meta/doc.go)：`IngestJobRepository` — `InsertQueued`、`UpdateRunning`、`UpdateFinished`、`IncrementFileStats` 等；`database/sql` + 迁移脚本（目录 `migrations/` 或文档内 SQL）。  
- **ES Task 索引**（新建）：`internal/storage/es/ingest_task.go`（或子包）：`EnsureIngestTaskIndex`、**`BulkIndexTasks`**（占位 `queued`）、**`UpdateTaskPartial`**（`running`+`started_at`、终态+`finished_at`）；与现有 [`Repository`](../internal/storage/es/repository.go) **共用连接与鉴权**，**不同 index 名**。类型 `IngestTaskDoc` 与 §8 mapping 对齐。

### 9.4 API 入队路径

- [`internal/api/handler/ingest.go`](../internal/api/handler/ingest.go)、[`ingest_remote.go`](../internal/api/handler/ingest_remote.go)：  
  - 生成 `job_id` 后 **INSERT `ingest_job`**（`status=queued`，填 `payload_type`、`job_name`、`pipeline_params`、`created_at` 等）。  
  - **每个文件**生成 **`task_id`**，写入 `FileRef` / 队列 JSON；**INSERT MySQL 后**同请求内 **`BulkIndexTasks`**（`status=queued`，`started_at`/`finished_at` 为空）。若 bulk 失败：记录日志并依赖 Worker 入口补 bulk（见 §9.5），策略由实现二选一并在运维文档写明。  
- 失败策略（计划建议）：MySQL 插入失败时 **记录日志 + 仍入队**（可配置严格模式改为 503），避免单点阻塞入库。

### 9.5 Worker 与 Runner

- [`internal/ingest/pipeline/job_worker.go`](../internal/ingest/pipeline/job_worker.go)：  
  - `ProcessJob` 开头：`UPDATE ingest_job SET status=running, started_at=...`（若尚未设置）；注入 `MetaWriter`（MySQL+ES）。若 API 未成功 bulk：**在解析出完整文件列表后补一次 `BulkIndexTasks`**（幂等 `index` 同 `_id`）。  
  - **每文件**：处理前 ES **`_update`**：`status=running`，**`started_at=now`**；处理结束后 **`_update`**：终态、`success_docs`/`fail_docs`/`total_chunks`、**`finished_at=now`**、`error_excerpt`、`updated_at`。**MySQL** 递增 `success_files`/`fail_files` 及 doc/chunk 汇总。  
  - `ProcessJob` 结束：`UPDATE ingest_job` 终态、`finished_at`、刷新 `last_error`（若任文件失败是否整 Job 标 `failed` 由产品定）。  
- [`internal/ingest/pipeline/runner.go`](../internal/ingest/pipeline/runner.go)：确认 `RunStats` 已含或扩展 **行级成功/失败、chunk 数**，供 Task/Job 汇总；**不在此层**直接写 MySQL，由 JobWorker 聚合，保持 Runner 无业务元数据依赖（或注入可选 callback，二选一实现时统一风格）。

### 9.6 进程入口

- [`cmd/api/main.go`](../cmd/api/main.go)（或实际 API main）：打开 MySQL、`IngestTaskES`，注入 ingest handler。  
- [`cmd/importer/main.go`](../cmd/importer/main.go)：`JobWorker` 注入同上；**无 MySQL 配置时**降级为仅日志 + ES 或仅现有行为（开关 `INGEST_META_ENABLED`）。

### 9.7 测试与运维

- ES：启动时 `EnsureIngestTaskIndex`（与 `EnsureIndex` 实体索引并列调用）。  
- 单测：`IngestJobRepository` 用 sqlite/mysql 容器；ES Task 用 mock HTTP 或 testcontainers（可选）。  
- 文档：更新 [design.md](../design.md) §6.2 / §7 简述 Job 元数据落库与 Task ES 索引名。

### 9.8 依赖顺序小结

1. 配置 + DDL 迁移 + ES mapping（§7/§8）。  
2. Go 类型与 ES/MySQL 仓储。  
3. `queue.Job` / `FileRef` 扩展与 handler 入队写库。  
4. `JobWorker` 钩子与 Runner 统计对接。  
5. `cmd` 装配与集成验证。
