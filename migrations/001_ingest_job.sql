-- MySQL 8+ 推荐；低版本若不支持表达式默认值，可去掉 DEFAULT (NOW()) 改由应用写入时间。
CREATE TABLE IF NOT EXISTS `ingest_job` (
  `job_id`            CHAR(36)     NOT NULL COMMENT '入队任务 ID',
  `job_name`          VARCHAR(64)  NOT NULL COMMENT '任务展示名',
  `status`            VARCHAR(32)  NOT NULL DEFAULT 'queued' COMMENT 'queued|running|succeeded|failed|cancelled|pause',
  `payload_type`      VARCHAR(32)  NOT NULL COMMENT 'multipart_redis|s3',
  `request_id`        VARCHAR(128) NULL,
  `started_at`        DATETIME     NULL,
  `finished_at`       DATETIME     NULL,
  `total_files`       INT UNSIGNED NOT NULL DEFAULT 0,
  `success_files`     INT UNSIGNED NOT NULL DEFAULT 0,
  `fail_files`        INT UNSIGNED NOT NULL DEFAULT 0,
  `total_docs`        INT UNSIGNED NOT NULL DEFAULT 0,
  `success_docs`      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `fail_docs`         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `total_chunks`      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  `last_error`        VARCHAR(2048) NULL,
  `pipeline_params`   JSON         NULL,
  `created_at`        DATETIME     NOT NULL DEFAULT (NOW()),
  `updated_at`        DATETIME     NOT NULL DEFAULT (NOW()) ON UPDATE NOW(),
  PRIMARY KEY (`job_id`),
  KEY `idx_ingest_job_status_created` (`status`, `created_at`),
  KEY `idx_ingest_job_finished` (`finished_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
