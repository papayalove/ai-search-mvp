package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ai-search-v1/internal/api/dto"
	"ai-search-v1/internal/query"
	storages3 "ai-search-v1/internal/storage/s3"
)

const (
	contentDefaultLimit int64 = 16 * 1024
	contentMaxLimit     int64 = 512 * 1024
	contentHTTPTimeout        = 45 * time.Second
)

// ContentHandler 提供 GET /v1/content：按 source（http(s) 或 s3://）与字节 offset 读取一段原文。
// S3 为 nil 时 s3:// 源返回 503；HTTP 源不依赖 S3。
type ContentHandler struct {
	S3   *storages3.Client
	HTTP *http.Client
}

// NewContentHandler 返回处理器；s3 可为 nil（仅 HTTP 源可用）。
func NewContentHandler(s3 *storages3.Client) http.Handler {
	return &ContentHandler{
		S3: s3,
		HTTP: &http.Client{
			Timeout: contentHTTPTimeout,
		},
	}
}

func (h *ContentHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h == nil || h.HTTP == nil {
		writeJSON(w, http.StatusInternalServerError, errBody("server_error", "content handler not initialized"))
		return
	}
	q := r.URL.Query()
	src := strings.TrimSpace(q.Get("source"))
	if src == "" {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_request", "source is required"))
		return
	}
	if query.ContentFetchSource(src, src) == "" {
		writeJSON(w, http.StatusBadRequest, errBody("invalid_request", "source must be http(s) or s3:// URL"))
		return
	}
	off, err := strconv.ParseInt(strings.TrimSpace(q.Get("offset")), 10, 64)
	if err != nil || off < 0 {
		off = 0
	}
	lim := contentDefaultLimit
	if ls := strings.TrimSpace(q.Get("limit")); ls != "" {
		n, err := strconv.ParseInt(ls, 10, 64)
		if err == nil && n > 0 {
			lim = n
		}
	}
	if lim > contentMaxLimit {
		lim = contentMaxLimit
	}

	ctx := r.Context()
	var raw []byte
	lu := strings.ToLower(src)
	if strings.HasPrefix(lu, "s3://") {
		if h.S3 == nil {
			writeJSON(w, http.StatusServiceUnavailable, errBody("s3_disabled", "S3 client is not configured for this server"))
			return
		}
		bucket, key, ok := storages3.ParseS3URI(src)
		if !ok {
			writeJSON(w, http.StatusBadRequest, errBody("invalid_request", "invalid s3:// URI"))
			return
		}
		raw, err = h.S3.GetObjectRange(ctx, bucket, key, off, lim)
	} else {
		raw, err = h.fetchHTTPRange(ctx, src, off, lim)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errBody("fetch_failed", err.Error()))
		return
	}
	n := len(raw)
	text := strings.ToValidUTF8(string(raw), "\uFFFD")
	next := off + int64(n)
	more := int64(n) >= lim && n > 0
	writeJSON(w, http.StatusOK, dto.ContentResponse{
		Text:          text,
		BytesReturned: n,
		NextOffset:    next,
		More:          more,
	})
}

func (h *ContentHandler) fetchHTTPRange(ctx context.Context, rawURL string, start, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u == nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	end := start + maxBytes - 1
	if end < start {
		return nil, fmt.Errorf("invalid byte range")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("HTTP %s: %s", resp.Status, msg)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return data[:maxBytes], nil
	}
	return data, nil
}
