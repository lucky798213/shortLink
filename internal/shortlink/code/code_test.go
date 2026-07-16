package code

import "testing"

// TestEncode 测试 Base62 编码的正确性。
// 覆盖场景：最小值(0)、普通值(1,10,61)、进位边界(62)、
// 全小写 z 最大值(32590299105)、6 位最大值(62^6-1=56800235583)。
func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		id   uint64
		want string
	}{
		{
			name: "ID=1 → 000001（最小正常ID，补5个0）",
			id:   1,
			want: "000001",
		},
		{
			name: "ID=0 → 000000（边界情况，防御性返回全0）",
			id:   0,
			want: "000000",
		},
		{
			name: "ID=10 → 00000a（验证小写字母映射：a=10）",
			id:   10,
			want: "00000a",
		},
		{
			name: "ID=61 → 00000Z（验证大写字母映射：Z=61，即最后一位大写）",
			id:   61,
			want: "00000Z",
		},
		{
			name: "ID=62 → 000010（验证进位：62=1×62+0，即'10'）",
			id:   62,
			want: "000010",
		},
		{
			name: "ID=32590299105 → zzzzzz（全小写z的最大值，z在字符集中排第35位）",
			id:   32590299105,
			want: "zzzzzz",
		},
		{
			name: "ID=56800235583 → ZZZZZZ（6位Base62最大值62^6-1，即全大写Z）",
			id:   56800235583,
			want: "ZZZZZZ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Encode(tt.id)
			if got != tt.want {
				t.Errorf("Encode(%d) = %q, 期望 %q", tt.id, got, tt.want)
			}
			// 确保所有输出固定为 6 位
			if len(got) != shortCodeLen {
				t.Errorf("Encode(%d) 输出长度 = %d, 期望长度 = %d", tt.id, len(got), shortCodeLen)
			}
		})
	}
}

// TestEncodeLength 验证各种 ID 编码后长度始终为 6 位。
// 包含一个超过 6 位表示范围的超大 ID(3521614606208)，
// 它已超过 62^6-1(≈568亿)，会因溢出得到 "000000"，
// 实际业务中自增 ID 几乎不可能达到这个数量级。
func TestEncodeLength(t *testing.T) {
	for _, id := range []uint64{0, 1, 62, 100, 1000, 1000000, 3521614606208} {
		got := Encode(id)
		if len(got) != shortCodeLen {
			t.Errorf("Encode(%d) = %q, 长度=%d, 期望长度=%d", id, got, len(got), shortCodeLen)
		}
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		code string
		want uint64
	}{
		{code: "000000", want: 0},
		{code: "000001", want: 1},
		{code: "00000a", want: 10},
		{code: "00000Z", want: 61},
		{code: "000010", want: 62},
		{code: "ZZZZZZ", want: MaxID},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got, err := Decode(tt.code)
			if err != nil {
				t.Fatalf("Decode(%q) error: %v", tt.code, err)
			}
			if got != tt.want {
				t.Fatalf("Decode(%q) = %d, want %d", tt.code, got, tt.want)
			}
			if Encode(got) != tt.code {
				t.Fatalf("Encode(Decode(%q)) = %q", tt.code, Encode(got))
			}
		})
	}
}

func TestDecodeRejectsInvalidCode(t *testing.T) {
	for _, code := range []string{"", "1", "0000010", "0000_1", "0000-1", "０００００１"} {
		if _, err := Decode(code); err == nil {
			t.Fatalf("Decode(%q) expected error", code)
		}
	}
}
