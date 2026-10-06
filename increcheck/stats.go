package increcheck

// stats.go：统计视图与判定日志。
// 每次编辑后重检的签名/实现声明数、因早停而免于继续失效的声明数、
// 累计沿用次数都由调度器在实际动作处累加，可与 EditOutcome 逐项核对。

// Stats 是可核对的统计视图（累计值）。
type Stats struct {
	Edits          int64 // 实际生效（非空操作、未被拒绝）的编辑数
	NoOps          int64 // 内容完全相同的空操作编辑数
	Rejected       int64 // 调度进行中被拒绝的编辑数
	RecheckedSigs  int64 // 累计实际重检的签名检查数
	RecheckedImpls int64 // 累计实际重检的实现检查数
	StoppedEarly   int64 // 累计因重检签名相同而早停的成员数
	Reused         int64 // 累计沿用缓存结果的次数（含查询命中）
}

// Logger 记录每条输入、输出与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// NopLogger 丢弃全部日志。
type NopLogger struct{}

func (NopLogger) Logf(string, ...any) {}

func formatSig(r SigResult) string {
	if !r.Present {
		return "absent"
	}
	if r.Err != ErrNone {
		return "present err=" + r.Err.Error() + " text=" + r.Text
	}
	return "present text=" + r.Text
}

func formatBasis(b Basis) string {
	parts := make([]string, 0, len(b))
	for _, dep := range sortedBasis(b) {
		parts = append(parts, dep+":"+itoa(b[dep]))
	}
	return joinComma(parts)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
