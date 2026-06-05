USE short_url;

CREATE TABLE IF NOT EXISTS short_url_id_alloc (
  name    VARCHAR(64)     NOT NULL PRIMARY KEY,
  next_id BIGINT UNSIGNED NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

DELIMITER //
CREATE PROCEDURE create_short_url_shards()
BEGIN
  DECLARE i INT DEFAULT 0;
  DECLARE table_name VARCHAR(64);
  WHILE i < 64 DO
    SET table_name = CONCAT('short_urls_', LPAD(i, 2, '0'));
    SET @ddl = CONCAT(
      'CREATE TABLE IF NOT EXISTS ', table_name, ' (',
      'id BIGINT UNSIGNED NOT NULL PRIMARY KEY,',
      'short_code VARCHAR(16) NOT NULL,',
      'origin_url TEXT NOT NULL,',
      'created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,',
      'expire_at DATETIME NULL,',
      'is_deleted TINYINT NOT NULL DEFAULT 0,',
      'UNIQUE KEY uk_short_code (short_code),',
      'KEY idx_expire_deleted (expire_at, is_deleted)',
      ') ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci'
    );
    PREPARE stmt FROM @ddl;
    EXECUTE stmt;
    DEALLOCATE PREPARE stmt;
    SET i = i + 1;
  END WHILE;
END//
DELIMITER ;

CALL create_short_url_shards();
DROP PROCEDURE create_short_url_shards;

DELIMITER //
CREATE PROCEDURE migrate_short_urls_to_shards()
BEGIN
  DECLARE i INT DEFAULT 0;
  DECLARE table_name VARCHAR(64);
  WHILE i < 64 DO
    SET table_name = CONCAT('short_urls_', LPAD(i, 2, '0'));
    SET @sql = CONCAT(
      'INSERT IGNORE INTO ', table_name,
      ' (id, short_code, origin_url, created_at, expire_at, is_deleted) ',
      'SELECT id, short_code, origin_url, created_at, expire_at, is_deleted ',
      'FROM short_urls WHERE short_code IS NOT NULL AND MOD(id, 64) = ', i
    );
    PREPARE stmt FROM @sql;
    EXECUTE stmt;
    DEALLOCATE PREPARE stmt;
    SET i = i + 1;
  END WHILE;
END//
DELIMITER ;

CALL migrate_short_urls_to_shards();
DROP PROCEDURE migrate_short_urls_to_shards;

INSERT INTO short_url_id_alloc (name, next_id)
SELECT 'short_url', COALESCE(MAX(id), 0) + 1 FROM short_urls
ON DUPLICATE KEY UPDATE next_id = GREATEST(next_id, VALUES(next_id));
