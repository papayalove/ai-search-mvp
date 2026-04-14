"""
KeyBERT 风格关键词提取（sentence-transformers + 余弦相似度），供 app.py 的 POST /v1/keywords 使用。

模型默认 all-MiniLM-L6-v2，与 Python KeyBERT 示例一致；首次请求时懒加载，避免仅使用 /v1/embeddings 的部署也被迫下载第二套权重。
"""
from __future__ import annotations

import logging
import os
import threading
from typing import Any

logger = logging.getLogger(__name__)

_lock = threading.Lock()
_kw: Any = None
_loaded_model_id: str = ""


def default_keybert_model_id() -> str:
    return (os.getenv("KEYBERT_MODEL", "all-MiniLM-L6-v2").strip() or "all-MiniLM-L6-v2")


def _resolve_device(spec: str) -> str:
    s = (spec or "").strip().lower()
    if s in ("", "auto"):
        try:
            import torch

            return "cuda" if torch.cuda.is_available() else "cpu"
        except Exception:
            return "cpu"
    if s in ("cpu", "cuda", "mps"):
        return s
    return "cpu"


def _get_keybert(model_id: str) -> Any:
    global _kw, _loaded_model_id
    with _lock:
        if _kw is not None and _loaded_model_id == model_id:
            return _kw
        try:
            from keybert import KeyBERT
            from sentence_transformers import SentenceTransformer
        except ImportError as e:
            raise RuntimeError(
                "关键词依赖未安装。请在 embedding-service 目录执行: pip install keybert scikit-learn"
            ) from e
        except OSError as e:
            # Windows 常见：WinError 1114 加载 c10.dll / 某依赖 DLL 初始化失败（与 PyTorch 安装或 VC++ 运行库有关）
            msg = str(e).lower()
            if "1114" in str(e) or "c10.dll" in msg or "dll" in msg:
                raise RuntimeError(
                    "无法加载 PyTorch 动态库（例如 WinError 1114、c10.dll）。"
                    "请在当前 conda/venv 中重装与机器匹配的 PyTorch，并安装 Microsoft Visual C++ Redistributable（x64）。"
                    "仅 CPU 时可尝试：\n"
                    "  pip uninstall torch torchvision torchaudio -y\n"
                    "  pip install torch --index-url https://download.pytorch.org/whl/cpu\n"
                    "若使用 CUDA 版 torch，请保证显卡驱动与 CUDA 版本与 PyTorch 官方说明一致；仍失败可重启后再试或暂时关闭冲突的安全软件。"
                ) from e
            raise
        dev = _resolve_device(os.getenv("KEYBERT_DEVICE", os.getenv("EMBEDDING_DEVICE", "auto")))
        logger.info("KeyBERT: loading SentenceTransformer %r device=%s", model_id, dev)
        try:
            st = SentenceTransformer(model_id, device=dev)
        except OSError as e:
            msg = str(e).lower()
            if "1114" in str(e) or "c10.dll" in msg or "dll" in msg:
                raise RuntimeError(
                    "加载 SentenceTransformer 时 PyTorch DLL 失败（同 WinError 1114）。"
                    "请按 keyphrase_backend / README「故障排查」中 Windows PyTorch 条目重装 torch 或检查 VC++ 运行库。"
                ) from e
            raise
        _kw = KeyBERT(model=st)
        _loaded_model_id = model_id
        return _kw


def loaded_keybert_model_id() -> str:
    return _loaded_model_id


def extract_keyphrases(
    text: str,
    *,
    model_id: str,
    keyphrase_ngram_range: tuple[int, int],
    top_n: int,
    stop_words: str | list[str] | None,
    use_mmr: bool,
    diversity: float,
    vectorizer: Any | None,
) -> list[tuple[str, float]]:
    """返回 (短语, 分数) 列表，与 KeyBERT.extract_keywords 一致。"""
    kw = _get_keybert(model_id)
    kwargs: dict[str, Any] = {
        "keyphrase_ngram_range": keyphrase_ngram_range,
        "top_n": int(top_n),
    }
    if stop_words is not None:
        kwargs["stop_words"] = stop_words
    if vectorizer is not None:
        kwargs["vectorizer"] = vectorizer
    if use_mmr:
        kwargs["use_mmr"] = True
        kwargs["diversity"] = float(diversity)
    return kw.extract_keywords(text, **kwargs)


def build_char_wb_vectorizer(ngram_range: tuple[int, int]) -> Any:
    """中文等无空格文本：字/字边界 n-gram（与 KeyBERT 文档中 CJK 用法一致）。"""
    from sklearn.feature_extraction.text import CountVectorizer

    return CountVectorizer(ngram_range=ngram_range, analyzer="char_wb")


def _text_has_cjk(s: str) -> bool:
    for c in s:
        if "\u4e00" <= c <= "\u9fff":
            return True
    return False


if __name__ == "__main__":
    import argparse
    import sys

    logging.basicConfig(level=logging.INFO, format="%(levelname)s %(message)s")
    ap = argparse.ArgumentParser(description="本地试跑 KeyBERT（与 POST /v1/keywords 同源逻辑）")
    ap.add_argument(
        "text",
        nargs="?",
        default="工业物联网中多模态数据用于设备故障诊断与安全监测",
        help="待提取文本；含中日韩字时默认 char_wb，否则默认按词 n-gram",
    )
    ap.add_argument("--top", type=int, default=5, dest="top_n")
    ap.add_argument(
        "--analyzer",
        choices=("auto", "char_wb", "word"),
        default="auto",
        help="auto：含 CJK 用 char_wb，否则 word",
    )
    args = ap.parse_args()
    text = args.text
    mid = default_keybert_model_id()
    ng = (1, 2)
    if args.analyzer == "char_wb":
        vec = build_char_wb_vectorizer(ng)
    elif args.analyzer == "word":
        vec = None
    else:
        vec = build_char_wb_vectorizer(ng) if _text_has_cjk(text) else None
    print(f"model={mid!r} analyzer={'char_wb' if vec else 'word'}", file=sys.stderr)
    print(f"text={text!r}", file=sys.stderr)
    pairs = extract_keyphrases(
        text,
        model_id=mid,
        keyphrase_ngram_range=ng,
        top_n=args.top_n,
        stop_words=None,
        use_mmr=False,
        diversity=0.5,
        vectorizer=vec,
    )
    for kw, score in pairs:
        print(f"{score:.4f}\t{kw}")
