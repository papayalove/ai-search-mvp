package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/api/middleware"
	"ai-search-v1/internal/config"
	"ai-search-v1/internal/queue"
)

// IngestRemoteHandler POST /v1/admin/ingest/remote
type IngestRemoteHandler struct {
	Broker   *queue.RedisBroker
	QueueEnv config.IngestQueueFromEnv
}

// NewIngestRemoteHandler broker 为 nil 时返回 nil。
func NewIngestRemoteHandler(b *queue.RedisBroker, qe config.IngestQueueFromEnv) *IngestRemoteHandler {
	if b == nil {
		return nil
	}
	return &IngestRemoteHandler{Broker: b, QueueEnv: qe}
}

func (h *IngestRemoteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := middleware.RequestIDFromContext(r)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("read_body", err.Error()))
		return
	}
	var req dto.IngestRemoteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_json", "body must be JSON"))
		return
	}
	if strings.TrimSpace(req.SourceURL) != "" && len(req.S3URIs) == 0 && strings.TrimSpace(req.Bucket) == "" {
		writeJSON(w, http.StatusBadRequest, errBody("url_not_supported", "source_url is reserved; provide s3_uris or bucket+keys/prefix"))
		return
	}
	hasKeys := len(req.Keys) > 0
	hasPrefix := strings.TrimSpace(req.Prefix) != ""
	hasURIs := len(req.S3URIs) > 0
	if !hasURIs && strings.TrimSpace(req.Bucket) == "" {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_request", "need s3_uris or bucket with keys/prefix"))
		return
	}
	if strings.TrimSpace(req.Bucket) != "" && !hasKeys && !hasPrefix && !hasURIs {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_request", "bucket requires keys or prefix or use s3_uris"))
		return
	}

	jobID := uuid.New().String()
	j := queue.Job{
		JobID:       jobID,
		PayloadKind: queue.PayloadKindS3,
		Partition:   strings.TrimSpace(req.Partition),
		Upsert:      req.Upsert,
		ChunkExpand: req.ChunkExpand,
		SourceType:  strings.TrimSpace(req.SourceType),
		Lang:        strings.TrimSpace(req.Lang),
		DocID:       strings.TrimSpace(req.DocID),
		PageNo:      req.PageNo,
		TaskID:      strings.TrimSpace(req.TaskID),
		S3URIs:      req.S3URIs,
		Bucket:      strings.TrimSpace(req.Bucket),
		Keys:        req.Keys,
		Prefix:      strings.TrimSpace(req.Prefix),
	}
	if err := h.Broker.Enqueue(r.Context(), j, h.QueueEnv.RemoteJobMetaTTL); err != nil {
		log.Printf("admin/ingest/remote: enqueue fail request_id=%s err=%v", rid, err)
		writeJSON(w, http.StatusInternalServerError, errBody("enqueue_failed", err.Error()))
		return
	}
	out := dto.IngestRemoteResponse{JobID: jobID, Status: "queued"}
	log.Printf("admin/ingest/remote: queued request_id=%s job_id=%s", rid, jobID)
	writeJSON(w, http.StatusAccepted, out)
}
