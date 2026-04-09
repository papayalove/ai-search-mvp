# Agent-Oriented Semantic Search MVP Design

## 0. 文档导航

- 架构图版（评审）：[design-architecture.md](./design-architecture.md)
- 开发执行版（任务清单）：[design-tasks.md](./design-tasks.md)

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
- **嵌入**：Go 进程内**仅**通过 **OpenAI 兼容 HTTP** 调嵌入；可选**远端 API**或**自建 Python 服务**（`python/embedding-service`），由 **`EMBEDDING_SOURCE`** 切换（见 §6.3）。
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
- multipart 文件正文写入 Redis，**TTL 默认 3h**（测试向）；远程 job 相关元数据/状态键 **TTL 默认 24h**。
- **Worker**：`cmd/importer` 在无 `-input` 等「单次文件」参数时消费队列，调用现有 **Runner** 同时写 **Milvus 与 ES**（ES 未启用则跳过）；远程路径对 S3 对象 **流式 GetObject** 后按 NDJSON 行处理。
- 配置以环境变量为主，见仓库根目录 `.env.example`（`INGEST_*`、`S3_ENDPOINT`、`AWS_*`）。

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

Worker：`cmd/importer` **无** `-input` 时常驻消费队列，调用 **Runner** 同时写 **Milvus + ES**（与 `configs/api.yaml` 开关一致）；**带** `-input` 时仍为单次 NDJSON 文件导入（不经过队列）。细节见 §4.2.1。

### 6.3 嵌入 HTTP 客户端（Go）与 `EMBEDDING_SOURCE`

实现：[internal/config/embedding.go](internal/config/embedding.go) → [internal/model/embedding/http_embedder.go](internal/model/embedding/http_embedder.go)。`configs/api.yaml` 中 **`embedding.backend` 仅支持 `http`**；进程内 **ONNX/hugot 本地嵌入已移除**，统一走 HTTP。

**两种来源**（根目录 `.env` 的 **`EMBEDDING_SOURCE`**）：

| 取值 | 嵌入 URL 推导 | API Key（`Authorization: Bearer`） |
|------|----------------|-----------------------------------|
| `remote`（及 `api`、`cloud`） | `EMBEDDING_ENDPOINT`（完整 URL）优先；否则 `EMBEDDING_API_BASE_URL` + `/v1/embeddings`；再否则 yaml `embedding.endpoint` | **`EMBEDDING_API_KEY`**（覆盖 yaml `api_key`） |
| `self_hosted`（及 `local_service`、`python`） | `http://EMBEDDING_LOCAL_HTTP_HOST:EMBEDDING_LOCAL_HTTP_PORT/v1/embeddings`（`0.0.0.0` 在客户端侧会规范为 `127.0.0.1`） | **仅** **`EMBEDDING_LOCAL_API_KEY`**（为空则不带 Bearer；**不使用** `EMBEDDING_API_KEY`，避免与远程混用） |

请求体中的 `model` 等仍由 yaml / `EMBEDDING_API_MODEL` 等配置，须与对端服务约定一致。

**自建 Python 嵌入服务**（可选）：[python/embedding-service](python/embedding-service) 提供与上述相同的 OpenAI 风格 `POST /v1/embeddings`；进程内鉴权见该目录 `DESIGN.md`（`EMBEDDING_SERVICE_API_KEY` / `EMBEDDING_LOCAL_API_KEY`，**勿**与 Go 的 `EMBEDDING_API_KEY` 混读）。默认模型加载策略为 **`EMBEDDING_LOADER=huggingface`**（transformers `AutoModel` + mean pool），可选 `sentence_transformers`。

**冒烟脚本**：仓库根目录 `demo_call_local.py` 按 **`EMBEDDING_SOURCE`** 调用远程或本地嵌入 URL，并选用对应 Key（未设置 `EMBEDDING_SOURCE` 时脚本默认按 `self_hosted` 测本机）。

## 7. 数据导入与更新（Ingestion）

### 7.1 线上/测试主路径（Redis 队列 + Worker）

与 §4.2.1、§6.2 一致，流程为：

1. 客户端调用 Admin 入库接口 → 任务入队（multipart 正文或 S3 元数据在 Redis 中 TTL 管理）。
2. `cmd/importer` Worker 出队 → 解析 NDJSON（远程经 S3 **GetObject** 流式读）→ chunk / 实体等管线。
3. **嵌入**：通过 **HTTP** 调用配置的嵌入服务（`EMBEDDING_SOURCE` 决定远端或自建），**非**本进程直接加载 PyTorch/ONNX。
4. Runner **同时写入** Milvus 与 ES（按 yaml 启用项）。

### 7.2 单次文件导入（不经过队列）

`cmd/importer -input <path> ...`：同步读取本地 NDJSON，同样走 Runner 与同一套嵌入 HTTP 配置，适用于离线批跑与小规模试导。

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
  python/embedding-service/   # 可选：自建 OpenAI 兼容嵌入 HTTP
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

- **远端 OpenAI 兼容 API**：`EMBEDDING_SOURCE=remote`，配置 `EMBEDDING_API_BASE_URL` / `EMBEDDING_ENDPOINT` 与 `EMBEDDING_API_KEY`（§6.3）。
- **自建 Python 服务**：`EMBEDDING_SOURCE=self_hosted`，与 `python/embedding-service` 联调；支持 Hub ID 或本地 checkpoint 路径、`EMBEDDING_MODEL_KWARGS`（JSON）等，详见该目录 `DESIGN.md` 与 `README.md`。

Reranker 仍为外部 HTTP，与本节嵌入部署独立。
