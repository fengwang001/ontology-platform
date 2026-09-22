package coalesce

// Interval 是闭区间 [Start, End]，单位是字节偏移，Start <= End。
type Interval struct {
	Start int64
	End   int64
}

// Length 返回区间字节数。
func (iv Interval) Length() int64 { return iv.End - iv.Start + 1 }

// UnsatisfiableError 表示区间集合在给定资源长度下无法满足
// （例如起点越过末尾，或 bytes=-0）。它与 *rangespec.SyntaxError
// 是两类彼此可判定的错误，并携带资源总长以便生成 416 响应。
type UnsatisfiableError struct {
	TotalLength int64
	Reason      string
}

func (e *UnsatisfiableError) Error() string {
	return "coalesce: unsatisfiable range for resource length " + itoa(e.TotalLength) + ": " + e.Reason
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
