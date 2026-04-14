package mysqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
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
	if err != nil {
		log.Printf("ingest_job: InsertQueued failed job_id=%s job_name=%s payload_type=%s request_id=%v total_files=%d err=%v",
			jobID, jobName, payloadType, req, totalFiles, err)
		return err
	}
	log.Printf("ingest_job: created job_id=%s job_name=%s payload_type=%s request_id=%v total_files=%d pipeline_json_bytes=%d",
		jobID, jobName, payloadType, req, totalFiles, len(pipelineJSON))
	return nil
}

// MarkRunning Worker 开始处理。
func (r *IngestJobRepository) MarkRunning(ctx context.Context, jobID string) error {
	if r == nil || r.db == nil {
		return nil
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE ingest_job SET status='running', started_at=UTC_TIMESTAMP(), updated_at=UTC_TIMESTAMP() WHERE job_id=?`, jobID)
	if err != nil {
		log.Printf("ingest_job: MarkRunning failed job_id=%s err=%v", jobID, err)
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Printf("ingest_job: MarkRunning RowsAffected failed job_id=%s err=%v", jobID, err)
		return err
	}
	if n == 0 {
		err := fmt.Errorf("mysqldb: MarkRunning updated 0 rows (missing ingest_job row for job_id=%q?)", jobID)
		log.Printf("ingest_job: MarkRunning job_id=%s: %v", jobID, err)
		return err
	}
	log.Printf("ingest_job: status=running job_id=%s rows_affected=%d", jobID, n)
	return nil
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
// successDocs/failDocs：逻辑上处理的「输入文档」计数（如 NDJSON 行数、整文件算 1）；与 ES task 上 success_docs/fail_docs 一致。
// chunks：本文件写入 Milvus 的 chunk 行数（RunStats.ChunksWritten，含按 ingest.chunk_* 切分后的多段），不是 doc 数。
// total_docs：本 job 累计处理的 doc 单位数，等于各次 successDocs+failDocs 之和（完成后与 success_docs+fail_docs 一致）。
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
	docDelta := successDocs + failDocs
	if docDelta < 0 {
		docDelta = 0
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE ingest_job SET
  success_files = success_files + ?,
  fail_files = fail_files + ?,
  success_docs = success_docs + ?,
  fail_docs = fail_docs + ?,
  total_docs = total_docs + ?,
  total_chunks = total_chunks + ?,
  updated_at = UTC_TIMESTAMP()
WHERE job_id=?`, sf, ff, successDocs, failDocs, docDelta, chunks, jobID)
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
	res, err := r.db.ExecContext(ctx, `
UPDATE ingest_job SET status=?, finished_at=UTC_TIMESTAMP(), last_error=?, updated_at=UTC_TIMESTAMP() WHERE job_id=?`,
		status, errPtr, jobID)
	if err != nil {
		log.Printf("ingest_job: MarkTerminal failed job_id=%s terminal_status=%s err=%v", jobID, status, err)
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Printf("ingest_job: MarkTerminal RowsAffected failed job_id=%s terminal_status=%s err=%v", jobID, status, err)
		return err
	}
	if n == 0 {
		err := fmt.Errorf("mysqldb: MarkTerminal updated 0 rows (missing ingest_job row for job_id=%q?)", jobID)
		log.Printf("ingest_job: MarkTerminal job_id=%s terminal_status=%s: %v", jobID, status, err)
		return err
	}
	if strings.TrimSpace(lastErr) != "" {
		ex := lastErr
		if len(ex) > 256 {
			ex = ex[:256] + "…"
		}
		log.Printf("ingest_job: finished job_id=%s status=%s rows_affected=%d last_error_excerpt=%q", jobID, status, n, ex)
	} else {
		log.Printf("ingest_job: finished job_id=%s status=%s rows_affected=%d", jobID, status, n)
	}
	return nil
}
