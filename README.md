# ai-search-v1

面向 Agent 的语义搜索 MVP（Go monorepo）。

## 环境要求

- Go **1.24+**
- 项目根目录执行：`go mod download` / `go mod tidy`

密钥与覆盖项可放在根目录 **`.env`**（勿提交），启动时会自动加载；模板见 **`.env.example`**。

---

## 配置

- 主配置：**`configs/api.yaml`**
- 指定其它路径：环境变量 **`API_CONFIG`**（绝对路径更稳妥）
- 未设置 **`API_CONFIG`** 时：使用当前工作目录下的 **`configs/api.yaml`**

---

## 部署：`cmd/api`（HTTP 服务）

编译：

```bash
go build -o api ./cmd/api
```

Windows：

```powershell
go build -o api.exe ./cmd/api
```

运行（建议在含 `configs/` 的项目根目录）：

```bash
./api
```

常用环境变量：

| 变量 | 说明 |
|------|------|
| `HTTP_ADDR` | 监听地址，覆盖 yaml 中 `http.addr`（如 `0.0.0.0:8080`） |
| `API_CONFIG` | `api.yaml` 路径 |
| `MILVUS_PASSWORD` 等 | 见 `.env.example`，与 Milvus 连接一致 |

当前暴露路由：

- `GET /healthz` — 健康检查
- `POST /v1/search` — 搜索（检索链路未接全时可能仍为占位逻辑）

**说明：** 二进制即 HTTP 进程，无需再套 uvicorn；生产可在前面加 Nginx/Caddy 做 HTTPS 与反代。

**Linux 交叉编译（在 Windows 上）：**

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -o api ./cmd/api
```

---

## 任务执行：`cmd/importer`（入库）

编译：

```bash
go build -o importer ./cmd/importer
```

执行前在 **`configs/api.yaml`** 中开启 **`embedding.enabled: true`** 并配置嵌入服务；Milvus 账号密码可用 **`.env`**。

**干跑（不连库、不嵌入）：**

```bash
./importer -config configs/api.yaml -input test.ndjson -dry-run
```

**入库（已切好块，一行一条 NDJSON）：**

```bash
./importer -config configs/api.yaml -input test.ndjson
```

**入库（对每行 `text` 做递归字符切分，`chunk_id` 多块时为 `原id#0`…）：**

```bash
./importer -config configs/api.yaml -input test.ndjson -chunk
```

其它常用参数：`-partition`、`-upsert`、`-no-flush`、`-ensure-collection=false`。

---

## 工具：`cmd/evaluator/milvuspeek`（查看 Milvus 中已入库向量抽样）

编译：

```bash
go build -o milvuspeek ./cmd/evaluator/milvuspeek
```

```bash
./milvuspeek -config configs/api.yaml
./milvuspeek -config configs/api.yaml -limit 50
./milvuspeek -config configs/api.yaml -ids "chunk_id_1,chunk_id_2"
./milvuspeek -config configs/api.yaml -no-vector
```

---

## 文档与设计

- 业务与设计说明见仓库内 **`design.md`**、**`design-tasks.md`** 等。
