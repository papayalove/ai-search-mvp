#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
删除 Milvus 中的指定 collection（与 Go 项目共用配置）。

依赖:
  pip install pymilvus pyyaml

用法（在项目根目录执行）:
  python scripts/drop_milvus_collection.py              # 只打印配置（未连 Milvus，不是「查到还有 collection」）
  python scripts/drop_milvus_collection.py --list-collections  # 连上后列出当前 db 下真实 collections
  python scripts/drop_milvus_collection.py --yes        # 确认删除

环境变量（可选，覆盖 yaml）:
  MILVUS_ADDRESS, MILVUS_USERNAME, MILVUS_PASSWORD, MILVUS_DB_NAME

密码: 从项目根目录 .env 读取 MILVUS_PASSWORD（与 Go 一致；后加载的 .env 覆盖 configs/.env）。

说明: 部分 Milvus 部署里「用户名」与「数据库名」恰好同名（例如均为 mineru_core），属正常情况。
"""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path


def repo_root() -> Path:
    return Path(__file__).resolve().parent.parent


def apply_dotenv(path: Path) -> None:
    """按 KEY=VAL 写入 os.environ（覆盖已有键，对齐根目录 .env Overload）。"""
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


def normalize_milvus_uri(address: str) -> str:
    """tcp://host:port -> http://host:port，供 MilvusClient 使用。"""
    s = address.strip()
    low = s.lower()
    for p in ("tcp://", "grpc://", "http://", "https://"):
        if low.startswith(p):
            s = s[len(p) :]
            break
    if "/" in s:
        s = s.split("/", 1)[0]
    s = s.strip()
    if not s:
        return ""
    if not s.lower().startswith("http"):
        return f"http://{s}"
    return s


def load_milvus_settings(config_path: Path) -> dict:
    try:
        import yaml
    except ImportError:
        print("缺少 PyYAML，请执行: pip install pyyaml", file=sys.stderr)
        sys.exit(1)
    with config_path.open(encoding="utf-8") as f:
        cfg = yaml.safe_load(f)
    return cfg.get("milvus") or {}


def main() -> None:
    root = repo_root()
    parser = argparse.ArgumentParser(description="Drop a Milvus collection")
    parser.add_argument(
        "--config",
        type=Path,
        default=root / "configs" / "api.yaml",
        help="api.yaml 路径",
    )
    parser.add_argument(
        "--collection",
        type=str,
        default="",
        help="要删除的 collection，默认用 yaml 里 milvus.collection",
    )
    parser.add_argument(
        "--yes",
        action="store_true",
        help="确认执行删除（不加则仅 dry-run）",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="只打印连接信息与 collection 名，不连接 Milvus",
    )
    parser.add_argument(
        "--list-collections",
        action="store_true",
        help="仅连接并列出当前 db 下所有 collection（不删除）",
    )
    args = parser.parse_args()

    apply_dotenv(root / "configs" / ".env")
    apply_dotenv(root / ".env")

    m = load_milvus_settings(args.config)
    uri_raw = os.environ.get("MILVUS_ADDRESS", m.get("address") or "")
    uri = normalize_milvus_uri(uri_raw)
    user = os.environ.get("MILVUS_USERNAME", m.get("username") or "")
    password = os.environ.get("MILVUS_PASSWORD", m.get("password") or "")
    db_name = os.environ.get("MILVUS_DB_NAME", m.get("db_name") or "default")
    collection = (args.collection or m.get("collection") or "").strip()

    if not uri:
        print("未配置 Milvus 地址：请在 api.yaml milvus.address 或环境变量 MILVUS_ADDRESS 设置", file=sys.stderr)
        sys.exit(1)
    if not args.list_collections and not collection:
        print("未指定 collection：请用 --collection 或在 api.yaml milvus.collection 配置", file=sys.stderr)
        sys.exit(1)

    print(f"uri:        {uri}")
    print(f"user:       {user!r}")
    print(f"password:   {'(set)' if password else '(empty)'}")
    print(f"db_name:    {db_name}")
    if not args.list_collections:
        print(f"collection: {collection}")

    if args.dry_run:
        print("\n--dry-run：不连接 Milvus。", file=sys.stderr)
        sys.exit(0)

    if not args.list_collections and not args.yes:
        print(
            "\n未连接 Milvus；上面 collection 仅来自配置文件/环境变量，不能据此判断库里是否仍存在该集合。",
            file=sys.stderr,
        )
        print("确认删除请执行:  python scripts/drop_milvus_collection.py --yes", file=sys.stderr)
        print("删除后自检请执行:  python scripts/drop_milvus_collection.py --list-collections", file=sys.stderr)
        sys.exit(0)

    try:
        from pymilvus import MilvusClient
    except ImportError:
        print("缺少 pymilvus，请执行: pip install pymilvus", file=sys.stderr)
        sys.exit(1)

    kwargs = {"uri": uri, "user": user, "password": password}
    try:
        client = MilvusClient(**kwargs, db_name=db_name)
    except TypeError:
        client = MilvusClient(**kwargs)
        if hasattr(client, "use_database"):
            client.use_database(db_name)

    names = client.list_collections()
    if args.list_collections:
        print(f"当前库 {db_name!r} 下 collections: {names}")
        sys.exit(0)

    if collection not in names:
        print(f"collection {collection!r} 不存在，当前有: {names}")
        sys.exit(0)

    client.drop_collection(collection)
    print(f"已删除 collection: {collection!r}")
    names_after = client.list_collections()
    print(f"删除后当前库 {db_name!r} 下 collections: {names_after}")


if __name__ == "__main__":
    main()
