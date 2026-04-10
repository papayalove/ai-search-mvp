package mysqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// IngestJobRepository ingest_job 表访问。
type IngestJobRepository struct {
	db *sql.DB
}

// Open 打开 MySQL（DSN 与 go-sql-driver/mysql 一致）。
func Open(dsn string) (*IngestJobRepository, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("mysqldb: empty dsn")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("mysqldb ping: %w", wrapMySQLAccessHint(err))
	}
	return &IngestJobRepository{db: db}, nil
}

// wrapMySQLAccessHint 为常见认证错误补充 DSN 说明（不替换原错误）。
func wrapMySQLAccessHint(err error) error {
	if err == nil {
		return nil
	}
	var me *mysqldriver.MySQLError
	if errors.As(err, &me) && me.Number == 1045 {
		return fmt.Errorf("%v — 提示: Error 1045 且 using password: NO 表示 DSN 未带上密码；有密码时应为 user:password@tcp(host:port)/dbname?parseTime=true&loc=UTC；密码含 @ : / 等字符需按 driver 文档做转义/URL 编码", err)
	}
	return err
}

func (r *IngestJobRepository) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// InsertQueued 新建 Job 行。
func (r *IngestJobRepository) InsertQueued(ctx context.Context, jobID, jobName, payloadType, requestID string, totalFiles int, pipelineJSON []byte) error {
	if r == nil || r.db == nil {
		return nil
	}
	if jobName == "" {
		jobName = "ingest"
	}
	var req interface{}
	if strings.TrimSpace(requestID) != "" {
		req = requestID
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO ingest_job (job_id, job_name, status, payload_type, request_id, total_files, pipeline_params, created_at, updated_at)
VALUES (?, ?, 'queued', ?, ?, ?, ?, UTC_TIMESTAMP(), UTC_TIMESTAMP())
`, jobID, jobName, payloadType, req, totalFiles, pipelineJSON)
	return err
}

// MarkRunning Worker 开始处理。
func (r *IngestJobRepository) MarkRunning(ctx context.Context, jobID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE ingest_job SET status='running', started_at=UTC_TIMESTAMP(), updated_at=UTC_TIMESTAMP() WHERE job_id=?`, jobID)
	return err
}

// SetTotalFiles 修正文件总数（如 S3 list 后）。
func (r *IngestJobRepository) SetTotalFiles(ctx context.Context, jobID string, n int) error {
	if r == nil || r.db == nil {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE ingest_job SET total_files=?, updated_at=UTC_TIMESTAMP() WHERE job_id=?`, n, jobID)
	return err
}

// AddFileOutcome 单文件完成统计。
func (r *IngestJobRepository) AddFileOutcome(ctx context.Context, jobID string, fileOK bool, successDocs, failDocs, chunks int64) error {
	if r == nil || r.db == nil {
		return nil
	}
	sf, ff := 0, 0
	if fileOK {
		sf = 1
	} else {
		ff = 1
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE ingest_job SET
  success_files = success_files + ?,
  fail_files = fail_files + ?,
  success_docs = success_docs + ?,
  fail_docs = fail_docs + ?,
  total_chunks = total_chunks + ?,
  updated_at = UTC_TIMESTAMP()
WHERE job_id=?`, sf, ff, successDocs, failDocs, chunks, jobID)
	return err
}

// MarkTerminal 整 Job 终态。
func (r *IngestJobRepository) MarkTerminal(ctx context.Context, jobID, status, lastErr string) error {
	if r == nil || r.db == nil {
		return nil
	}
	var errPtr interface{}
	if strings.TrimSpace(lastErr) != "" {
		le := lastErr
		if len(le) > 2048 {
			le = le[:2048]
		}
		errPtr = le
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE ingest_job SET status=?, finished_at=UTC_TIMESTAMP(), last_error=?, updated_at=UTC_TIMESTAMP() WHERE job_id=?`,
		status, errPtr, jobID)
	return err
}
