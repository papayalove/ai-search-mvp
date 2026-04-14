# Agent-Oriented Semantic Search MVP Design

## 0. 文档导航

- 架构图版（评审）：[design-architecture.md](./design-architecture.md)
- 开发执行版（任务清单）：[design-tasks.md](./design-tasks.md)
- 实现与计划同步记录：**§15**（按日期汇总，便于与 [plan/](./plan/) 对照）

本文件保留完整系统设计信息，作为主文档。

## 1. 背景与目标

在 Agent 时代，搜索系统需要直接服务任务型查询，而不仅是关键词匹配。本文档定义一个最小可用（MVP）的语义搜索系统：输入用户 query，返回最相关的 top(k) 段落级证据（支持网页与 PDF），作为 Agent 后续推理和回答的高质量上下文。

目标：
- 提供稳定的 `POST /v1/search` 检索 API。
- 支持 `5 路 query 重写 + 混合召回 + 外部 reranker`。
- 在小规模垂直语料上快速验证效果与可用性。
- 技术路线面向未来超大规模数据（百亿网页、50 亿 PDF 页）演进。

## 2. 范围与非目标

MVP 范围：
- 数据类型：网页、PDF 页面。
- 检索粒度：chunk（段落级证据）。
- 召回：实体倒排（ES）+ 向量召回（Milvus）。
- 排序：外部 reranker。
- 更新策略：日级离线更新。

非目标（后续阶段）：
- 端到端答案生成（本期只做检索与排序）。
- 近实时索引更新（分钟级）。
- 全网规模直接上线。

## 3. 总体架构

核心组件：
- API Service（Go）：对外搜索接口与查询编排；在配置 Redis 入库队列时提供异步 Admin 入库接口。
- Query Rewrite Service（外部 API）：将原 query 重写为 5 路。
- Entity Inverted Index（ES）：实体/术语到 chunk_id 的倒排映射。
- Vector Store（Milvus）：chunk 向量检索。
- Metadata Store（关系型或 KV）：chunk 文本与来源元信息。
- Reranker（外部 API）：对候选集合统一精排。
- **Redis 入库队列**：multipart 与远程 S3 引用先入队，由 Worker 消费（见 §4.2.1）。
- **S3（只读）**：远程入库任务中按对象键流式拉取 NDJSON 正文（凭证与 endpoint 见 `.env`）。
- **嵌入**：Go 进程内**仅**通过 **OpenAI 兼容 HTTP** 调嵌入；可选**远端 API**或**自建 Python 服务**（`model_services/embedding-service`），由 **`EMBEDDING_SOURCE`** 切换（见 §6.3）。
- Ingestion Pipeline（Go）：`cmd/importer` Worker 或 `-input` 单次文件，经 Runner 写 Milvus + ES。

## 4. 数据模型与索引设计

### 4.1 主键规范
- `doc_id`：原始文档唯一标识（URL 规范化哈希 / PDF 文档 ID）。
- `chunk_id`：全局唯一，建议由 `doc_id + page_no + chunk_no` 生成稳定哈希。
- 约束：ES、Milvus、Metadata Store 均使用同一 `chunk_id`。

### 4.2 ES（实体倒排）
索引建议：`entity_postings_v1`（schema 变更时请使用新索引名或 reindex，与代码中 `EnsureIndex` 行为一致）

关键字段（与实现 [internal/storage/es](internal/storage/es) 对齐）：
- `entity_keys`（keyword 数组）：归一化实体/术语，每 chunk 一条 posting 文档。
- `chunk_id`（keyword）
- `doc_id`（keyword）
- `source_type`（keyword: web/pdf）
- `lang`（keyword）
- `job_id`、`task_id`（keyword）：入库批次与业务任务 ID。
- `extra_info`（object，dynamic）：行级扩展 JSON。
- `created_time`、`update_time`（date）：首次写入与每次索引更新时间。

**实体召回查询**：`SearchByEntityKeys` 使用 `bool` + `should`（每查询键一条 `term` 打在 `entity_keys` 上），`minimum_should_match: 1`；排序链为 `_score` 降序、`update_time` 降序、`chunk_id` 升序。

说明：
- 只承担实体到 chunk 的高效候选召回，不依赖全文 BM25 主召回。

