"""
OpenAI 兼容 POST /v1/embeddings，供 Go internal/model/embedding/http_embedder.go 调用。
启动：在本目录执行  uvicorn app:app --host 0.0.0.0 --port 3888
或：python app.py
"""
from __future__ import annotations

import json
import logging
import os
import sys
from contextlib import asynccontextmanager
from typing import Annotated, Any, List, Union

_SVC_DIR = os.path.dirname(os.path.abspath(__file__))
if _SVC_DIR not in sys.path:
    sys.path.insert(0, _SVC_DIR)

import numpy as np
from fastapi import FastAPI, Header, HTTPException
from pydantic import BaseModel, Field

from embed_backend import EmbedBackend, build_backend


def _load_dotenv_files() -> None:
    """加载仓库根目录与本目录 .env（与 Go LoadDotenv 行为对齐，避免只改 .env 却不 export）。"""
    try:
        from dotenv import load_dotenv
    except ImportError:
        return
    root = os.path.abspath(os.path.join(_SVC_DIR, "..", ".."))
    load_dotenv(os.path.join(root, ".env"))
    load_dotenv(os.path.join(_SVC_DIR, ".env"), override=True)


_load_dotenv_files()

logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO"))
logger = logging.getLogger(__name__)


def _env_bool(key: str, default: bool) -> bool:
    v = os.getenv(key)
    if v is None:
        return default
    return v.strip().lower() in ("1", "true", "yes", "on")


def _looks_like_filesystem_path(s: str) -> bool:
    s = s.strip()
    if not s:
        return False
    if s.startswith("\\\\"):
        return True
    if len(s) >= 2 and s[1] == ":" and s[0].isalpha():
        return True
    if s.startswith("/") and not s.startswith("//"):
        return True
    if s.startswith(("./", ".\\", "../", "..\\")):
        return True
    return False


def _canonical_model_ref(s: str) -> str:
    """Hub id 原样；本地路径规范化（斜杠、大小写、realpath），减少 Windows 下与 JSON 转义不一致导致的误报。"""
    s = (s or "").strip()
    if not s:
        return ""
    if not _looks_like_filesystem_path(s):
        return s
    p = os.path.expandvars(os.path.expanduser(s.replace("/", os.sep)))
    try:
        return os.path.normcase(os.path.normpath(os.path.realpath(p)))
    except OSError:
        return os.path.normcase(os.path.normpath(p))


def _models_match(request_model: str, loaded_model: str) -> bool:
    if not request_model:
        return True
    a, b = _canonical_model_ref(request_model), _canonical_model_ref(loaded_model)
    if a == b:
        return True
    return False


def _coerce_model_kwargs(d: dict[str, Any]) -> dict[str, Any]:
    """将 JSON 里的 torch_dtype 等字符串转成 torch 类型（与 LangChain model_kwargs 常见写法对齐）。"""
    out = dict(d)
    td = out.get("torch_dtype")
    if isinstance(td, str) and td.strip():
        try:
            import torch

            key = td.strip().lower()
            mapping = {
                "float16": torch.float16,
                "fp16": torch.float16,
                "bfloat16": torch.bfloat16,
                "bf16": torch.bfloat16,
                "float32": torch.float32,
                "fp32": torch.float32,
                "auto": "auto",
            }
            if key in mapping:
                out["torch_dtype"] = mapping[key]
        except Exception:
            pass
    return out


def _load_model_kwargs_from_env() -> dict[str, Any]:
    """
    与 LangChain HuggingFaceEmbeddings(model_name=path, model_kwargs=config['kwargs']) 对齐：
    环境变量 EMBEDDING_MODEL_KWARGS 为 **单个 JSON 对象**（字符串）。
    """
    raw = os.getenv("EMBEDDING_MODEL_KWARGS", "").strip()
    if not raw:
        return {}
    try:
        o = json.loads(raw)
    except json.JSONDecodeError as e:
        logger.warning("EMBEDDING_MODEL_KWARGS 不是合法 JSON: %s", e)
        return {}
    if not isinstance(o, dict):
        logger.warning("EMBEDDING_MODEL_KWARGS 须为 JSON 对象，当前为 %s", type(o).__name__)
        return {}
    return _coerce_model_kwargs(o)


