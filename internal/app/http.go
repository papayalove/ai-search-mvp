package app

import (
	"net/http"

	"ai-search-v1/internal/api/handler"
	"ai-search-v1/internal/api/middleware"
	"ai-search-v1/internal/config"
	"ai-search-v1/internal/ingest/chunk"
	ingestmeta "ai-search-v1/internal/ingest/meta"
	querypipe "ai-search-v1/internal/query/pipeline"
	"ai-search-v1/internal/queue"
	"ai-search-v1/internal/storage/milvus"
	storages3 "ai-search-v1/internal/storage/s3"
)

// NewHTTPServer builds the API HTTP handler stack (middleware + routes).
// ingestBroker 非 nil 时注册 POST /v1/admin/ingest 与 POST /v1/admin/ingest/remote（异步入队）。
// contentS3 非 nil 时 GET /v1/content 支持 s3:// 源（HTTP 源不依赖 S3）。
func NewHTTPServer(searcher querypipe.Searcher, ingestBroker *queue.RedisBroker, ingestQE config.IngestQueueFromEnv, ingestChunkOpts chunk.RecursiveChunkOptions, milvusRepo *milvus.Repository, ingestMeta *ingestmeta.Service, contentS3 *storages3.Client) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST /v1/search", handler.NewSearchHandler(searcher))
	mux.Handle("GET /v1/content", handler.NewContentHandler(contentS3))
	mux.Handle("GET /v1/admin/collections", handler.NewAdminCollectionsHandler(milvusRepo))
	mux.Handle("GET /v1/admin/partitions", handler.NewAdminPartitionsHandler(milvusRepo))
	if ih := handler.NewIngestHandler(ingestBroker, ingestQE, ingestChunkOpts, ingestMeta); ih != nil {
		mux.Handle("POST /v1/admin/ingest", ih)
	}
	if ir := handler.NewIngestRemoteHandler(ingestBroker, ingestQE, ingestMeta); ir != nil {
		mux.Handle("POST /v1/admin/ingest/remote", ir)
	}
	if aq := handler.NewAdminQueryHandler(searcher); aq != nil {
		mux.Handle("POST /v1/admin/query", aq)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	var h http.Handler = mux
	h = middleware.AccessLog(h)
	h = middleware.RequestID(h)
	h = middleware.CORS(h)
	h = middleware.Recover(h)
	return h
}
