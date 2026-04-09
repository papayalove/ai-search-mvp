#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
查询 Elasticsearch 指定 index 的文档条数，并打印前 3 条 _source（与 Go 共用 configs/api.yaml / .env）。

依赖:
  pip install pyyaml

用法（在项目根目录执行）:
  python scripts/es_index_peek.py
  python scripts/es_index_peek.py --index other_index

环境变量（可选，覆盖 yaml，与 internal/config/api.go ToElasticsearch 对齐）:
  API_CONFIG          配置文件路径
  ES_URL              单节点地址，覆盖 yaml 中的 address
  ES_ADDRESSES        逗号分隔，取第一个作为 base
  ES_USERNAME, ES_PASSWORD, ES_INDEX
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any


def repo_root() -> Path:
    return Path(__file__).resolve().parent.parent


def apply_dotenv(path: Path) -> None:
    if not path.is_file():
        return
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            continue
        key, _, val = line.partition("=")
        key = key.strip()
        val = val.strip().strip('"').strip("'")
        if key:
            os.environ[key] = val


def normalize_base(url: str) -> str:
    return url.rstrip("/").strip()


def load_es_settings(config_path: Path) -> dict[str, Any]:
    try:
        import yaml
    except ImportError:
        print("缺少 PyYAML，请执行: pip install pyyaml", file=sys.stderr)
        sys.exit(1)
    with config_path.open(encoding="utf-8") as f:
        cfg = yaml.safe_load(f)
    return cfg.get("elasticsearch") or {}


def resolve_es_target(es_yaml: dict[str, Any]) -> tuple[str, str, str, str]:
    """返回 (base_url, index, username, password)。"""
    addrs: list[str] = []
    for a in es_yaml.get("addresses") or []:
        a = str(a).strip()
        if a:
            addrs.append(a)
    one = str(es_yaml.get("address") or "").strip()
    if one:
        addrs = [one] + [x for x in addrs if x != one]

    if os.environ.get("ES_URL", "").strip():
        addrs = [os.environ["ES_URL"].strip()]
    elif os.environ.get("ES_ADDRESSES", "").strip():
        parts = [p.strip() for p in os.environ["ES_ADDRESSES"].split(",") if p.strip()]
        if parts:
            addrs = parts

    if not addrs:
        print("未配置 ES 地址：请在 configs/api.yaml 的 elasticsearch.address 或环境变量 ES_URL / ES_ADDRESSES 中设置", file=sys.stderr)
        sys.exit(1)

    base = normalize_base(addrs[0])
    index = str(es_yaml.get("index") or "").strip() or "entity_postings_v1"
    if os.environ.get("ES_INDEX", "").strip():
        index = os.environ["ES_INDEX"].strip()

    user = str(es_yaml.get("username") or "").strip()
    if os.environ.get("ES_USERNAME", "").strip():
        user = os.environ["ES_USERNAME"].strip()

    password = str(es_yaml.get("password") or "")
    if "ES_PASSWORD" in os.environ:
        password = os.environ["ES_PASSWORD"]

    return base, index, user, password


def es_request(
    method: str,
    url: str,
    *,
    user: str,
    password: str,
    body: bytes | None = None,
    timeout: float = 60.0,
) -> tuple[int, bytes]:
    req = urllib.request.Request(url, data=body, method=method)
    req.add_header("Accept", "application/json")
    if body is not None:
        req.add_header("Content-Type", "application/json")
    u, p = user.strip(), password
    if u or p != "":
        token = base64.b64encode(f"{u}:{p}".encode("utf-8")).decode("ascii")
        req.add_header("Authorization", f"Basic {token}")

    ctx = ssl.create_default_context()
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
            return resp.getcode(), resp.read()
    except urllib.error.HTTPError as e:
        raw = e.read()
        return e.code, raw


def main() -> None:
    parser = argparse.ArgumentParser(description="ES index 文档数与前 3 条数据")
    parser.add_argument("--config", default="", help="api.yaml 路径（默认 API_CONFIG 或 configs/api.yaml）")
    parser.add_argument("--index", default="", help="覆盖索引名（默认 yaml / ES_INDEX）")
    parser.add_argument("--timeout", type=float, default=60.0, help="HTTP 超时秒数")
    args = parser.parse_args()

    root = repo_root()
    apply_dotenv(root / ".env")

    cfg_rel = args.config.strip() or os.environ.get("API_CONFIG", "").strip() or "configs/api.yaml"
    config_path = Path(cfg_rel)
    if not config_path.is_absolute():
        config_path = root / config_path
    if not config_path.is_file():
        print(f"配置文件不存在: {config_path}", file=sys.stderr)
        sys.exit(1)

    es_yaml = load_es_settings(config_path)
    base, index, user, password = resolve_es_target(es_yaml)
    if args.index.strip():
        index = args.index.strip()

    count_url = f"{base}/{urllib.parse.quote(index, safe='')}/_count"
    search_url = f"{base}/{urllib.parse.quote(index, safe='')}/_search"

    print(f"base_url={base}")
    print(f"index={index}")
    print()

    # --- count ---
    code, raw = es_request("GET", count_url, user=user, password=password, timeout=args.timeout)
    if code >= 300:
        print(f"_count 失败 HTTP {code}: {raw.decode('utf-8', errors='replace')[:2000]}", file=sys.stderr)
        sys.exit(1)
    count_body = json.loads(raw.decode("utf-8"))
    total = count_body.get("count")
    print(f"文档条数 (/_count): {total}")
    print()

    # --- first 3 docs ---
    query = {"size": 3, "query": {"match_all": {}}, "sort": ["_doc"]}
    payload = json.dumps(query).encode("utf-8")
    code, raw = es_request("POST", search_url, user=user, password=password, body=payload, timeout=args.timeout)
    if code >= 300:
        print(f"_search 失败 HTTP {code}: {raw.decode('utf-8', errors='replace')[:2000]}", file=sys.stderr)
        sys.exit(1)
    search_body = json.loads(raw.decode("utf-8"))
    hits = (search_body.get("hits") or {}).get("hits") or []
    print(f"前 {len(hits)} 条 _source（match_all + sort _doc）:")
    for i, h in enumerate(hits, start=1):
        src = h.get("_source")
        doc_id = h.get("_id", "")
        print(f"--- [{i}] _id={doc_id} ---")
        print(json.dumps(src, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
