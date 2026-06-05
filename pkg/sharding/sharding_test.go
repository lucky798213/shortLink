package sharding

import "testing"

func TestStrategyTableByID(t *testing.T) {
	strategy := NewStrategy(64)
	tests := []struct {
		id   uint64
		want string
	}{
		{id: 0, want: "short_urls_00"},
		{id: 1, want: "short_urls_01"},
		{id: 63, want: "short_urls_63"},
		{id: 64, want: "short_urls_00"},
	}

	for _, tt := range tests {
		got := strategy.TableByID(tt.id)
		if got != tt.want {
			t.Fatalf("TableByID(%d) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestStrategyTableByShortCode(t *testing.T) {
	strategy := NewStrategy(64)
	table, id, err := strategy.TableByShortCode("000010")
	if err != nil {
		t.Fatalf("TableByShortCode() error: %v", err)
	}
	if id != 62 || table != "short_urls_62" {
		t.Fatalf("got table=%q id=%d, want short_urls_62 id=62", table, id)
	}
}

func TestStrategyRejectsInvalidShortCode(t *testing.T) {
	strategy := NewStrategy(64)
	if _, _, err := strategy.TableByShortCode("bad"); err == nil {
		t.Fatal("TableByShortCode() expected error")
	}
}
