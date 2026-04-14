-- 将已有 ingest_job（主键为 job_id）迁移为自增 id 主键 + job_id 唯一键。
-- 执行前请备份；若表已是新结构可跳过。

ALTER TABLE `ingest_job` DROP PRIMARY KEY;

ALTER TABLE `ingest_job`
  ADD COLUMN `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '自增主键' FIRST,
  ADD PRIMARY KEY (`id`);

ALTER TABLE `ingest_job`
  ADD UNIQUE KEY `uk_ingest_job_job_id` (`job_id`);