### 4.2.1 入库队列（Redis + Worker）

- `POST /v1/admin/ingest`（multipart）与 `POST /v1/admin/ingest/remote`（JSON + S3 引用）均**先入 Redis 队列**，返回 **202** 与 `job_id`。
- **队列元素与 Job**：Redis **List** 上 **`LPush` 的一条字符串** = 序列化后的 **一个 `queue.Job`**。一次 multipart 请求里多个文件仍属 **同一** `job_id`（同一 Job 的 `files[]`）；一次 remote 请求也是一个 Job（可含多条 `s3_uris` 等）。另有 `ingest:p:{job_id}:…` 等 **payload / 元数据** key，与「待消费 list」不同。
- **出队语义**：Worker 使用 **`BRPOP`**，**每条 list 元素只会被一个 consumer 弹出一次**（多 worker 并行时各自取 **不同** 的下一条；若进程在 `ProcessJob` 中途崩溃，已出队的任务不会自动回到队列，需运维或业务侧重试策略）。
- multipart 文件正文写入 Redis，**TTL 默认 3h**（测试向）；远程 job 相关元数据/状态键 **TTL 默认 24h**。
- **Worker**：`cmd/importer` 在无 `-input` 等「单次文件」参数时消费队列，调用现有 **Runner** 同时写 **Milvus 与 ES**（ES 未启用则跳过）。**S3 对象**：按 **对象 key 扩展名** 分支——**`.ndjson` / `.jsonl` / `.json`** 走按行 NDJSON；**`.txt` / `.md` / `.markdown`** 走整篇 `RunPlain`（与 multipart 一致，均再经 `ingest.chunk_*` 切分）。单对象正文读入上限与 multipart 单文件一致（**32MiB**）。若 Job 未带 `doc_id` 且为上述纯文本路径，**`doc_id`** 取 **`s3://bucket/key` 的 SHA-256 十六进制**（`pkg/util.StableDocIDFromS3Object`），以便 `RunPlain` 派生稳定 `chunk_id`。
- **多 Worker**：默认**不**持 Redis 单例锁，**多进程可并行消费**同一 list；本进程内 **`-workers` / `REDIS_INGEST_WORKER_CONCURRENCY`＞1** 时多个 **`ProcessJob` 可并行**。若须同队列仅单进程： **`IMPORTER_REQUIRE_SINGLETON_LOCK=true`** 或 **`-require-singleton-lock`**。
- 配置以环境变量为主，见仓库根目录 `.env.example`（`INGEST_*`、`S3_ENDPOINT`、`AWS_*`、`S3_ADDRESSING_STYLE`、`IMPORTER_REQUIRE_SINGLETON_LOCK` 等）。

### 4.3 Milvus（向量索引）
Collection 建议：`chunk_vectors_v1`（列变更需新建 collection 或删建）

字段：
- `chunk_id`（主键）
- `embedding`（vector<float>）
- `doc_id`、`source_type`、`lang`（VarChar）
- `job_id`、`task_id`（VarChar）
- `extra_info`（VarChar，JSON 字符串）
- `created_time`、`updated_time`（Int64，Unix 毫秒 UTC）

### 4.4 Metadata Store
建议表：`chunk_metadata`

字段：
- `chunk_id`（PK）
- `doc_id`
- `source_type`
- `title`
- `url`（web）
- `pdf_page`（pdf）
- `chunk_text`
- `token_count`
- `lang`
- `ingest_time`

## 5. 查询主链路（5 路重写）

### 5.1 流程
1. 接收 `query`。
2. 调用 rewrite 模型，生成 5 路子查询（q1..q5）。
3. 5 路并行执行混合召回：
   - 实体倒排召回（ES）
   - 向量召回（Milvus）
4. 每路输出最多 50 条候选 chunk。
5. 合并 5 路候选（上限 250），按当前策略先凑满再去重。
6. 调用外部 reranker 对候选精排。
7. 返回 top_k 段落证据。

### 5.2 重写类型建议
- 实体显式化
- 同义改写
- 背景扩展
- 约束强化
- 原 query 保真版

### 5.3 超时与降级
- 总超时预算：目标 P95 <= 800ms。
- 慢路降级：子路超时可跳过，不阻塞整体返回。
- rewrite 失败降级：使用原 query 单路召回 + rerank。

