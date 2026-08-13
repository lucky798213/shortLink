package shortlink

import "errors"

var (
	ErrInvalidOriginURL = errors.New("origin_url must be a valid http or https URL")
	ErrInvalidExpireAt  = errors.New("expire_at must be in the future")
	ErrNotFound         = errors.New("short url not found")
)
