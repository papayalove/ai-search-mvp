package dto

// AdminPartitionsResponse GET /v1/admin/partitions（当前配置 collection 下的分区名）
type AdminPartitionsResponse struct {
	Partitions []string `json:"partitions"`
}
