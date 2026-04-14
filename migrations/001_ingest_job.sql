-- MySQL 8+ 推荐；低版本若不支持表达式默认值，可去掉 DEFAULT (NOW()) 改由应用写入时间。
-- 已有旧表（仅 job_id 主键）请另执行 migrations/002_ingest_job_id_pk.sql。
CREATE TABLE IF NOT EXISTS `ingest_job` (
  `id`                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键',
  `job_id`            CHAR(36)     NOT NULL COMMENT '入队任务 ID',
  `job_name`          VARCHAR(64)  NOT NULL COMMENT '任务展示名',
  `status`            VARCHAR(32)  NOT NULL DEFAULT 'queued' COMMENT 'queued|running|succeeded|failed|cancelled|pause',
  `payload_type`      VARCHAR(32)  NOT NULL COMMENT 'multipart_redis|s3',
  `request_id`        VARCHAR(128) NULL,
  `started_at`        DATETIME(3)  NULL,
  `finished_at`       DATETIME(3)  NULL,
  `total_files`       INT UNSIGNED NOT NULL DEFAULT 0,
  `success_files`     INT UNSIGNED NOT NULL DEFAULT 0,
  `fail_files`        INT UNSIGNED NOT NULL DEFAULT 0,
  `total_docs`        INT UNSIGNED NOT NULL DEFAULT 0 COMMENT '累计处理的输入 doc 单位（各文件 success+fail doc 之和，与 success_docs+fail_docs 一致）',
  `success_docs`      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '成功处理的输入行/文档数（如 NDJSON 行）',
  `fail_docs`         BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '失败处理的输入行/文档数',
  `total_chunks`      BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '写入 Milvus 的 chunk 行数（非 doc 数）',
  `last_error`        VARCHAR(2048) NULL,
  `pipeline_params`   JSON         NULL,
  `created_at`        DATETIME     NOT NULL DEFAULT (NOW()),
  `updated_at`        DATETIME     NOT NULL DEFAULT (NOW()) ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_ingest_job_job_id` (`job_id`),
  KEY `idx_ingest_job_status_created` (`status`, `created_at`),
  KEY `idx_ingest_job_finished` (`finished_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
