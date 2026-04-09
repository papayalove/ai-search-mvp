// Package embedding provides text→vector clients: HTTPEmbedder (OpenAI-compatible API) and
// HugotEmbedder (load ONNX + tokenizer from a directory in-process via knights-analytics/hugot).
package embedding

import "context"

// Embedder turns text batches into dense vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
