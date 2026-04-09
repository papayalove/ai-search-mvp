package handler

import (
	"log"
	"net/http"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/api/middleware"
	"ai-search-v1/internal/storage/milvus"
)

// AdminPartitionsHandler GET /v1/admin/partitions — 列出 api.yaml 中 milvus.collection 下的分区。
type AdminPartitionsHandler struct {
	Repo *milvus.Repository
}

func NewAdminPartitionsHandler(repo *milvus.Repository) *AdminPartitionsHandler {
	return &AdminPartitionsHandler{Repo: repo}
}

func (h *AdminPartitionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := middleware.RequestIDFromContext(r)
	if r.Method != http.MethodGet {
		log.Printf("admin/partitions: reject_method request_id=%s method=%s", rid, r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.Repo == nil {
		log.Printf("admin/partitions: reject request_id=%s reason=repo_nil", rid)
		writeJSON(w, http.StatusServiceUnavailable, errBody("milvus_unavailable", "milvus is not configured"))
		return
	}
	log.Printf("admin/partitions: begin request_id=%s collection=%q", rid, h.Repo.Config().Collection)
	names, err := h.Repo.ListPartitionNames(r.Context())
	if err != nil {
		log.Printf("admin/partitions: list_fail request_id=%s err=%v", rid, err)
		writeJSON(w, http.StatusInternalServerError, errBody("list_partitions_failed", err.Error()))
		return
	}
	log.Printf("admin/partitions: ok request_id=%s count=%d", rid, len(names))
	writeJSON(w, http.StatusOK, dto.AdminPartitionsResponse{Partitions: names})
}
