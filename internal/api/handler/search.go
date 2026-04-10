package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/query/pipeline"
)

const (
	defaultTopK = 10
	maxTopK     = 100
	maxQueryLen = 4096
)

// SearchHandler serves POST /v1/search.
type SearchHandler struct {
	Searcher pipeline.Searcher
}

// NewSearchHandler returns a handler with the given searcher (required).
func NewSearchHandler(s pipeline.Searcher) *SearchHandler {
	if s == nil {
		s = pipeline.StubSearcher{}
	}
	return &SearchHandler{Searcher: s}
}

func (h *SearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req dto.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_json", "request body must be JSON"))
		return
	}
	if err := validateSearchRequest(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_request", err.Error()))
		return
	}
	topK := req.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	includeDebug := r.Header.Get("X-Search-Debug") == "1"
	in := pipeline.SearchInput{
		Query:        strings.TrimSpace(req.Query),
		TopK:         topK,
		SourceTypes:  normalizeSourceTypes(req.SourceTypes),
		Filters:      req.Filters,
		RequestID:    req.RequestID,
		IncludeDebug: includeDebug,
		Retrieval:    strings.TrimSpace(req.Retrieval),
	}
	out, err := h.Searcher.Search(r.Context(), in)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "requires embedding") || strings.Contains(msg, "chunk lookup embed") || strings.Contains(msg, "embedder is nil") {
			writeJSON(w, http.StatusServiceUnavailable, errBody("embed_required", msg))
			return
		}
		if strings.Contains(msg, "elasticsearch is disabled") {
			writeJSON(w, http.StatusServiceUnavailable, errBody("es_disabled", msg))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody("search_failed", msg))
		return
	}
	resp := dto.SearchResponse{Hits: toDTOHits(out.Hits)}
	if out.Debug != nil {
		resp.Debug = &dto.SearchDebug{
			Rewrites:     out.Debug.Rewrites,
			RecallCounts: out.Debug.RecallCounts,
			MergedCount:  out.Debug.MergedCount,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func validateSearchRequest(req *dto.SearchRequest) error {
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return errors.New("query is required")
	}
	if len(q) > maxQueryLen {
		return errors.New("query exceeds maximum length")
	}
	if req.TopK < 0 || req.TopK > maxTopK {
		return errors.New("top_k must be 0 or omitted for default, or between 1 and 100")
	}
	for _, raw := range req.SourceTypes {
		st := strings.TrimSpace(strings.ToLower(raw))
		if st != "web" && st != "pdf" {
			return errors.New("source_types entries must be web or pdf")
		}
	}
	return nil
}

func normalizeSourceTypes(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		out = append(out, strings.TrimSpace(strings.ToLower(raw)))
	}
	return out
}

func toDTOHits(hits []pipeline.SearchHit) []dto.SearchHit {
	if len(hits) == 0 {
		return []dto.SearchHit{}
	}
	out := make([]dto.SearchHit, len(hits))
	for i := range hits {
		h := hits[i]
		out[i] = dto.SearchHit{
			ChunkID:     h.ChunkID,
			DocID:       h.DocID,
			Snippet:     h.Snippet,
			Score:       h.Score,
			SourceType:  h.SourceType,
			URLOrDocID:  h.URLOrDocID,
			PDFPage:     h.PDFPage,
			Title:       h.Title,
		}
	}
	return out
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func errBody(code, msg string) apiError {
	var e apiError
	e.Error.Code = code
	e.Error.Message = msg
	return e
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