def _server_bearer_secret() -> str:
    """本服务要求的 Bearer：优先 EMBEDDING_SERVICE_API_KEY；未设则用 EMBEDDING_LOCAL_API_KEY（与 Go self_hosted 一致）。勿读 EMBEDDING_API_KEY。"""
    sk = os.getenv("EMBEDDING_SERVICE_API_KEY", "").strip()
    if sk:
        return sk
    return os.getenv("EMBEDDING_LOCAL_API_KEY", "").strip()


def load_runtime_config() -> dict[str, Any]:
    # HuggingFace Hub id 或本地目录路径（与 Go self_hosted 时发往 /v1/embeddings 的 model 须一致）
    model_id = os.getenv("EMBEDDING_MODEL", "BAAI/bge-m3").strip() or "BAAI/bge-m3"
    ld = os.getenv("EMBEDDING_LOADER", "huggingface").strip().lower()
    if not ld:
        ld = "huggingface"
    return {
        "model_id": model_id,
        "loader": ld,
        "device": os.getenv("EMBEDDING_DEVICE", "auto").strip().lower(),
        "max_batch": max(1, int(os.getenv("EMBEDDING_MAX_BATCH", "64"))),
        "normalize": _env_bool("EMBEDDING_NORMALIZE", True),
        "trust_remote_code": _env_bool("EMBEDDING_TRUST_REMOTE_CODE", False),
        "model_kwargs": _load_model_kwargs_from_env(),
        "api_key": _server_bearer_secret(),
        "encode_batch_size": max(1, int(os.getenv("EMBEDDING_ENCODE_BATCH_SIZE", "32"))),
        "max_seq_length": max(32, int(os.getenv("EMBEDDING_MAX_SEQ_LENGTH", "2048"))),
    }


_runtime: dict[str, Any] = {}
_backend: EmbedBackend | None = None
_model_id_loaded: str = ""


def _resolve_torch_device(spec: str) -> str:
    if spec in ("", "auto"):
        try:
            import torch

            return "cuda" if torch.cuda.is_available() else "cpu"
        except Exception:
            return "cpu"
    if spec in ("cpu", "cuda", "mps"):
        return spec
    return spec


def _parse_torch_version(v: str) -> tuple[int, int, int]:
    """'2.0.1+cu118' -> (2, 0, 1)"""
    base = v.split("+", 1)[0].strip()
    parts = base.split(".")
    nums: list[int] = []
    for p in parts[:3]:
        if p.isdigit():
            nums.append(int(p))
        else:
            break
    while len(nums) < 3:
        nums.append(0)
    return (nums[0], nums[1], nums[2])


def _require_torch_21() -> None:
    """避免 torch 2.0.x 时 transformers 半加载导致 LRScheduler NameError。"""
    try:
        import torch
    except ImportError as e:
        raise RuntimeError("未安装 PyTorch。请: pip install \"torch>=2.1\"") from e
    if _parse_torch_version(torch.__version__) < (2, 1, 0):
        raise RuntimeError(
            f"当前 PyTorch 为 {torch.__version__}，与本项目依赖的 transformers/sentence-transformers 不兼容，"
            f"需要 >=2.1.0（否则会禁用 PyTorch 并出现 LRScheduler 等导入错误）。请执行:\n"
            f"  pip install -U \"torch>=2.1\"\n"
            f"GPU 环境请到 https://pytorch.org 选择对应 CUDA 版本的安装命令。"
        )


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _backend, _model_id_loaded
    cfg = load_runtime_config()
    _runtime.clear()
    _runtime.update(cfg)
    app.state.runtime = _runtime

    _require_torch_21()

    model_id = cfg["model_id"]
    device = _resolve_torch_device(cfg["device"])
    loader = str(cfg.get("loader") or "huggingface")

    logger.info("embedding loader=%s model=%r device=%s", loader, model_id, device)
    try:
        _backend = build_backend(
            loader=loader,
            model_id=model_id,
            device=device,
            trust_remote_code=bool(cfg.get("trust_remote_code")),
            model_kwargs=dict(cfg.get("model_kwargs") or {}),
            normalize=bool(cfg.get("normalize", True)),
            max_seq_length=int(cfg.get("max_seq_length", 2048)),
        )
    except Exception as e:
        err = str(e).lower()
        if "qwen3" in err or "does not recognize this architecture" in err:
            logger.exception("model load failed")
            raise RuntimeError(
                "当前 transformers 无法识别 checkpoint 中的架构（例如 model_type=qwen3）。"
                "请升级: pip install -U \"transformers>=4.51\""
                "（Qwen 文档建议 transformers>=4.51.0）。若仍失败，可对本地 Qwen 权重设 EMBEDDING_TRUST_REMOTE_CODE=true，"
                "或在 EMBEDDING_MODEL_KWARGS 中传 trust_remote_code。"
            ) from e
        raise
    _model_id_loaded = model_id
    logger.info("ready: %s", _model_id_loaded)
    yield
    _backend = None
    _model_id_loaded = ""


