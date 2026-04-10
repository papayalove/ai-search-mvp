package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/api/middleware"
	"ai-search-v1/internal/query/pipeline"
)

// AdminQueryHandler POST /v1/admin/query（暂不鉴权）；内部走 Searcher.Search，search_type 映射为 SearchInput.SearchType。
type AdminQueryHandler struct {
	Searcher pipeline.Searcher
}

func NewAdminQueryHandler(s pipeline.Searcher) *AdminQueryHandler {
	if s == nil {
		return nil
	}
	return &AdminQueryHandler{Searcher: s}
}

func (h *AdminQueryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("read_body", err.Error()))
		return
	}
	rid := middleware.RequestIDFromContext(r)
	log.Printf("admin/query: raw_body request_id=%s bytes=%d body=%s", rid, len(raw), string(raw))

	var req dto.AdminQueryRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_json", "request body must be JSON"))
		return
	}
	if dec, err := json.Marshal(req); err == nil {
		log.Printf("admin/query: decoded dto.AdminQueryRequest request_id=%s json=%s", rid, dec)
	}
	searchType, err := normalizeSearchType(req.SearchType)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_request", err.Error()))
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	in := pipeline.SearchInput{
		Query:        req.Q,
		TopK:         limit,
		SearchType:   searchType,
		Retrieval:    strings.TrimSpace(req.RetrievalMode),
		IncludeDebug: false,
	}
	if inJSON, err := json.Marshal(in); err == nil {
		log.Printf("admin/query: pipeline.SearchInput request_id=%s json=%s", rid, inJSON)
	}
	out, err := h.Searcher.Search(r.Context(), in)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "requires embedding") || strings.Contains(msg, "chunk lookup text mode") {
			writeJSON(w, http.StatusServiceUnavailable, errBody("embed_required", msg))
			return
		}
		if strings.Contains(msg, "invalid mode") || strings.Contains(msg, "requires Milvus") {
			writeJSON(w, http.StatusBadRequest, errBody("invalid_request", msg))
			return
		}
		if strings.Contains(msg, "elasticsearch is disabled") {
			writeJSON(w, http.StatusServiceUnavailable, errBody("es_disabled", msg))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody("admin_query_failed", msg))
		return
	}
	coll, dim := "", 0
	if out.ChunkRun != nil {
		coll = out.ChunkRun.Collection
		dim = out.ChunkRun.VectorDim
	}
	records := make([]dto.AdminQueryRecord, 0, len(out.Hits))
	for _, hit := range out.Hits {
		records = append(records, searchHitToAdminRecord(hit, coll, dim))
	}
	writeJSON(w, http.StatusOK, dto.AdminQueryResponse{Records: records})
}

func normalizeSearchType(searchType string) (string, error) {
	st := strings.ToLower(strings.TrimSpace(searchType))
	switch st {
	case "file_name", "filename":
		return "file_name", nil
	case "chunk_id", "id":
		return "chunk_id", nil
	case "text":
		return "text", nil
	case "":
		return "", fmt.Errorf("search_type is required (file_name, chunk_id, text)")
	default:
		return "", fmt.Errorf("invalid search_type %q", searchType)
	}
}

func searchHitToAdminRecord(hit pipeline.SearchHit, collection string, vectorDim int) dto.AdminQueryRecord {
	meta := map[string]string{
		"source_type": hit.SourceType,
		"lang":        hit.Lang,
	}
	if hit.Score != 0 {
		meta["score"] = fmt.Sprintf("%g", hit.Score)
	}
	// Ts：更新时间（JSON ts）；CreatedAt：创建时间，来自 Milvus/ES 的 created，避免把 updated 误标为 Created、避免 Ts=0 时用 Now() 造假。
	updatedMs := hit.Ts
	createdMs := hit.CreatedTs
	if createdMs <= 0 && updatedMs > 0 {
		createdMs = updatedMs
	}
	var createdAt string
	if createdMs > 0 {
		createdAt = time.UnixMilli(createdMs).UTC().Format(time.RFC3339Nano)
	}
	tsOut := updatedMs
	if tsOut <= 0 && createdMs > 0 {
		tsOut = createdMs
	}
	id := hit.ChunkID
	return dto.AdminQueryRecord{
		ID:           id,
		ChunkID:      id,
		DocID:        hit.DocID,
		FileName:     deriveFileNameFromChunkID(id),
		Collection: collection,
		SourceType:   hit.SourceType,
		Lang:         hit.Lang,
		Score:        hit.Score,
		Ts:           tsOut,
		Status:       "indexed",
		VectorDim:    vectorDim,
		Metadata:     meta,
		CreatedAt:    createdAt,
		RecallSource: hit.RecallSource,
	}
}

func deriveFileNameFromChunkID(chunkID string) string {
	if chunkID == "" {
		return ""
	}
	if i := strings.LastIndex(chunkID, "/"); i >= 0 && i+1 < len(chunkID) {
		return chunkID[i+1:]
	}
	return chunkID
}
