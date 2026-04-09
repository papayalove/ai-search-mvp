package handler

import (
	"log"
	"net/http"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/api/middleware"
	"ai-search-v1/internal/storage/milvus"
)

// AdminCollectionsHandler GET /v1/admin/collections — 列出当前 Milvus 数据库中的 collection 名。
type AdminCollectionsHandler struct {
	Repo *milvus.Repository
}

func NewAdminCollectionsHandler(repo *milvus.Repository) *AdminCollectionsHandler {
	return &AdminCollectionsHandler{Repo: repo}
}

func (h *AdminCollectionsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rid := middleware.RequestIDFromContext(r)
	if r.Method != http.MethodGet {
		log.Printf("admin/collections: reject_method request_id=%s method=%s", rid, r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.Repo == nil {
		log.Printf("admin/collections: reject request_id=%s reason=repo_nil", rid)
		writeJSON(w, http.StatusServiceUnavailable, errBody("milvus_unavailable", "milvus is not configured"))
		return
	}
	log.Printf("admin/collections: begin request_id=%s", rid)
	names, err := h.Repo.ListCollectionNames(r.Context())
	if err != nil {
		log.Printf("admin/collections: list_fail request_id=%s err=%v", rid, err)
		writeJSON(w, http.StatusInternalServerError, errBody("list_collections_failed", err.Error()))
		return
	}
	log.Printf("admin/collections: ok request_id=%s count=%d names=%q", rid, len(names), names)
	writeJSON(w, http.StatusOK, dto.AdminCollectionsResponse{Collections: names})
}
