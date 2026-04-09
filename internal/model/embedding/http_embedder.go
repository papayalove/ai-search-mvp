package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// HTTPEmbedder calls a remote or local HTTP embedding API (OpenAI-compatible by default).
type HTTPEmbedder struct {
	opt Options
}

// NewHTTPEmbedder validates options and returns an embedder.
func NewHTTPEmbedder(opt Options) (*HTTPEmbedder, error) {
	o, err := opt.normalized()
	if err != nil {
		return nil, err
	}
	return &HTTPEmbedder{opt: o}, nil
}

// Embed batches texts (MaxBatch per request) and returns vectors in the same order.
func (e *HTTPEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(texts))
	bs := e.opt.MaxBatch
	for start := 0; start < len(texts); start += bs {
		end := start + bs
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]
		vecs, err := e.embedBatch(ctx, batch)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(batch) {
			return nil, fmt.Errorf("embedding API returned %d vectors for %d inputs", len(vecs), len(batch))
		}
		out = append(out, vecs...)
	}
	if e.opt.ExpectedDim > 0 {
		for i := range out {
			if len(out[i]) != e.opt.ExpectedDim {
				return nil, fmt.Errorf("vector dim %d at %d want %d", len(out[i]), i, e.opt.ExpectedDim)
			}
		}
	}
	return out, nil
}

func (e *HTTPEmbedder) embedBatch(ctx context.Context, batch []string) ([][]float32, error) {
	body := map[string]any{
		"input": batch,
	}
	if m := strings.TrimSpace(e.opt.Model); m != "" {
		body["model"] = m
	}
	for k, v := range e.opt.ExtraRequestFields {
		body[k] = v
	}
	if mf := strings.TrimSpace(e.opt.ModelFile); mf != "" {
		body[e.opt.ModelFileField] = mf
	}

	switch e.opt.RequestFormat {
	case RequestOpenAI:
	default:
		return nil, fmt.Errorf("unsupported request format %q", e.opt.RequestFormat)
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.opt.Endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(e.opt.APIKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	for hk, hv := range e.opt.ExtraHeaders {
		if strings.TrimSpace(hk) != "" {
			req.Header.Set(hk, hv)
		}
	}

	resp, err := e.opt.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding http status %d: %s", resp.StatusCode, truncate(string(respBody), 512))
	}

	switch e.opt.ResponseFormat {
	case ResponseOpenAIData:
		return parseOpenAIData(respBody)
	case ResponseEmbeddingsArray:
		return parseEmbeddingsArray(respBody)
	default:
		return nil, fmt.Errorf("unsupported response format %q", e.opt.ResponseFormat)
	}
}

func parseOpenAIData(b []byte) ([][]float32, error) {
	var wrap struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return nil, fmt.Errorf("decode openai_data: %w", err)
	}
	if len(wrap.Data) == 0 {
		return nil, fmt.Errorf("empty data[] in embedding response")
	}
	sort.Slice(wrap.Data, func(i, j int) bool { return wrap.Data[i].Index < wrap.Data[j].Index })
	out := make([][]float32, len(wrap.Data))
	for i := range wrap.Data {
		v := wrap.Data[i].Embedding
		f := make([]float32, len(v))
		for j := range v {
			f[j] = float32(v[j])
		}
		out[i] = f
	}
	return out, nil
}

func parseEmbeddingsArray(b []byte) ([][]float32, error) {
	var wrap struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return nil, fmt.Errorf("decode embeddings_array: %w", err)
	}
	if len(wrap.Embeddings) == 0 {
		return nil, fmt.Errorf("empty embeddings[] in embedding response")
	}
	out := make([][]float32, len(wrap.Embeddings))
	for i := range wrap.Embeddings {
		row := wrap.Embeddings[i]
		f := make([]float32, len(row))
		for j := range row {
			f[j] = float32(row[j])
		}
		out[i] = f
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