## 6. API 设计

接口：`POST /v1/search`

请求体（MVP）：
- `query` (string)
- `top_k` (int, default 10)
- `source_types` (array: web/pdf)
- `filters` (object)
- `request_id` (string)

响应体（MVP）：
- `hits[]`：
  - `chunk_id`
  - `snippet`
  - `score`
  - `source_type`
  - `url_or_doc_id`
  - `pdf_page`（可空）
  - `title`
- `debug`（可选）：
  - rewrite 列表
  - 每路召回数量
  - 合并后候选数量

### 6.2 Admin 异步入库 API

与实现 [internal/app/http.go](internal/app/http.go)、[internal/api/handler](internal/api/handler) 对齐：

| 接口 | 行为 | 前提 |
|------|------|------|
| `POST /v1/admin/ingest` | multipart 文件正文写入 Redis（TTL 默认 3h），任务入队，返回 **202** + `job_id` | `REDIS_INGEST_ENABLED=true` 且已配置 `REDIS_INGEST_URL` 或 `REDIS_INGEST_HOST`（可配 `REDIS_INGEST_PASSWORD` 等）并成功连接 Redis；否则不注册该路由 |
| `POST /v1/admin/ingest/remote` | JSON 携带 S3 引用等并入队，**202** + `job_id` | 同上 |

multipart 接受扩展名：**`.ndjson`、`.jsonl`、`.json`**（按行 NDJSON）、**`.txt`、`.md`**（整篇再走 §7.1 切分）。**无** `chunk` / `chunk_expand` 表单字段。

Worker：`cmd/importer` **无** `-input` 时常驻消费队列，调用 **Runner** 同时写 **Milvus + ES**（与 `configs/api.yaml` 开关一致）；**带** `-input` 时仍为单次 NDJSON 文件导入（不经过队列）。细节见 §4.2.1。

### 6.3 嵌入 HTTP 客户端（Go）与 `EMBEDDING_SOURCE`

实现：[internal/config/embedding.go](internal/config/embedding.go) → [internal/model/embedding/http_embedder.go](internal/model/embedding/http_embedder.go)。`configs/api.yaml` 中 **`embedding.backend` 仅支持 `http`**；进程内 **ONNX/hugot 本地嵌入已移除**，统一走 HTTP。

**两种来源**（根目录 `.env` 的 **`EMBEDDING_SOURCE`**）：

| 取值 | 嵌入 URL 推导 | API Key（`Authorization: Bearer`） |
|------|----------------|-----------------------------------|
| `remote`（及 `api`、`cloud`） | 若设置 `EMBEDDING_API_BASE_URL`（API 根，不含 path）则请求 `{BASE}/v1/embeddings`；否则使用 yaml `embedding.endpoint` | **`EMBEDDING_API_KEY`**（覆盖 yaml `api_key`） |
| `self_hosted`（及 `local_service`、`python`） | `http://EMBEDDING_LOCAL_HTTP_HOST:EMBEDDING_LOCAL_HTTP_PORT/v1/embeddings`（`0.0.0.0` 在客户端侧会规范为 `127.0.0.1`） | **仅** **`EMBEDDING_LOCAL_API_KEY`**（为空则不带 Bearer；**不使用** `EMBEDDING_API_KEY`，避免与远程混用） |

请求体中的 `model` 等仍由 yaml / `EMBEDDING_API_MODEL` 等配置，须与对端服务约定一致。

**自建 Python 嵌入服务**（可选）：[model_services/embedding-service](model_services/embedding-service) 提供与上述相同的 OpenAI 风格 `POST /v1/embeddings`；进程内鉴权见该目录 `DESIGN.md`（`EMBEDDING_SERVICE_API_KEY` / `EMBEDDING_LOCAL_API_KEY`，**勿**与 Go 的 `EMBEDDING_API_KEY` 混读）。默认模型加载策略为 **`EMBEDDING_LOADER=huggingface`**（transformers `AutoModel` + mean pool），可选 `sentence_transformers`。

**冒烟脚本**：仓库根目录 `demo_call_local.py` 按 **`EMBEDDING_SOURCE`** 调用远程或本地嵌入 URL，并选用对应 Key（未设置 `EMBEDDING_SOURCE` 时脚本默认按 `self_hosted` 测本机）。

