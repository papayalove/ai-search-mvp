"""嵌入后端：默认用 HuggingFace transformers（与 model_name + model_kwargs 一致）；可选 sentence_transformers。"""
from __future__ import annotations

import logging
from typing import Any, Protocol

import numpy as np

logger = logging.getLogger(__name__)


class EmbedBackend(Protocol):
    def encode(self, texts: list[str], batch_size: int, normalize: bool) -> np.ndarray: ...


class SentenceTransformerBackend:
    def __init__(self, model: Any) -> None:
        self._m = model

    def encode(self, texts: list[str], batch_size: int, normalize: bool) -> np.ndarray:
        v = self._m.encode(
            texts,
            batch_size=batch_size,
            normalize_embeddings=normalize,
            convert_to_numpy=True,
            show_progress_bar=False,
        )
        if isinstance(v, np.ndarray) and v.ndim == 1:
            v = v.reshape(1, -1)
        return v


class HFTransformersMeanPoolBackend:
    """
    与 LangChain 里 ``HuggingFaceEmbeddings(model_name=path, model_kwargs=kwargs)`` 的用法对齐：
    用 ``AutoTokenizer`` / ``AutoModel.from_pretrained`` 加载，**不**走 SentenceTransformers，
    避免 Trainer checkpoint 被误识别为 ST 并套一层 mean pooling 的警告。

    编码：对 ``last_hidden_state``（或 ``hidden_states[-1]``）按 attention mask 做 mean pool，
    可选 L2 归一化。部分专用嵌入头（如个别 Qwen3-Embedding 官方脚本）若需 last_token 等策略，可后续加环境变量扩展。
    """

    def __init__(
        self,
        model: Any,
        tokenizer: Any,
        device: str,
        default_normalize: bool,
        max_length: int,
    ) -> None:
        self._model = model
        self._tokenizer = tokenizer
        self._device = device
        self._default_normalize = default_normalize
        self._max_length = max_length

    @classmethod
    def from_pretrained(
        cls,
        model_id: str,
        device: str,
        trust_remote_code: bool,
        model_kwargs: dict[str, Any],
        normalize: bool,
        max_length: int,
    ) -> HFTransformersMeanPoolBackend:
        import torch
        from transformers import AutoModel, AutoTokenizer

        mk = dict(model_kwargs)
        tc = bool(mk.pop("trust_remote_code", False) or trust_remote_code)
        mk.pop("device", None)

        tokenizer = AutoTokenizer.from_pretrained(model_id, trust_remote_code=tc)
        model = AutoModel.from_pretrained(model_id, trust_remote_code=tc, **mk)
        model.to(device)
        model.eval()
        logger.info("HF transformers backend: AutoModel loaded to %s", device)
        return cls(model, tokenizer, device, normalize, max_length)

    def encode(self, texts: list[str], batch_size: int, normalize: bool) -> np.ndarray:
        import torch
        import torch.nn.functional as F

        do_norm = normalize or self._default_normalize
        chunks: list[np.ndarray] = []
        for i in range(0, len(texts), batch_size):
            batch = texts[i : i + batch_size]
            enc = self._tokenizer(
                batch,
                padding=True,
                truncation=True,
                max_length=self._max_length,
                return_tensors="pt",
            )
            enc = {k: v.to(self._device) for k, v in enc.items()}
            with torch.no_grad():
                out = self._model(**enc, output_hidden_states=True)

            lhs = getattr(out, "last_hidden_state", None)
            hs = getattr(out, "hidden_states", None)
            if lhs is not None:
                h = lhs
            elif hs is not None:
                h = hs[-1]
            else:
                raise RuntimeError("模型前向未返回 last_hidden_state / hidden_states，无法 mean pool")

            mask = enc["attention_mask"].unsqueeze(-1).expand(h.size()).float()
            summed = (h * mask).sum(dim=1)
            denom = mask.sum(dim=1).clamp(min=1e-9)
            pooled = summed / denom
            if do_norm:
                pooled = F.normalize(pooled, p=2, dim=1)
            chunks.append(pooled.float().cpu().numpy())
        return np.vstack(chunks)


def build_backend(
    loader: str,
    model_id: str,
    device: str,
    trust_remote_code: bool,
    model_kwargs: dict[str, Any],
    normalize: bool,
    max_seq_length: int,
) -> EmbedBackend:
    loader = loader.strip().lower()
    if loader in ("sentence_transformers", "st", "sbert"):
        from sentence_transformers import SentenceTransformer

        st_kw: dict[str, Any] = {}
        if trust_remote_code:
            st_kw["trust_remote_code"] = True
        mk = dict(model_kwargs)
        if mk:
            st_kw["model_kwargs"] = mk
            logger.info("SentenceTransformer model_kwargs keys: %s", list(mk.keys()))
        logger.info("loader=sentence_transformers model=%r device=%s", model_id, device)
        m = SentenceTransformer(model_id, device=device, **st_kw)
        return SentenceTransformerBackend(m)

    if loader in ("huggingface", "hf", "langchain"):
        logger.info(
            "loader=huggingface (transformers AutoModel+mean_pool) model=%r device=%s",
            model_id,
            device,
        )
        return HFTransformersMeanPoolBackend.from_pretrained(
            model_id=model_id,
            device=device,
            trust_remote_code=trust_remote_code,
            model_kwargs=model_kwargs,
            normalize=normalize,
            max_length=max_seq_length,
        )

    raise ValueError(
        f"未知 EMBEDDING_LOADER={loader!r}；使用 huggingface（默认）或 sentence_transformers"
    )
