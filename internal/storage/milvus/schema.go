package milvus

import (
	"fmt"

	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

// Field names for chunk vector collection.
const (
	FieldChunkID      = "chunk_id"
	FieldDocID        = "doc_id"
	FieldEmbedding    = "embedding"
	FieldSourceType   = "source_type"
	FieldLang         = "lang"
	FieldJobID        = "job_id"
	FieldTaskID       = "task_id"
	FieldExtraInfo    = "extra_info" // JSON string, VarChar
	FieldCreatedTime  = "created_time"
	FieldUpdatedTime  = "updated_time"
)

const defaultCollectionName = "chunk_vectors_v1"

func collectionSchema(cfg Config) (*entity.Schema, error) {
	if cfg.VectorDim <= 0 {
		return nil, fmt.Errorf("vector dim must be positive")
	}
	maxLen := int64(cfg.MaxChunkIDLen)
	if maxLen <= 0 {
		maxLen = defaultMaxChunkIDLen
	}
	docLen := int64(cfg.MaxDocIDLen)
	if docLen <= 0 {
		docLen = defaultMaxChunkIDLen
	}
	jobLen := int64(cfg.MaxJobIDLen)
	if jobLen <= 0 {
		jobLen = defaultMaxJobIDLen
	}
	taskLen := int64(cfg.MaxTaskIDLen)
	if taskLen <= 0 {
		taskLen = defaultMaxTaskIDLen
	}
	exLen := int64(cfg.MaxExtraInfoLen)
	if exLen <= 0 {
		exLen = defaultMaxExtraInfoLen
	}
	return entity.NewSchema().
		WithName(cfg.Collection).
		WithDescription("chunk-level vectors for semantic search MVP").
		WithAutoID(false).
		WithDynamicFieldEnabled(false).
		WithField(
			entity.NewField().
				WithName(FieldChunkID).
				WithDataType(entity.FieldTypeVarChar).
				WithIsPrimaryKey(true).
				WithIsAutoID(false).
				WithMaxLength(maxLen),
		).
		WithField(
			entity.NewField().
				WithName(FieldDocID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(docLen),
		).
		WithField(
			entity.NewField().
				WithName(FieldEmbedding).
				WithDataType(entity.FieldTypeFloatVector).
				WithDim(int64(cfg.VectorDim)),
		).
		WithField(
			entity.NewField().
				WithName(FieldSourceType).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(32),
		).
		WithField(
			entity.NewField().
				WithName(FieldLang).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(32),
		).
		WithField(
			entity.NewField().
				WithName(FieldJobID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(jobLen),
		).
		WithField(
			entity.NewField().
				WithName(FieldTaskID).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(taskLen),
		).
		WithField(
			entity.NewField().
				WithName(FieldExtraInfo).
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(exLen),
		).
		WithField(
			entity.NewField().
				WithName(FieldCreatedTime).
				WithDataType(entity.FieldTypeInt64),
		).
		WithField(
			entity.NewField().
				WithName(FieldUpdatedTime).
				WithDataType(entity.FieldTypeInt64),
		), nil
}
