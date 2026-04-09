package embedding

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Options configures HTTPEmbedder (OpenAI-compatible HTTP API).
type Options struct {
	Endpoint string
	APIKey   string

	// Model is sent as JSON field "model" (OpenAI-style).
	Model string

	// ModelFile is optional; when non-empty it is merged into the JSON body (key ModelFileField, default model_path).
	ModelFile      string
	ModelFileField string

	ExtraRequestFields map[string]any
	ExtraHeaders       map[string]string

	RequestFormat  RequestFormat
	ResponseFormat ResponseFormat

	Timeout     time.Duration
	MaxBatch    int
	ExpectedDim int

	HTTPClient *http.Client
}

// RequestFormat selects how the POST body is built.
type RequestFormat string

const (
	RequestOpenAI RequestFormat = "openai"
)

// ResponseFormat selects how the response is parsed.
type ResponseFormat string

const (
	ResponseOpenAIData ResponseFormat = "openai_data"
	ResponseEmbeddingsArray ResponseFormat = "embeddings_array"
)

func (o Options) normalized() (Options, error) {
	ep := strings.TrimSpace(o.Endpoint)
	if ep == "" {
		return o, fmt.Errorf("embedding endpoint is required for HTTP backend")
	}
	o.Endpoint = ep

	if o.RequestFormat == "" {
		o.RequestFormat = RequestOpenAI
	}
	switch o.RequestFormat {
	case RequestOpenAI:
	default:
		return o, fmt.Errorf("unsupported request_format %q", o.RequestFormat)
	}
	if o.ResponseFormat == "" {
		o.ResponseFormat = ResponseOpenAIData
	}
	switch o.ResponseFormat {
	case ResponseOpenAIData, ResponseEmbeddingsArray:
	default:
		return o, fmt.Errorf("unsupported response_format %q", o.ResponseFormat)
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}
	if o.MaxBatch <= 0 {
		o.MaxBatch = 32
	}
	if o.ModelFileField == "" {
		o.ModelFileField = "model_path"
	}
	if o.ExtraRequestFields == nil {
		o.ExtraRequestFields = map[string]any{}
	}
	if o.ExtraHeaders == nil {
		o.ExtraHeaders = map[string]string{}
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: o.Timeout}
	} else if o.HTTPClient.Timeout == 0 {
		o.HTTPClient.Timeout = o.Timeout
	}
	return o, nil
}
