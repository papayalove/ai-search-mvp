#!/usr/bin/env python3
"""
按根目录 .env 的 EMBEDDING_SOURCE 对嵌入 HTTP 做冒烟测试（与 Go internal/config/embedding.go 一致）：
  - remote（及 api、cloud）→ EMBEDDING_ENDPOINT 或 EMBEDDING_API_BASE_URL + /v1/embeddings，Bearer 用 EMBEDDING_API_KEY
  - self_hosted（及 local_service、python；或未设置 SOURCE 时默认 self_hosted）→ 本地 host:port，Bearer 用 EMBEDDING_LOCAL_API_KEY 或 EMBEDDING_SERVICE_API_KEY

用法（须在仓库根目录）：
  python demo_call_local.py
  python demo_call_local.py "你好" "世界"
"""
from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request


def _http_client_host(host: str) -> str:
    """0.0.0.0 / :: 只能用于监听，不能作为本机 HTTP 客户端的目标（Windows 会 WinError 10049）。"""
    h = (host or "").strip()
    if h in ("0.0.0.0", "::", ""):
        return "127.0.0.1"
    return h


def _load_dotenv() -> None:
    try:
        from dotenv import load_dotenv
    except ImportError:
        return
    root = os.path.dirname(os.path.abspath(__file__))
    load_dotenv(os.path.join(root, ".env"))
    emb = os.path.join(root, "python", "embedding-service", ".env")
    if os.path.isfile(emb):
        load_dotenv(emb, override=True)


def _embedding_source() -> str:
    s = os.getenv("EMBEDDING_SOURCE", "").strip().lower()
    if not s:
        return "self_hosted"
    return s


def main() -> int:
    _load_dotenv()

    src = _embedding_source()
    health_base: str | None = None

    if src in ("remote", "api", "cloud"):
        ep = os.getenv("EMBEDDING_ENDPOINT", "").strip()
        if ep:
            url = ep
        else:
            base = os.getenv("EMBEDDING_API_BASE_URL", "").strip().rstrip("/")
            if not base:
                print(
                    "EMBEDDING_SOURCE=remote 时需要设置 EMBEDDING_ENDPOINT 或 EMBEDDING_API_BASE_URL",
                    file=sys.stderr,
                )
                return 1
            url = base + "/v1/embeddings"
        api_key = os.getenv("EMBEDDING_API_KEY", "").strip()
        model = os.getenv("EMBEDDING_API_MODEL", "").strip() or "BAAI/bge-m3"
    elif src in ("self_hosted", "local_service", "python"):
        host = _http_client_host(os.getenv("EMBEDDING_LOCAL_HTTP_HOST", "127.0.0.1"))
        port = os.getenv("EMBEDDING_LOCAL_HTTP_PORT", "3888").strip()
        health_base = f"http://{host}:{port}".rstrip("/")
        url = health_base + "/v1/embeddings"
        api_key = (
            os.getenv("EMBEDDING_LOCAL_API_KEY", "").strip()
            or os.getenv("EMBEDDING_SERVICE_API_KEY", "").strip()
        )
        model = os.getenv("EMBEDDING_MODEL", "").strip() or "BAAI/bge-m3"
    else:
        print(f"未知 EMBEDDING_SOURCE={src!r}，请用 remote 或 self_hosted", file=sys.stderr)
        return 1

    texts = sys.argv[1:] if len(sys.argv) > 1 else ["hello", "embedding smoke test"]
    body = json.dumps({"model": model, "input": texts}, ensure_ascii=False).encode("utf-8")

    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    if api_key:
        req.add_header("Authorization", f"Bearer {api_key}")

    print(f"EMBEDDING_SOURCE={src}")
    print(f"POST {url}")
    print(f"model={model!r} input_count={len(texts)}")

    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            raw = resp.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="replace")
        print(f"HTTP {e.code}: {err_body[:800]}", file=sys.stderr)
        return 1
    except urllib.error.URLError as e:
        print(f"请求失败: {e}", file=sys.stderr)
        return 1

    data = json.loads(raw)
    items = data.get("data") or []
    print(f"ok: {len(items)} vectors, response.model={data.get('model')!r}")
    for i, row in enumerate(items):
        emb = row.get("embedding") or []
        print(f"  [{i}] dim={len(emb)} head5={emb[:5]}")

    if health_base:
        health_url = health_base + "/healthz"
        try:
            with urllib.request.urlopen(health_url, timeout=5) as hr:
                print(f"GET {health_url} -> {hr.read().decode()}")
        except OSError:
            pass

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
