CREATE DATABASE IF NOT EXISTS short_url
  DEFAULT CHARACTER SET utf8mb4
  DEFAULT COLLATE utf8mb4_unicode_ci;

USE short_url;

CREATE TABLE IF NOT EXISTS short_urls (
  id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  short_code VARCHAR(16)  NULL,
  origin_url TEXT         NOT NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expire_at  DATETIME     NULL,
  is_deleted TINYINT      NOT NULL DEFAULT 0,
  UNIQUE KEY uk_short_code (short_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS short_url_id_alloc (
  name    VARCHAR(64)     NOT NULL PRIMARY KEY,
  next_id BIGINT UNSIGNED NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO short_url_id_alloc (name, next_id)
VALUES ('short_url', 1)
ON DUPLICATE KEY UPDATE next_id = next_id;

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

CREATE TABLE IF NOT EXISTS short_url_visits (
  id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  short_code VARCHAR(16)  NOT NULL,
  ip         VARCHAR(64)  NOT NULL DEFAULT '',
  user_agent VARCHAR(512) NOT NULL DEFAULT '',
  referer    VARCHAR(1024) NOT NULL DEFAULT '',
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  KEY idx_short_code_created_at (short_code, created_at),
  KEY idx_short_code_ip (short_code, ip)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
