USE short_url;

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
