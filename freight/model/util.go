package model

import "fmt"

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// CeilDiv 返回 ceil(a/b)，要求 b > 0，a >= 0。
func CeilDiv(a, b int64) (int64, bool) {
	if b <= 0 || a < 0 {
		return 0, false
	}
	return (a + b - 1) / b, true
}
