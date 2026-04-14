# 本地 Embedding HTTP 服务

为仓库内 **Go API / Importer** 提供 OpenAI 兼容的 `POST /v1/embeddings`，以及可选的 **KeyBERT 风格** `POST /v1/keywords`（关键词/关键短语）。实现见 [`app.py`](./app.py)、[`keyphrase_backend.py`](./keyphrase_backend.py)。设计细节见 [`DESIGN.md`](./DESIGN.md)。

## 环境要求

- Python 3.10+（推荐 3.11）
- 能访问 HuggingFace 下载模型，或已将模型放到本地目录并用 `EMBEDDING_MODEL` 指向该路径

## 安装依赖

```bash
cd model_services/embedding-service
python -m venv .venv

# Windows
.venv\Scripts\activate
# Linux / macOS
# source .venv/bin/activate

pip install -r requirements.txt
```

仅 CPU、减小体积时可先装 CPU 版 PyTorch，再装其余依赖：

```bash
pip install torch --index-url https://download.pytorch.org/whl/cpu
pip install -r requirements.txt
```

## 启动服务

**方式一（推荐）**：Uvicorn

```bash
cd model_services/embedding-service
uvicorn app:app --host 0.0.0.0 --port 3888
```

**方式二**：直接运行模块（使用环境变量 `HOST` / `PORT`，默认 `0.0.0.0:3888`）

```bash
cd model_services/embedding-service
python app.py
```

首次启动会按 `EMBEDDING_MODEL`（Hub id 或本地目录）加载模型，耗时取决于网络与磁盘；看到日志 `ready: ...` 即表示可对外提供服务。

## 常用环境变量

启动时会自动尝试加载 **仓库根目录** 与 **`model_services/embedding-service/.env`**（依赖 `python-dotenv`，已写入 `requirements.txt`）。未安装 `python-dotenv` 时，仍须在启动前自行 `export` / `set` 环境变量。

**注意**：`EMBEDDING_MODEL` / `EMBEDDING_DEVICE` 等由 **本 Python 进程**读取。Go 在 `EMBEDDING_SOURCE=self_hosted` 时发往 `/v1/embeddings` 的 `model` 与 **同一环境变量 `EMBEDDING_MODEL`** 对齐；远程 API 时 Go 用 **`EMBEDDING_API_MODEL`**。不一致会 400 model mismatch。

| 变量 | 说明 | 默认 |
|------|------|------|
| `EMBEDDING_MODEL` | HuggingFace 模型 ID 或本地模型目录（与 Go self_hosted 请求体 `model` 一致） | `BAAI/bge-m3` |
| `EMBEDDING_LOADER` | `huggingface`（默认）：`AutoModel` + mean pool；`sentence_transformers`：仅标准 ST 布局 | `huggingface` |
| `EMBEDDING_MODEL_KWARGS` | 单行 JSON，对齐 LangChain `model_kwargs`，传入 `AutoModel.from_pretrained`（或 ST 的 `model_kwargs`） | 空 |
| `EMBEDDING_MAX_SEQ_LENGTH` | tokenizer `max_length`（mean pool 路径） | `2048` |
| `EMBEDDING_DEVICE` | `auto` / `cpu` / `cuda` / `mps` | `auto` |
| `EMBEDDING_MAX_BATCH` | 单次请求最大文本条数 | `64` |
| `EMBEDDING_NORMALIZE` | 是否 L2 归一化 | `true` |
| `EMBEDDING_TRUST_REMOTE_CODE` | 部分模型需 `true` | `false` |
| `EMBEDDING_SERVICE_API_KEY` / `EMBEDDING_LOCAL_API_KEY` | 本服务鉴权：优先前者，否则用后者（与 Go self_hosted 的 `EMBEDDING_LOCAL_API_KEY` 对齐）。均空则不校验 | 空 |
| `HOST` / `PORT` | 仅 `python app.py` 生效 | `0.0.0.0` / `3888` |
| `KEYBERT_MODEL` / `KEYBERT_DEVICE` | 见上文「关键词提取」 | 见上文 |
| `KEYWORD_MAX_TEXT_CHARS` / `KEYWORD_STRICT_MODEL` | 见上文「关键词提取」 | 见上文 |

