package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/api/middleware"
	"ai-search-v1/internal/config"
	"ai-search-v1/internal/ingest/chunk"
	"ai-search-v1/internal/queue"
)

const (
	maxMultipartBody = 64 << 20
	maxIngestFile    = 32 << 20
)

// IngestHandler multipart → Redis 正文 + 入队（202）。
type IngestHandler struct {
	Broker    *queue.RedisBroker
	QueueEnv  config.IngestQueueFromEnv
	ChunkOpts chunk.RecursiveChunkOptions
}

// NewIngestHandler broker 为 nil 时返回 nil（不注册路由）。
func NewIngestHandler(b *queue.RedisBroker, qe config.IngestQueueFromEnv, co chunk.RecursiveChunkOptions) *IngestHandler {
	if b == nil {
		return nil
	}
	return &IngestHandler{Broker: b, QueueEnv: qe, ChunkOpts: co}
}

func (h *IngestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := middleware.RequestIDFromContext(r)
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBody)
	if err := r.ParseMultipartForm(maxMultipartBody); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_multipart", err.Error()))
		return
	}

	partition := strings.TrimSpace(r.FormValue("partition"))
	upsert := formBool(r.FormValue("upsert"))
	chunkExpand := formBool(r.FormValue("chunk"))
	sourceType := strings.TrimSpace(r.FormValue("source_type"))
	lang := strings.TrimSpace(r.FormValue("lang"))
	chunkIDForm := strings.TrimSpace(r.FormValue("chunk_id"))
	docID := strings.TrimSpace(r.FormValue("doc_id"))
	taskID := strings.TrimSpace(r.FormValue("task_id"))
	pageNo := 0
	if v := strings.TrimSpace(r.FormValue("page_no")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			pageNo = n
		}
	}

	headers := r.MultipartForm.File["files"]
	if len(headers) == 0 {
		if fh := r.MultipartForm.File["file"]; len(fh) > 0 {
			headers = fh
		}
	}
	if len(headers) == 0 {
		writeJSON(w, http.StatusBadRequest, errBody("no_files", "use form field \"files\" (repeatable) or \"file\""))
		return
	}

	jobID := uuid.New().String()
	var files []queue.FileRef
	var accepted []dto.IngestAcceptedFile

	for i, fh := range headers {
		ext := strings.ToLower(strings.TrimSpace(filepath.Ext(fh.Filename)))
		switch ext {
		case ".ndjson", ".jsonl", ".txt", ".md", ".markdown":
		default:
			writeJSON(w, http.StatusBadRequest, errBody("unsupported_type", fmt.Sprintf("file %q: use .ndjson, .jsonl, .txt, .md", fh.Filename)))
			return
		}
		f, err := fh.Open()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("open_failed", err.Error()))
			return
		}
		body, err := io.ReadAll(io.LimitReader(f, maxIngestFile+1))
		_ = f.Close()
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("read_failed", err.Error()))
			return
		}
		if len(body) > maxIngestFile {
			writeJSON(w, http.StatusBadRequest, errBody("too_large", fmt.Sprintf("%s exceeds %d bytes", fh.Filename, maxIngestFile)))
			return
		}
		pkey := queue.PayloadRedisKey(jobID, i)
		if err := h.Broker.SetPayload(r.Context(), pkey, body, h.QueueEnv.MultipartPayloadTTL); err != nil {
			log.Printf("admin/ingest: redis set fail request_id=%s job_id=%s err=%v", rid, jobID, err)
			writeJSON(w, http.StatusInternalServerError, errBody("redis_failed", err.Error()))
			return
		}
		files = append(files, queue.FileRef{PayloadKey: pkey, Filename: fh.Filename})
		accepted = append(accepted, dto.IngestAcceptedFile{Name: fh.Filename, PayloadKey: pkey})
	}

	j := queue.Job{
		JobID:       jobID,
		PayloadKind: queue.PayloadKindMultipartRedis,
		Partition:   partition,
		Upsert:      upsert,
		ChunkExpand: chunkExpand,
		SourceType:  sourceType,
		Lang:        lang,
		DocID:       docID,
		PageNo:      pageNo,
		ChunkID:     chunkIDForm,
		TaskID:      taskID,
		Files:       files,
	}
	if err := h.Broker.Enqueue(r.Context(), j, h.QueueEnv.MultipartPayloadTTL); err != nil {
		log.Printf("admin/ingest: enqueue fail request_id=%s err=%v", rid, err)
		writeJSON(w, http.StatusInternalServerError, errBody("enqueue_failed", err.Error()))
		return
	}

	out := dto.IngestAcceptedResponse{
		JobID:  jobID,
		Status: "queued",
		Files:  accepted,
	}
	if payload, err := json.Marshal(out); err == nil {
		log.Printf("admin/ingest: queued request_id=%s body=%s", rid, payload)
	}
	writeJSON(w, http.StatusAccepted, out)
}

func formBool(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}
