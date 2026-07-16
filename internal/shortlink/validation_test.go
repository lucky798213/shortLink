package shortlink

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeOriginURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{name: "http", rawURL: "http://example.com/path", want: "http://example.com/path"},
		{name: "trim whitespace", rawURL: "  https://example.com  ", want: "https://example.com"},
		{name: "empty", rawURL: "", wantErr: true},
		{name: "missing host", rawURL: "https:///path", wantErr: true},
		{name: "unsupported scheme", rawURL: "ftp://example.com", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeOriginURL(tt.rawURL)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidOriginURL) {
					t.Fatalf("NormalizeOriginURL() error = %v, want ErrInvalidOriginURL", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizeOriginURL() = %q, %v, want %q, nil", got, err, tt.want)
			}
		})
	}
}

func TestParseExpireAt(t *testing.T) {
	now := time.Unix(100, 0)
	if got, err := ParseExpireAt(0, now); err != nil || got != nil {
		t.Fatalf("ParseExpireAt(0) = %v, %v, want nil, nil", got, err)
	}
	if _, err := ParseExpireAt(100, now); !errors.Is(err, ErrInvalidExpireAt) {
		t.Fatalf("ParseExpireAt(past) error = %v, want ErrInvalidExpireAt", err)
	}
	got, err := ParseExpireAt(200, now)
	if err != nil || got == nil || got.Unix() != 200 {
		t.Fatalf("ParseExpireAt(future) = %v, %v, want unix 200", got, err)
	}
}
