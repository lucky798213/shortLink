package shortlink

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// NormalizeOriginURL 校验并规范化用户提交的原始链接。
func NormalizeOriginURL(rawURL string) (string, error) {
	originURL := strings.TrimSpace(rawURL)
	if originURL == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidOriginURL)
	}

	parsed, err := url.Parse(originURL)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidOriginURL, err)
	}
	if parsed.Host == "" || strings.ContainsAny(parsed.Host, " \t\r\n") {
		return "", fmt.Errorf("%w: invalid host", ErrInvalidOriginURL)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme", ErrInvalidOriginURL)
	}
	return originURL, nil
}

// ParseExpireAt 将 Unix 秒转换为过期时间，0 表示永不过期。
func ParseExpireAt(expireAtUnix int64, now time.Time) (*time.Time, error) {
	if expireAtUnix == 0 {
		return nil, nil
	}
	if expireAtUnix <= now.Unix() {
		return nil, ErrInvalidExpireAt
	}
	expireAt := time.Unix(expireAtUnix, 0)
	return &expireAt, nil
}
