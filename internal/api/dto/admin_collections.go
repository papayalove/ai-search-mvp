package dto

// AdminCollectionsResponse GET /v1/admin/collections
type AdminCollectionsResponse struct {
	Collections []string `json:"collections"`
}