## 7. 数据导入与更新（Ingestion）

### 7.1 线上/测试主路径（Redis 队列 + Worker）

与 §4.2.1、§6.2 一致，流程为：

1. 客户端调用 Admin 入库接口 → 任务入队（multipart 正文或 S3 元数据在 Redis 中 TTL 管理）。
2. `cmd/importer` Worker **`BRPOP` 出队** → 按载荷类型与 **S3 扩展名 / multipart 扩展名** 选择 NDJSON 行或整篇文本路径（见 §4.2.1）→ chunk / 实体等管线。
3. **嵌入**：通过 **HTTP** 调用配置的嵌入服务（`EMBEDDING_SOURCE` 决定远端或自建），**非**本进程直接加载 PyTorch/ONNX。
4. Runner **同时写入** Milvus 与 ES（按 yaml 启用项）。

**文本切分策略（与实现一致）**：NDJSON 类扩展名 **`.ndjson` / `.jsonl` / `.json`**（按行 JSON，语义同 NDJSON 流）以及 **`.txt` / `.md`** 整篇导入时，**一律**按 `configs/api.yaml` 中 **`ingest.chunk_size`、`ingest.chunk_overlap`** 等（`RecursiveChunkOptions`）对正文递归切分后再嵌入；**不提供**关闭二次切分的 API/表单/队列字段。旧队列消息中若仍含已废弃字段，Worker 侧忽略即可。

### 7.2 单次文件导入（不经过队列）

`cmd/importer -input <path> ...`：同步读取本地 **NDJSON 行文件**（扩展名与队列侧一致时含 `.json`），同样走 Runner 与同一套嵌入 HTTP 配置，适用于离线批跑与小规模试导；切分策略与 §7.1 相同（**始终**按 `ingest.chunk_*` 切分）。

### 7.3 离线日更（概念流程）

与业务「日更」一致的数据准备步骤仍可描述为：源数据 → 抽取清洗 → chunk → 实体归一化 → **经上述队列或 `-input` 写入索引**。不要求 API 与 Worker 同机。

要求：
- 幂等导入（重复执行不产生不一致）。
- 失败重试与坏样本隔离。
- 导入任务输出统计报表（成功数、失败数、耗时）。
- **Schema 变更**：Milvus / ES 字段调整多为破坏性，需新 collection / 新索引名或删建后全量重导（与实现 `EnsureCollection` / `EnsureIndex` 行为一致）。

## 8. 数据清空策略

清空工具支持：
- 维度：`es | milvus | meta | all`
- 范围：按 dataset / tenant / 时间段
- 模式：`dry-run` 默认启用，必须 `--confirm` 才执行

原则：
- 分批删除，避免长时间锁与服务抖动。
- 操作日志可审计。
- 清空后可做一致性校验。

## 9. 评测方案（重点）

### 9.1 对比组
- Baseline-1：原 query + Milvus + reranker
- Baseline-2：原 query + ES 实体倒排 + Milvus + reranker
- Target：5 路 rewrite + ES 实体倒排 + Milvus + reranker

### 9.2 公开数据集
- BEIR：通用检索基准
- LoTTE：长尾与复杂查询
- MIRACL（或 Mr.TyDi）：多语言补充

### 9.3 指标
相关性：
- `Recall@50`, `Recall@100`
- `MRR@10`
- `nDCG@10`
- `Hit@1/3/10`

系统性：
- `P50/P95/P99 latency`
- `timeout_rate`
- `empty_result_rate`
- `rewrite_fail_rate`

### 9.4 分桶评测
按 query 类型分桶输出指标：
- 实体明确型
- 实体模糊型
- 长问句
- 短关键词
- 时间敏感型

### 9.5 在线 A/B（小流量）
- A：不开启 5 路 rewrite
- B：开启 5 路 rewrite

核心观察：
- Top1 CTR / Top3 CTR
- 停留时长
- 重搜率
- 守护指标（延迟、错误率、超时率）

## 10. SLO 与可观测性

初始 SLO：
- 查询链路 P95 <= 800ms
- API 错误率 < 1%
- 空结果率受控并持续监控

