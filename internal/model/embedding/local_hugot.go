package embedding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

// LocalOptions configures in-process embedding via knights-analytics/hugot (ONNX + tokenizer on disk).
// ModelDir must be a Hugging Face–style export directory containing the ONNX file and tokenizer assets
// (e.g. export with Optimum: model.onnx, tokenizer.json, config.json).
type LocalOptions struct {
	ModelDir     string
	OnnxFilename string
	PipelineName string
	Normalize    bool
	MaxBatch     int
	ExpectedDim  int
}

func (o LocalOptions) normalized() (LocalOptions, error) {
	dir := strings.TrimSpace(o.ModelDir)
	if dir == "" {
		return o, fmt.Errorf("local embedding requires model_dir (directory with ONNX + tokenizer)")
	}
	o.ModelDir = filepath.Clean(dir)
	if o.OnnxFilename == "" {
		o.OnnxFilename = "model.onnx"
	}
	if o.PipelineName == "" {
		o.PipelineName = "defaultEmbedding"
	}
	if o.MaxBatch <= 0 {
		o.MaxBatch = 32
	}
	return o, nil
}

// HugotEmbedder runs feature-extraction ONNX models locally (no HTTP).
type HugotEmbedder struct {
	sess *hugot.Session
	pipe *pipelines.FeatureExtractionPipeline
	mu   sync.Mutex
	opt  LocalOptions
}

// NewLocalHugotEmbedder loads the model from disk into the current process using Hugot’s Go backend.
func NewLocalHugotEmbedder(lo LocalOptions) (*HugotEmbedder, error) {
	o, err := lo.normalized()
	if err != nil {
		return nil, err
	}
	sess, err := hugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("hugot session: %w", err)
	}
	cfg := hugot.FeatureExtractionConfig{
		ModelPath:    o.ModelDir,
		Name:         o.PipelineName,
		OnnxFilename: o.OnnxFilename,
	}
	if o.Normalize {
		cfg.Options = []hugot.FeatureExtractionOption{pipelines.WithNormalization()}
	}
	pipe, err := hugot.NewPipeline(sess, cfg)
	if err != nil {
		_ = sess.Destroy()
		return nil, fmt.Errorf("hugot feature extraction pipeline: %w — Go 本地嵌入仅支持含 ONNX 与 tokenizer 的目录（如 Optimum 导出），不能直接使用 Python HuggingFaceEmbeddings 那种 PyTorch/safetensors checkpoint；可导出 ONNX 后指向该目录并设 EMBEDDING_LOCAL_ONNX_FILE，或改用 embedding.backend=http 调用已有嵌入服务", err)
	}
	return &HugotEmbedder{sess: sess, pipe: pipe, opt: o}, nil
}

// Close releases Hugot session and model memory.
func (h *HugotEmbedder) Close() error {
	if h == nil || h.sess == nil {
		return nil
	}
	err := h.sess.Destroy()
	h.sess = nil
	h.pipe = nil
	return err
}

// Embed implements Embedder. Context cancellation is checked between batches; inference calls are not preemptible.
func (h *HugotEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	bs := h.opt.MaxBatch
	for start := 0; start < len(texts); start += bs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + bs
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		h.mu.Lock()
		res, err := h.pipe.RunPipeline(batch)
		h.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("hugot run: %w", err)
		}
		if res == nil || len(res.Embeddings) != len(batch) {
			return nil, fmt.Errorf("hugot returned %d embeddings for %d inputs", len(res.Embeddings), len(batch))
		}
		out = append(out, res.Embeddings...)
	}
	if h.opt.ExpectedDim > 0 {
		for i := range out {
			if len(out[i]) != h.opt.ExpectedDim {
				return nil, fmt.Errorf("embedding dim %d at %d want %d", len(out[i]), i, h.opt.ExpectedDim)
			}
		}
	}
	return out, nil
}
