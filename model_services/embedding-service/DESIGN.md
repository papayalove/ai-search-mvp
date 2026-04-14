# HuggingFace 本地 Embedding 服务（设计）

与仓库主设计 [design.md](../../design.md) §14（Qwen3-Embedding 等）及 Go 侧 [internal/model/embedding/http_embedder.go](../../internal/model/embedding/http_embedder.go) 对齐。

## 实现说明（`app.py`）

- **与 LangChain 对齐**：`EMBEDDING_MODEL` ≈ `config['path']`（Hub id 或本地目录）；`EMBEDDING_MODEL_KWARGS` ≈ `model_kwargs`。默认 **`EMBEDDING_LOADER=huggingface`**：用 `transformers` 的 `AutoTokenizer` + `AutoModel.from_pretrained` + mean pool（**不**经 SentenceTransformers，避免非 ST 目录被误包一层）。可选 **`sentence_transformers`** 加载标准 ST 模型。
- **依赖**：见 [requirements.txt](./requirements.txt)；GPU/CPU 按需安装 `torch`。
- **启动**（默认端口与 `.env.example` 中 `EMBEDDING_API_BASE_URL` 一致）：

```bash
cd model_services/embedding-service
pip install -r requirements.txt
# 可选：CPU 轮子
# pip install torch --index-url https://download.pytorch.org/whl/cpu
uvicorn app:app --host 0.0.0.0 --port 3888
```

- **环境变量**：
  - `EMBEDDING_MODEL`：HuggingFace 模型 id，默认 `BAAI/bge-m3`（与 `configs/api.yaml` 常见配置一致）。
  - `EMBEDDING_DEVICE`：`auto` | `cpu` | `cuda` | `mps`。
  - `EMBEDDING_MAX_BATCH`：单请求最大条数（默认 64）。
  - `EMBEDDING_ENCODE_BATCH_SIZE`：`encode` 内部 batch（默认 32）。
  - `EMBEDDING_NORMALIZE`：是否 L2 归一化（默认 `true`，适合检索）。
  - `EMBEDDING_TRUST_REMOTE_CODE`：部分模型需 `true`（如部分 Qwen 嵌入权重）。
  - 本进程鉴权（非空则要求 `Authorization: Bearer`）：优先 `EMBEDDING_SERVICE_API_KEY`，否则使用 `EMBEDDING_LOCAL_API_KEY`（与 Go `EMBEDDING_SOURCE=self_hosted` 时发送的 key 一致即可）。**勿用** `EMBEDDING_API_KEY`（那是 Go 调远程 API 的 outbound key）。两键皆空则本地不校验。
  - `HOST` / `PORT`：仅 `python app.py` 直连 uvicorn 时使用。

- **联调 Go**：根目录 `.env` 设 `EMBEDDING_API_BASE_URL=http://127.0.0.1:3888`，`embedding.backend: http`，`expected_dim` 与所选模型输出维度一致（如 bge-m3 为 1024）。

## 目标与边界

- 提供与现有 Go HTTP embedder **兼容的 REST 契约**（批量文本 → float 向量列表）。
- 单机部署；不负责强鉴权（可选 `API_KEY` 校验）。
- **非目标**：reranker、替代云端嵌入路径、与入库队列耦合。

## API 契约（与 Go 对齐）

- **维度**：与 `configs/api.yaml` 中 `milvus.vector_dim` 及 `embedding` 配置一致。
- **请求**：`POST`，body 含待嵌入文本列表（字段名与现有 http embedder 调用的上游约定一致；实现时以 Go 客户端序列化为准）。
- **响应**：二维 `[][]float32`（或等价 JSON），长度与输入条数一致。
- **约束**：建议单请求最大 batch、超时与 body 大小在实现中写明，并在服务端拒绝超限请求。

## 模型与运行时

- **模型**：如 Qwen3-Embedding-0.6B（以 HuggingFace 官方说明为准）；依赖版本与官方要求一致。
- **栈**：`transformers` 或 `sentence-transformers` 二选一；说明 GPU/CPU、`dtype`、模型缓存目录。
- **服务形态**：FastAPI + Uvicorn；**健康检查** `GET /healthz`（或 `/health`）。
- **关键词**：`POST /v1/keywords`（KeyBERT + `sentence-transformers`，默认 `KEYBERT_MODEL=all-MiniLM-L6-v2`），与嵌入模型配置独立；鉴权同 `/v1/embeddings`。实现见 [`keyphrase_backend.py`](./keyphrase_backend.py)。

## 与 Go 对接

- 服务地址通过根目录 **`.env`** 覆盖 yaml，例如 `EMBEDDING_API_BASE_URL`（见 [.env.example](../../.env.example)）。
- 联调：`cmd/api` / `cmd/importer` 使用 `http` backend 指向本服务。

## 部署提示

- 使用 `uv` 或 `pip` 锁依赖；容器化时挂载模型缓存卷以加速启动。
