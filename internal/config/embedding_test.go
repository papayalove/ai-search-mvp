package config

import (
	"testing"
)

func TestToHTTPEmbedderOptions_EmbeddingSourceSelfHosted(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "self_hosted")
	t.Setenv("EMBEDDING_LOCAL_HTTP_HOST", "127.0.0.1")
	t.Setenv("EMBEDDING_LOCAL_HTTP_PORT", "3999")
	t.Setenv("EMBEDDING_API_BASE_URL", "")

	e := EmbeddingConfig{
		Endpoint:       "http://should-not-use/v1/embeddings",
		TimeoutSeconds: 30,
		MaxBatch:       8,
		ExpectedDim:    768,
	}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://127.0.0.1:3999/v1/embeddings"; opt.Endpoint != want {
		t.Fatalf("endpoint: got %q want %q", opt.Endpoint, want)
	}
}

func TestToHTTPEmbedderOptions_RemoteBaseURL(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "remote")
	t.Setenv("EMBEDDING_API_BASE_URL", "https://api.example.com")

	e := EmbeddingConfig{TimeoutSeconds: 30, MaxBatch: 8, ExpectedDim: 768}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.example.com/v1/embeddings"; opt.Endpoint != want {
		t.Fatalf("endpoint: got %q want %q", opt.Endpoint, want)
	}
}

func TestToHTTPEmbedderOptions_RemoteFallsBackToYamlWhenBaseUnset(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "remote")
	t.Setenv("EMBEDDING_API_BASE_URL", "")

	e := EmbeddingConfig{
		Endpoint:       "https://yaml-only.example/v1/embeddings",
		TimeoutSeconds: 30,
		MaxBatch:       8,
		ExpectedDim:    768,
	}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://yaml-only.example/v1/embeddings"; opt.Endpoint != want {
		t.Fatalf("endpoint: got %q want %q", opt.Endpoint, want)
	}
}

func TestToHTTPEmbedderOptions_SelfHostedUsesEmbeddingModelIgnoresAPIModel(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "self_hosted")
	t.Setenv("EMBEDDING_LOCAL_HTTP_HOST", "127.0.0.1")
	t.Setenv("EMBEDDING_LOCAL_HTTP_PORT", "3888")
	t.Setenv("EMBEDDING_API_BASE_URL", "")
	t.Setenv("EMBEDDING_API_MODEL", "BAAI/bge-m3")
	t.Setenv("EMBEDDING_MODEL", `D:\checkpoint\model`)

	e := EmbeddingConfig{Model: "yaml-model", TimeoutSeconds: 30, MaxBatch: 8, ExpectedDim: 768}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opt.Model != `D:\checkpoint\model` {
		t.Fatalf("model: got %q want EMBEDDING_MODEL (self_hosted ignores EMBEDDING_API_MODEL)", opt.Model)
	}
}

func TestToHTTPEmbedderOptions_SelfHostedFallsBackToYamlWhenEmbeddingModelUnset(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "self_hosted")
	t.Setenv("EMBEDDING_LOCAL_HTTP_HOST", "127.0.0.1")
	t.Setenv("EMBEDDING_LOCAL_HTTP_PORT", "3888")
	t.Setenv("EMBEDDING_API_BASE_URL", "")
	t.Setenv("EMBEDDING_API_MODEL", "api-only")
	t.Setenv("EMBEDDING_MODEL", "")

	e := EmbeddingConfig{Model: "from-yaml", TimeoutSeconds: 30, MaxBatch: 8, ExpectedDim: 768}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opt.Model != "from-yaml" {
		t.Fatalf("model: got %q want yaml when EMBEDDING_MODEL empty", opt.Model)
	}
}

func TestToHTTPEmbedderOptions_SelfHostedUsesOnlyLocalAPIKey(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "self_hosted")
	t.Setenv("EMBEDDING_LOCAL_HTTP_HOST", "127.0.0.1")
	t.Setenv("EMBEDDING_LOCAL_HTTP_PORT", "3888")
	t.Setenv("EMBEDDING_API_BASE_URL", "")
	t.Setenv("EMBEDDING_API_KEY", "remote-secret")
	t.Setenv("EMBEDDING_LOCAL_API_KEY", "local-secret")

	e := EmbeddingConfig{APIKey: "yaml-key", TimeoutSeconds: 30, MaxBatch: 8, ExpectedDim: 768}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opt.APIKey != "local-secret" {
		t.Fatalf("APIKey: got %q want local-secret (must not use EMBEDDING_API_KEY)", opt.APIKey)
	}
}

func TestToHTTPEmbedderOptions_RemoteUsesAPIKey(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "remote")
	t.Setenv("EMBEDDING_API_BASE_URL", "https://api.example.com")
	t.Setenv("EMBEDDING_API_KEY", "remote-secret")
	t.Setenv("EMBEDDING_LOCAL_API_KEY", "local-secret")

	e := EmbeddingConfig{APIKey: "", TimeoutSeconds: 30, MaxBatch: 8, ExpectedDim: 768}
	opt, err := e.ToHTTPEmbedderOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opt.APIKey != "remote-secret" {
		t.Fatalf("APIKey: got %q want remote-secret", opt.APIKey)
	}
}

func TestEffectiveBackend_EmbeddingSourceForcesHTTP(t *testing.T) {
	t.Setenv("EMBEDDING_SOURCE", "self_hosted")
	t.Setenv("EMBEDDING_BACKEND", "")

	e := EmbeddingConfig{Backend: "local"}
	if g := e.EffectiveBackend(); g != "http" {
		t.Fatalf("EffectiveBackend: got %q want http", g)
	}
}
