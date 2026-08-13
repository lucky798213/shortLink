package code

import "fmt"

// charset 是 Base62 编码的字符集。
// 顺序为：数字 0-9 → 小写字母 a-z → 大写字母 A-Z，共 62 个字符。
// 这个顺序保证了编码后的短码按字典序排列时，与自增 ID 的顺序一致。
const charset = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// base 是进制数，即字符集的长度（62）。
const base = uint64(len(charset))

// shortCodeLen 是短码的固定长度。
// 6 位 Base62 可表示 62^6 ≈ 568 亿个不同短码，足够绝大部分业务场景。
const shortCodeLen = 6

// MaxID 是 6 位 Base62 短码能表示的最大 ID。
const MaxID uint64 = 56800235583

var decodeTable = func() [256]int {
	var table [256]int
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < len(charset); i++ {
		table[charset[i]] = i
	}
	return table
}()

// ShortCodeLen 返回固定短码长度。
func ShortCodeLen() int {
	return shortCodeLen
}

// Encode 将 uint64 类型的自增 ID 编码为 6 位 Base62 短码。
//
// 为什么要用自增 ID 而不是随机生成：
// 1. 自增 ID 天然唯一，无需担心碰撞
// 2. Base62 编码是可逆的，能从短码反推出 ID，便于调试
// 3. 相比 UUID/随机串，短码更短且有序
//
// 为什么左补 '0'：
// 保证所有短码长度一致（6 位），便于数据库索引和前端展示。
// 这里的 '0' 是 charset[0]，即字符 '0'。
func Encode(id uint64) string {
	// 边界情况：id=0 理论上不会出现（MySQL 自增主键从 1 开始），
	// 但为了防御性编程，统一返回全 '0' 的 6 位短码。
	if id == 0 {
		buf := make([]byte, shortCodeLen)
		for i := range buf {
			buf[i] = charset[0]
		}
		return string(buf)
	}

	// 从右往左填充字符，类似手算进制转换时的"除基取余"。

	//得到一个长度为 6 的 byte 数组。
	buf := make([]byte, shortCodeLen)
	pos := shortCodeLen - 1 // 从最后一个位置开始填

	for id > 0 && pos >= 0 {
		buf[pos] = charset[id%base] // 取余数，映射到字符集
		id /= base                  // 除基数，继续处理高位
		pos--
	}

	// 高位补 '0'（charset[0]）。
	// 例如 id=1 编码结果为 "1"，但我们需要 6 位，所以前面补 5 个 '0' 变成 "000001"。
	for i := 0; i <= pos; i++ {
		buf[i] = charset[0]
	}

	return string(buf)
}

// Decode 将 6 位 Base62 短码反解为 ID，并完成基础合法性校验。
func Decode(shortCode string) (uint64, error) {
	if len(shortCode) != shortCodeLen {
		return 0, fmt.Errorf("short code length must be %d", shortCodeLen)
	}

	var id uint64
	for i := 0; i < len(shortCode); i++ {
		value := decodeTable[shortCode[i]]
		if value < 0 {
			return 0, fmt.Errorf("short code contains invalid character %q", shortCode[i])
		}
		id = id*base + uint64(value)
	}
	if id > MaxID {
		return 0, fmt.Errorf("short code exceeds capacity")
	}
	return id, nil
}

// IsValid 判断短码是否为固定长度的合法 Base62 编码。
func IsValid(shortCode string) bool {
	_, err := Decode(shortCode)
	return err == nil
}