可观测性：
- request_id 贯穿各阶段日志
- 分阶段打点：rewrite、ES 召回、Milvus 召回、merge、rerank
- 指标面板：延迟、召回量、rerank 输入规模、降级触发率

## 11. 工程目录（Go 与配套）

```text
仓库根/
  cmd/
    api/
    importer/          # 无 -input：队列 Worker；有 -input：单次导入
    cleaner/
    evaluator/
  internal/
    app/
    api/{handler,dto,middleware}
    queue/             # Redis 入库任务 broker
    query/{rewrite,entity,recall,rerank,pipeline}
    ingest/{source,parse,chunk,enrich,indexer,pipeline}
    storage/{es,milvus,meta,s3}   # s3：远程 ingest 只读客户端
    eval/{dataset,runner,metrics,report}
    clean/{plan,execute}
    model/{embedding,rewrite,rerank}
    config/
    observability/
  pkg/{types,util}
  model_services/embedding-service/   # 可选：自建 OpenAI 兼容嵌入 HTTP
  configs/
  demo_call_local.py          # 按 EMBEDDING_SOURCE 测嵌入 HTTP
  deployments/{docker,k8s}
  scripts/
  test/{integration,e2e}
```

环境变量约定见根目录 [`.env.example`](./.env.example)（`INGEST_*`、`EMBEDDING_SOURCE`、`EMBEDDING_*`、`S3_*`、`AWS_*` 等）。

## 12. 里程碑与演进

M1（MVP 可用）：
- 单域小语料打通完整链路
- 完成离线评测对比与第一版线上 API

M2（稳定优化）：
- 重写策略优化与候选配额调参
- 加强缓存、并发与降级策略

M3（规模化准备）：
- 索引分片、冷热分层
- 多集群与异步流水线优化

## 13. 主要风险与缓解

风险：实体抽取漏召回
- 缓解：保留 Milvus 并行召回兜底

风险：5 路并发导致延迟抖动
- 缓解：超时预算、慢路跳过、候选截断

风险：候选重复导致 rerank 有效密度下降
- 缓解：监控重复率；若效果不达标，切换为先去重再补齐策略

风险：公开数据集与业务场景偏差
- 缓解：MVP 后补充业务小样本标注集做校准

## 14. 模型选型与嵌入部署

### 14.1 评测基线（建议固定）

为确保离线评测可复现，建议基线：

- Embedding（语义向量）：`Qwen3-Embedding-0.6B`（或与之对标的统一 checkpoint）
- Reranker：`Qwen3-Reranker-0.6B`

约束：
- 在同一轮离线评测和线上 A/B 中，不混用其他 embedding 或 reranker 模型。
- 若替换模型，须记录版本并与基线对比（`Recall@k`, `MRR@10`, `nDCG@10`, `P95`）。

### 14.2 生产/开发中的嵌入服务形态

实现上 **向量维** 须与 `configs/api.yaml` 中 `milvus.vector_dim`、`embedding.expected_dim` 一致。

- **远端 OpenAI 兼容 API**：`EMBEDDING_SOURCE=remote`，配置 `EMBEDDING_API_BASE_URL`（或 yaml `embedding.endpoint`）与 `EMBEDDING_API_KEY`（§6.3）。
- **自建 Python 服务**：`EMBEDDING_SOURCE=self_hosted`，与 `model_services/embedding-service` 联调；支持 Hub ID 或本地 checkpoint 路径、`EMBEDDING_MODEL_KWARGS`（JSON）等，详见该目录 `DESIGN.md` 与 `README.md`。

Reranker 仍为外部 HTTP，与本节嵌入部署独立。

## 15. 变更与实现同步（2026-04-10）

本节汇总**当日**与近期设计文档（`plan/`）对齐的落地修改，便于评审 `design.md` 与代码的一致性。详细字段级说明仍以源码与 `plan/*.plan.md` 为准。

### 15.1 关联计划文档

| 文档 | 主题 |
|------|------|
| [plan/ingest-metadata-mysql-es.plan.md](./plan/ingest-metadata-mysql-es.plan.md) | 入库 Job/Task 元数据、MySQL `ingest_job`、ES 任务索引与 Worker 回写 |
| [plan/embedding-queue-es-storage.plan.md](./plan/embedding-queue-es-storage.plan.md) | Redis 队列载荷、Milvus/ES 双写、multipart 与 S3 入队形态 |

