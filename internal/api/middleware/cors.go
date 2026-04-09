package middleware

import "net/http"

// CORS 允许浏览器跨域调用 API（如 Vite 开发服 :8008 → Go :8080）。
// 对带 Origin 的请求回显该 Origin，便于本地与内网调试；生产环境建议改为白名单。
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, PATCH, DELETE")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID, X-Search-Debug")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