Windows PowerShell 示例：

```powershell
cd d:\projects\agentic_search\ai-search-mvp\model_services\embedding-service
$env:EMBEDDING_DEVICE = "cpu"
uvicorn app:app --host 0.0.0.0 --port 3888
```

## 与「远端嵌入 API」的环境变量区分

| 用途 | 变量（示例） | 谁读取 |
|------|----------------|--------|
| Go 调用的 HTTP 嵌入基址（可远端） | `EMBEDDING_API_BASE_URL`（API 根 + `/v1/embeddings`）、`EMBEDDING_API_KEY`、`EMBEDDING_API_MODEL` | `cmd/api`、`cmd/importer` |
| 本机自建服务「对外的 host:port」（文档/脚本） | `EMBEDDING_LOCAL_HTTP_HOST`、`EMBEDDING_LOCAL_HTTP_PORT` | 仓库根目录 [`demo_call_local.py`](../../demo_call_local.py)；`python app.py` 默认端口可与 `EMBEDDING_LOCAL_HTTP_PORT` 对齐 |
| 自建服务若单独设鉴权 | `EMBEDDING_LOCAL_API_KEY` | `demo_call_local.py`（可选） |
| 本进程加载的模型（及 Go self_hosted 请求的 model） | `EMBEDDING_MODEL`、`EMBEDDING_DEVICE`、`EMBEDDING_MODEL_KWARGS`（JSON） | `app.py` + Go |

要让 **Go 走自建服务**：把 `EMBEDDING_API_BASE_URL` 设为 `http://127.0.0.1:3888`（或与 `EMBEDDING_LOCAL_HTTP_*` 一致）；需要同时远端时，远端地址只写在「另一套环境」或切换 `.env`，不要和本地 demo 混读。

## 自检

```bash
curl -s http://127.0.0.1:3888/healthz
```

```bash
curl -s http://127.0.0.1:3888/v1/embeddings ^
  -H "Content-Type: application/json" ^
  -d "{\"model\":\"BAAI/bge-m3\",\"input\":[\"hello\"]}"
```

（Linux / macOS 将 `^` 换为 `\` 并写成一行即可。）

若设置了 `EMBEDDING_SERVICE_API_KEY` 或 `EMBEDDING_LOCAL_API_KEY`，请求需带头：`Authorization: Bearer <同一值>`。

## 关键词提取 `POST /v1/keywords`

与 Python `KeyBERT` 用法类似：默认句向量模型 **`all-MiniLM-L6-v2`**（`KEYBERT_MODEL`），**首次调用时下载/加载**，与 `EMBEDDING_MODEL` 独立。

| 变量 | 说明 | 默认 |
|------|------|------|
| `KEYBERT_MODEL` | Sentence-Transformers 模型 id（Hub 或本地路径） | `all-MiniLM-L6-v2` |
| `KEYBERT_DEVICE` | 未设时沿用 `EMBEDDING_DEVICE`（再默认 `auto`） | 同左 |
| `KEYWORD_MAX_TEXT_CHARS` | 单请求 `text` 最大字符数，`0` 表示不限制 | `65536` |
| `KEYWORD_STRICT_MODEL` | 为 `true` 时，若请求体带 `model`，须与 `KEYBERT_MODEL` 一致 | `false` |

**中文**建议请求里设 `"analyzer": "char_wb"`（字边界 n-gram），否则默认按「词」切分，对无空格中文效果较差。

```bash
curl -s http://127.0.0.1:3888/v1/keywords ^
  -H "Content-Type: application/json" ^
  -d "{\"text\":\"工业物联网中多模态数据用于设备故障诊断与安全监测\",\"analyzer\":\"char_wb\",\"keyphrase_ngram_range\":[1,2],\"top_n\":5}"
