package middleware

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	code int
	// ingestFilenames 仅用于 AccessLog 一行摘要，不写入 HTTP 响应头。
	ingestFilenames []string
}

func (w *statusRecorder) WriteHeader(status int) {
	w.code = status
	w.ResponseWriter.WriteHeader(status)
}

// NoteIngestFilenames 供 POST /v1/admin/ingest 等在处理早期登记入参文件名，供 AccessLog 打印。
// w 须为本包 AccessLog 注入的 ResponseWriter；否则静默忽略。
func NoteIngestFilenames(w http.ResponseWriter, names []string) {
	rec, ok := w.(*statusRecorder)
	if !ok || len(names) == 0 {
		return
	}
	rec.ingestFilenames = append([]string(nil), names...)
}

func (w *statusRecorder) ingestFilenamesLogFragment() string {
	if len(w.ingestFilenames) == 0 {
		return ""
	}
	b, err := json.Marshal(w.ingestFilenames)
	if err != nil {
		return " filenames=" + strings.Join(w.ingestFilenames, ",")
	}
	s := string(b)
	const max = 800
	if len(s) > max {
		s = s[:max] + "…"
	}
	return " filenames=" + s
}

// AccessLog 记录每个请求的 method、path、状态码、耗时与 X-Request-ID（若有）。
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		code := rec.code
		if code == 0 {
			code = http.StatusOK
		}
		rid := RequestIDFromContext(r)
		fn := rec.ingestFilenamesLogFragment()
		if rid != "" {
			log.Printf("http: %s %s -> %d (%v) request_id=%s remote=%s%s", r.Method, r.URL.Path, code, time.Since(start).Truncate(time.Millisecond), rid, r.RemoteAddr, fn)
		} else {
			log.Printf("http: %s %s -> %d (%v) remote=%s%s", r.Method, r.URL.Path, code, time.Since(start).Truncate(time.Millisecond), r.RemoteAddr, fn)
		}
	})
}
