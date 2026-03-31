# 语义搜索开发任务清单（执行版）

## 1. 里程碑

### M1：MVP 打通（必须完成）
- [ ] 完成 `POST /v1/search` 基础接口
- [ ] 接入 5 路 query rewrite
- [ ] 接入 ES 实体倒排召回
- [ ] 接入 Milvus 向量召回
- [ ] 完成候选合并（每路50，总250）与去重
- [ ] 接入外部 reranker
- [ ] 返回段落级证据（含来源字段）

### M2：稳定性与性能
- [ ] 超时预算与慢路降级
- [ ] request_id 全链路日志
- [ ] 指标埋点（阶段耗时、召回量、错误率）
- [ ] 缓存与并发参数调优

### M3：规模化准备
- [ ] 索引版本化与灰度切换
- [ ] 批量导入吞吐优化
- [ ] 分片与冷热分层方案预留

## 2. 模块任务分解

### 2.1 API 模块（`cmd/api`, `internal/api`）
- [ ] 定义请求/响应 DTO
- [ ] 实现 `POST /v1/search` handler
- [ ] 参数校验与错误码规范
- [ ] debug 字段开关

### 2.2 Query Pipeline（`internal/query`）
- [ ] rewrite client：5 路生成与模板约束
- [ ] entity extractor：query 实体与短语抽取
- [ ] recall orchestrator：5 路并行调度
- [ ] merger：候选合并与去重策略
- [ ] rerank adapter：外部服务封装

### 2.3 存储接入（`internal/storage`）
- [ ] ES postings 查询封装
- [ ] Milvus 向量查询封装
- [ ] metadata 读取封装
- [ ] `chunk_id` 一致性校验

### 2.4 导入链路（`cmd/importer`, `internal/ingest`）
- [ ] 网页/PDF 解析
- [ ] chunk 切分与 overlap
- [ ] 实体抽取与归一
- [ ] embedding 生成
- [ ] 同步写入 ES/Milvus/Metadata
- [ ] 导入统计报表

### 2.5 清空工具（`cmd/cleaner`, `internal/clean`）
- [ ] `dry-run` 计划输出
- [ ] `--confirm` 执行保护
- [ ] 分批删除与重试
- [ ] 清空后一致性检查

### 2.6 评测模块（`cmd/evaluator`, `internal/eval`）
- [ ] 数据集加载（BEIR/LoTTE/MIRACL）
- [ ] 对比组运行（Baseline-1/2, Target）
- [ ] 指标计算（Recall/MRR/nDCG/Hit）
- [ ] 分桶评测（实体明确/模糊/长问句等）
- [ ] 生成 `metrics.json`, `summary.md`, `hard_queries.md`

## 3. 质量门槛（DoD）

- [ ] `go list ./...` 通过
- [ ] API 集成测试通过
- [ ] 导入与清空工具 smoke test 通过
- [ ] 离线评测产物完整
- [ ] P95 延迟达标（<= 800ms，含降级策略）

## 4. 固定模型与约束

- Embedding：`Qwen3-Embedding-0.6B`
- Reranker：`Qwen3-Reranker-0.6B`
- [ ] 本轮评测不混用其它 embedding/reranker

## 5. 风险清单

- [ ] 实体抽取漏召回：保持 Milvus 并行兜底
- [ ] 多路并发延迟抖动：预算与慢路降级
- [ ] 候选重复挤占 rerank：持续监控重复率并准备回切策略