```

响应示例：`{"object":"keywords","model":"all-MiniLM-L6-v2","keywords":[{"keyword":"...","score":0.42},...]}`。

## 与 Go 联调

1. 保持本服务监听地址与端口一致（例如 `http://127.0.0.1:3888`）。
2. 在仓库根目录 `.env` 中设 **`EMBEDDING_SOURCE=self_hosted`**，并配置 `EMBEDDING_LOCAL_HTTP_*` / `EMBEDDING_LOCAL_API_KEY`（与 `internal/config/embedding.go` 一致）；或 `EMBEDDING_SOURCE=remote` 时用 `EMBEDDING_API_BASE_URL`（API 根，不含 `/v1/embeddings`）指向本机或远端。
3. `configs/api.yaml` 中 `embedding.backend: http`，且 **`expected_dim` 与当前模型输出维度一致**（`BAAI/bge-m3` 一般为 1024）。
4. Go 在 `self_hosted` 下会把 **`.env` 里的 `EMBEDDING_MODEL`** 原样写入请求体 `model`，须与 Python 进程加载的 id/路径一致。

随后启动 `go run ./cmd/api`（及按需的 `cmd/importer`）即可。

## 嵌入 HTTP 冒烟脚本（与 EMBEDDING_SOURCE 一致）

脚本在**仓库根目录**：[`demo_call_local.py`](../../demo_call_local.py)。

```bash
cd /path/to/ai-search-mvp   # 仓库根
pip install python-dotenv
python demo_call_local.py
python demo_call_local.py "第一句" "第二句"
```

- `EMBEDDING_SOURCE=remote`（或 `api`/`cloud`）：请求 `EMBEDDING_API_BASE_URL` + `/v1/embeddings`，`Authorization: Bearer EMBEDDING_API_KEY`。
- `EMBEDDING_SOURCE=self_hosted`（或 `local_service`/`python`）或 **未设置 SOURCE**（默认 self_hosted）：请求本地 `EMBEDDING_LOCAL_HTTP_*`，Bearer 用 `EMBEDDING_LOCAL_API_KEY` 或 `EMBEDDING_SERVICE_API_KEY`。

会加载根目录 `.env`，若存在 `model_services/embedding-service/.env` 则在其后覆盖。

## 故障排查

- **CUDA 不可用**：将 `EMBEDDING_DEVICE=cpu`，或安装带 CUDA 的 PyTorch。
- **连接 HuggingFace 超时**：配置代理，或预先下载模型到本地目录并把 `EMBEDDING_MODEL` 设为该目录。
- **Go 报 model mismatch**：`self_hosted` 时 Go 与 Python 共用 **`EMBEDDING_MODEL`** 字符串；`remote` 时用 **`EMBEDDING_API_MODEL`** 与远端约定一致。
- **`KeyError: 'qwen3'` / `does not recognize this architecture`**：checkpoint 的 `config.json` 里 `model_type` 为 `qwen3`，需要 **较新的 HuggingFace transformers**。执行：
  `pip install -U "transformers>=4.51" sentence-transformers`
  仍报错时可设 `EMBEDDING_TRUST_REMOTE_CODE=true`。注意：**Trainer 导出的 Qwen3 嵌入 checkpoint 未必能被 SentenceTransformer 直接当句向量模型用**；若升级后仍有维度/前向错误，需按 Qwen3-Embedding 官方示例用 `AutoModel` 自定义池化，或导出为兼容 ST 的格式。
- **`Disabling PyTorch because PyTorch >= 2.1 is required` / `LRScheduler` is not defined**：当前环境的 **torch 版本过低**（例如 2.0.1）。请先升级 PyTorch 再装其余依赖：`pip install -U "torch>=2.1"`（GPU 请到 [pytorch.org](https://pytorch.org) 选 CUDA 版本）。
- **Windows `OSError: [WinError 1114]` / 加载 `c10.dll` 失败**：多为本机 **PyTorch 与 CUDA/驱动不匹配**、**安装损坏**或缺少 **Visual C++ 可再发行组件（x64）**。可依次尝试：安装/更新 [VC++ Redistributable](https://learn.microsoft.com/zh-cn/cpp/windows/latest-supported-vc-redist)；在**当前** conda 环境重装与用途一致的 torch（仅 CPU 示例）：
  `pip uninstall torch torchvision torchaudio -y` 后
  `pip install torch --index-url https://download.pytorch.org/whl/cpu`；
  若需 GPU 版请到 [pytorch.org](https://pytorch.org) 按 CUDA 版本选择安装命令；更新显卡驱动后**重启**再试。与 KeyBERT 无关的其它程序若占用了同名 DLL，也可能触发 1114。
