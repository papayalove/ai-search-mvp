package dto

// ContentResponse 为 GET /v1/content 的 JSON 体：按字节区间读取后的 UTF-8 文本（非法字节已替换）。
type ContentResponse struct {
	Text          string `json:"text"`
	BytesReturned int    `json:"bytes_returned"`
	NextOffset    int64  `json:"next_offset"`
	// More 为 true 表示本次读满 limit 字节，调用方可将 next_offset 作为下一请求的 offset 继续拉取。
	More bool `json:"more"`
}