app = FastAPI(title="HF Embedding Service", lifespan=lifespan)


class EmbeddingsRequest(BaseModel):
    input: Union[str, List[str]] = Field(..., description="OpenAI: string or string[]")
    model: str | None = None
    encoding_format: str | None = None


def _require_bearer(authorization: str | None, expected: str) -> None:
    if not expected:
        return
    if not authorization or not authorization.startswith("Bearer "):
        raise HTTPException(status_code=401, detail="missing or invalid Authorization")
    token = authorization[7:].strip()
    if token != expected:
        raise HTTPException(status_code=401, detail="invalid API key")


@app.post("/v1/embeddings")
def create_embeddings(
    body: EmbeddingsRequest,
    authorization: Annotated[str | None, Header()] = None,
) -> dict[str, Any]:
    cfg = getattr(app.state, "runtime", None) or _runtime
    _require_bearer(authorization, cfg.get("api_key", ""))

    if _backend is None:
        raise HTTPException(status_code=503, detail="model not loaded")

    if isinstance(body.input, str):
        texts = [body.input]
    else:
        texts = list(body.input)

    if len(texts) == 0:
        return {"object": "list", "data": [], "model": _model_id_loaded}

    max_b = int(cfg.get("max_batch", 64))
    if len(texts) > max_b:
        raise HTTPException(
            status_code=400,
            detail=f"batch size {len(texts)} exceeds EMBEDDING_MAX_BATCH={max_b}",
        )

    req_model = (body.model or "").strip()
    strict = _env_bool("EMBEDDING_STRICT_MODEL", True)
    if req_model and strict and not _models_match(req_model, _model_id_loaded):
        hint = ""
        if _looks_like_filesystem_path(req_model) and not _looks_like_filesystem_path(_model_id_loaded):
            hint = (
                " 客户端传的是本地路径，但当前进程仍加载 Hub 模型；请设环境变量 EMBEDDING_MODEL 为该路径并重启本服务。"
            )
        elif _looks_like_filesystem_path(req_model) or _looks_like_filesystem_path(_model_id_loaded):
            hint = " 若为同一目录，请检查路径是否一致（含盘符、符号链接）。"
        raise HTTPException(
            status_code=400,
            detail=(
                f"model mismatch: request {req_model!r} vs server {_model_id_loaded!r}.{hint}"
                " 开发环境可设 EMBEDDING_STRICT_MODEL=false 跳过校验（单模型部署时）。"
            ),
        )

    enc_bs = min(int(cfg.get("encode_batch_size", 32)), len(texts))
    normalize = bool(cfg.get("normalize", True))

    vectors = _backend.encode(texts, batch_size=enc_bs, normalize=normalize)
    if isinstance(vectors, np.ndarray) and vectors.ndim == 1:
        vectors = vectors.reshape(1, -1)

    data: list[dict[str, Any]] = []
    for i, row in enumerate(vectors):
        emb = row.astype("float64").tolist()
        data.append({"object": "embedding", "index": i, "embedding": emb})

    return {"object": "list", "data": data, "model": _model_id_loaded}


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {
        "status": "ok",
        "model": _model_id_loaded or None,
        "loaded": _backend is not None,
        "loader": (_runtime.get("loader") if _runtime else None),
    }


if __name__ == "__main__":
    import uvicorn

    host = os.getenv("HOST", "0.0.0.0")
    # 与 .env 中 EMBEDDING_LOCAL_HTTP_PORT 对齐，便于与 demo_call_local 同一端口约定
    port = int(os.getenv("PORT") or os.getenv("EMBEDDING_LOCAL_HTTP_PORT", "3888"))
    uvicorn.run("app:app", host=host, port=port, reload=False)
