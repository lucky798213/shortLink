USE short_url;

ALTER TABLE short_urls
  MODIFY COLUMN short_code VARCHAR(16) NULL;
