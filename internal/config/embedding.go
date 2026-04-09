package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"ai-search-v1/internal/model/embedding"
)

// BuildEmbedder returns an embedder when embedding.enabled is true; otherwise nil, nil.
func (a *API) BuildEmbedder() (embedding.Embedder, error) {
	if a == nil || !a.Embedding.EmbeddingEnabled() {
		return nil, nil
	}
	b := a.Embedding.EffectiveBackend()
	switch b {
	case "http", "remote", "api":
		opt, err := a.Embedding.ToHTTPEmbedderOptions()
		if err != nil {
			return nil, err
		}
		return embedding.NewHTTPEmbedder(opt)
	case "local", "file", "onnx":
		return nil, fmt.Errorf(
			"embedding backend %q is not supported; use embedding.backend: http in configs/api.yaml and set EMBEDDING_SOURCE=remote (远程 API) or self_hosted (自建 python embedding-service)", b)
	default:
		return nil, fmt.Errorf("embedding backend %q: use http with EMBEDDING_SOURCE=remote or self_hosted", b)
	}
}

// EmbeddingConfig configures the embedding HTTP client（远程或自建服务，由 .env 的 EMBEDDING_SOURCE 切换）。
type EmbeddingConfig struct {
	Enabled *bool `yaml:"enabled"`

	// Backend: 仅支持 http；yaml 中若写 local 将在 BuildEmbedder 时报错并提示改用 HTTP + EMBEDDING_SOURCE。
	Backend string `yaml:"backend"`

	Endpoint string `yaml:"endpoint"`
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`

	ModelFile      string `yaml:"model_file"`
	ModelFileField string `yaml:"model_file_field"`

	// Path 与 Python config["path"] 对齐；若未设 local_model_dir 则用作本地 ONNX 目录。
	Path string `yaml:"path"`
	// Kwargs 与 Python model_kwargs 对齐：http 后端会并入请求 JSON（与 extra_request_fields 合并，kwargs 覆盖同名键）；local 后端仅识别少数键，其余忽略。
	Kwargs map[string]any `yaml:"kwargs"`

	LocalModelDir  string `yaml:"local_model_dir"`
	LocalOnnxFile  string `yaml:"local_onnx_file"`
	LocalNormalize *bool  `yaml:"local_normalize"`
	LocalPipeline  string `yaml:"local_pipeline_name"`

	TimeoutSeconds int `yaml:"timeout_seconds"`
	MaxBatch       int `yaml:"max_batch"`
	ExpectedDim    int `yaml:"expected_dim"`

	RequestFormat  string `yaml:"request_format"`
	ResponseFormat string `yaml:"response_format"`

	ExtraRequestFields map[string]any    `yaml:"extra_request_fields"`
	ExtraHeaders       map[string]string `yaml:"extra_headers"`
}

// EmbeddingEnabled is true only when enabled is explicitly true.
func (e EmbeddingConfig) EmbeddingEnabled() bool {
	if e.Enabled == nil {
		return false
	}
	return *e.Enabled
}

// EffectiveBackend 返回实际使用的嵌入后端。
// EMBEDDING_SOURCE=remote|self_hosted|… 时强制走 HTTP 客户端（与 yaml backend 无关）。
// 否则 EMBEDDING_BACKEND 可覆盖 yaml；yaml 的 backend 为空时默认为 http。
func (e EmbeddingConfig) EffectiveBackend() string {
	if s := strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDING_SOURCE"))); s != "" {
		switch s {
		case "remote", "api", "cloud", "self_hosted", "local_service", "python":
			return "http"
		}
	}
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_BACKEND")); v != "" {
		return strings.ToLower(v)
	}
	b := strings.TrimSpace(e.Backend)
	if b == "" {
		return "http"
	}
	return strings.ToLower(b)
}

// ToHTTPEmbedderOptions maps YAML to embedding.Options for HTTP backend.
func (e EmbeddingConfig) ToHTTPEmbedderOptions() (embedding.Options, error) {
	reqFmt := embedding.RequestFormat(strings.TrimSpace(e.RequestFormat))
	if reqFmt == "" {
		reqFmt = embedding.RequestOpenAI
	}
	respFmt := embedding.ResponseFormat(strings.TrimSpace(e.ResponseFormat))
	if respFmt == "" {
		respFmt = embedding.ResponseOpenAIData
	}
	to := time.Duration(e.TimeoutSeconds) * time.Second
	extra := mergeEmbeddingExtra(e.ExtraRequestFields, e.Kwargs)
	src := strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDING_SOURCE")))

	// self_hosted：HTTP 请求 model 用 EMBEDDING_MODEL（Hub id 或本地路径），须与 Python 进程一致。
	// remote：HTTP 请求 model 用 EMBEDDING_API_MODEL。未设环境变量时回退 yaml embedding.model / path。
	model := strings.TrimSpace(e.Model)
	if model == "" {
		model = strings.TrimSpace(e.Path)
	}
	switch src {
	case "self_hosted", "local_service", "python":
		if v := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL")); v != "" {
			model = v
		}
	default:
		if v := strings.TrimSpace(os.Getenv("EMBEDDING_API_MODEL")); v != "" {
			model = v
		}
	}

	endpoint := strings.TrimSpace(e.Endpoint)
	if v := strings.TrimSpace(os.Getenv("EMBEDDING_ENDPOINT")); v != "" {
		endpoint = v
	} else {
		switch src {
		case "self_hosted", "local_service", "python":
			host := strings.TrimSpace(os.Getenv("EMBEDDING_LOCAL_HTTP_HOST"))
			if host == "" {
				host = "127.0.0.1"
			}
			// 0.0.0.0 / :: 仅适合 bind，HTTP 客户端必须连本机回环（Windows 否则 WinError 10049）
			if host == "0.0.0.0" || host == "::" {
				host = "127.0.0.1"
			}
			port := strings.TrimSpace(os.Getenv("EMBEDDING_LOCAL_HTTP_PORT"))
			if port == "" {
				port = "3888"
			}
			endpoint = fmt.Sprintf("http://%s:%s/v1/embeddings", host, port)
		default:
			if base := strings.TrimSpace(os.Getenv("EMBEDDING_API_BASE_URL")); base != "" {
				base = strings.TrimRight(base, "/")
				endpoint = base + "/v1/embeddings"
			}
		}
	}

	// 与 EMBEDDING_SOURCE 严格对应：自建只读 EMBEDDING_LOCAL_API_KEY；远程读 yaml + EMBEDDING_API_KEY（勿混用）
	var apiKey string
	switch src {
	case "self_hosted", "local_service", "python":
		apiKey = strings.TrimSpace(os.Getenv("EMBEDDING_LOCAL_API_KEY"))
	default:
		apiKey = strings.TrimSpace(e.APIKey)
		if v := strings.TrimSpace(os.Getenv("EMBEDDING_API_KEY")); v != "" {
			apiKey = v
		}
	}

	return embedding.Options{
		Endpoint:           endpoint,
		APIKey:             apiKey,
		Model:              model,
		ModelFile:          strings.TrimSpace(e.ModelFile),
		ModelFileField:     strings.TrimSpace(e.ModelFileField),
		ExtraRequestFields: extra,
		ExtraHeaders:       e.ExtraHeaders,
		RequestFormat:      reqFmt,
		ResponseFormat:     respFmt,
		Timeout:            to,
		MaxBatch:           e.MaxBatch,
		ExpectedDim:        e.ExpectedDim,
	}, nil
}

// mergeEmbeddingExtra: base then kwargs (kwargs override same keys), like layering Python model_kwargs onto defaults.
func mergeEmbeddingExtra(base, kwargs map[string]any) map[string]any {
	out := make(map[string]any)
	if base != nil {
		for k, v := range base {
			out[k] = v
		}
	}
	if kwargs != nil {
		for k, v := range kwargs {
			out[k] = v
		}
	}
	return out
}

// ToLocalHugotOptions maps YAML to local (on-disk ONNX) options.
// 环境变量 EMBEDDING_LOCAL_MODEL_DIR 或 EMBEDDING_LOCAL_PATH 非空时覆盖 yaml 的 local_model_dir / path。
func (e EmbeddingConfig) ToLocalHugotOptions() (embedding.LocalOptions, error) {
	dir := strings.TrimSpace(os.Getenv("EMBEDDING_LOCAL_MODEL_DIR"))
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("EMBEDDING_LOCAL_PATH"))
	}
	if dir == "" {
		dir = strings.TrimSpace(e.LocalModelDir)
	}
	if dir == "" {
		dir = strings.TrimSpace(e.Path)
	}
	norm := false
	if e.LocalNormalize != nil {
		norm = *e.LocalNormalize
	} else if e.Kwargs != nil {
		if v, ok := e.Kwargs["normalize_embeddings"]; ok {
			if b, ok := v.(bool); ok {
				norm = b
			}
		}
	}
	onnxFile := strings.TrimSpace(os.Getenv("EMBEDDING_LOCAL_ONNX_FILE"))
	if onnxFile == "" {
		onnxFile = strings.TrimSpace(e.LocalOnnxFile)
	}
	if onnxFile == "" && e.Kwargs != nil {
		if v, ok := e.Kwargs["onnx_file"]; ok {
			if s, ok := v.(string); ok {
				onnxFile = s
			}
		}
	}
	return embedding.LocalOptions{
		ModelDir:     dir,
		OnnxFilename: onnxFile,
		PipelineName: strings.TrimSpace(e.LocalPipeline),
		Normalize:    norm,
		MaxBatch:     e.MaxBatch,
		ExpectedDim:  e.ExpectedDim,
	}, nil
}