### 15.2 入库与队列（Go）

- **`chunk_expand` 已移除**：multipart、`POST /v1/admin/ingest/remote` DTO、`internal/queue.Job` JSON、`ingest_job.pipeline_params` 快照均**不再**包含该开关；`internal/ingest/pipeline` 中 NDJSON 与纯文本路径**始终**调用 `SplitRecord`（与 §7.1 描述一致）。
- **扩展名**：Admin multipart 与 Worker 对 **`.json`** 与 **`.ndjson` / `.jsonl`** 同等处理（按行 NDJSON 语义）；**非**标准单行 JSON 的大文件仍可能解析失败。
- **`cmd/importer`（Worker 模式）**：支持 **`IMPORTER_HTTP_ADDR`** 健康检查（默认 `:18080`，空则关闭）；默认**不**持 Redis 单例锁，**多进程可并行消费**同一队列；若需同队列仅单进程：`IMPORTER_REQUIRE_SINGLETON_LOCK` / `-require-singleton-lock`；**`-workers`** 与 **`REDIS_INGEST_WORKER_CONCURRENCY`** 控制本进程内消费协程数（可并行执行多个 `ProcessJob`）；Windows 下 **`os.Interrupt`** 与 BRPOP 超时配合退出；stderr 启动日志与空闲心跳。
- **MySQL 元数据**：Worker 在配置 **`MYSQL_DSN`** 且 Repo 可用时注入 **`ingestmeta.Service`**，更新 **`ingest_job`** 生命周期（不仅依赖 `INGEST_META_ENABLED`）；`total_docs` / `total_chunks` 等语义与 migration 注释及 Runner 统计对齐。
- **配置注释**：[configs/api.yaml](./configs/api.yaml)、[configs/importer.yaml](./configs/importer.yaml) 已改为「始终按 `ingest.chunk_*` 切分」表述。

### 15.3 Web：`heroic-web3-gateway`

- **Admin**：multipart **`job_name`** 由前端传入；批量上传可 **NDJSON 与纯文本同一请求**（已不再依赖按批拆分 `chunk` 表单）。
- **Search（`/search`）**：左侧展开菜单为无边框 **图标 + 文案** 行：**新对话**、**Knowledge**（`/admin`）、**API**（`VITE_API_BASE_URL` 去尾斜杠后的 `/v1`，未配置则用相对 `/v1` 新标签打开）；**history** 为小节标题，其下列表仅在已有用户检索记录时渲染；**无**「历史搜索」入口与空列表时的占位图标/提示文案。

### 15.4 后续文档维护约定

- 行为型变更（API 字段、队列 JSON、默认策略）应同时更新 **§6–§7** 与本节 **§15** 日期条目。
- 任务级勾选仍以 [design-tasks.md](./design-tasks.md) 为准；架构图以 [design-architecture.md](./design-architecture.md) 为准。

### 15.5 变更与实现同步（2026-04-13）

- **§4.2.1 队列与 Worker**：补充 **「一条 list 元素 = 一个 `queue.Job`」**、**`BRPOP` 单消费者语义**、**S3 按扩展名 NDJSON / 整篇**、**无 `doc_id` 时 S3 路径哈希**、**默认多 worker 并行**与 **`IMPORTER_REQUIRE_SINGLETON_LOCK`** 可选单进程锁。
- **Admin multipart / remote 默认 `upsert`**：未传或省略时 Milvus 侧默认 **Upsert**；显式 `false` 为 Insert（见 handler 与 `cmd/importer` `-upsert` 默认值）。
- **S3 客户端**：未配置 **`AWS_REGION`** 时由 **`EffectiveRegion`** 使用占位 **`us-east-1`** 满足 SDK；**`S3_ADDRESSING_STYLE=path`** / `virtual` 与自定义 **`S3_ENDPOINT`** 行为见 `internal/storage/s3/config.go`。
- **目录**：自建嵌入 HTTP 由 **`model_services/embedding-service`** 承载（原 `python/embedding-service`）；文档、`demo_call_local.py` 与配置注释中的路径已同步。
