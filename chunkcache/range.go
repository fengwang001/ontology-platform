package chunkcache

// ByteRange 表示对象上的字节范围。三种互斥写法：
//   - 起止：start>=0, end>=0（终点含）；起点不小于总长或终点<起点须可区分
//   - 起点至末尾：start>=0, end==-1
//   - 末尾若干字节：suffixN 为字节数；suffixN==0 须可区分
type ByteRange struct {
	start   int64
	end     int64
	suffix  bool
	suffixN int64
}

// RangeStartEnd 构造含首含尾的起止范围。end<start 属参数非法。
func RangeStartEnd(start, end int64) (ByteRange, error) {
	if start < 0 {
		return ByteRange{}, newError(KindInvalidParam, "RangeStartEnd", ReasonNegativeStart, nil)
	}
	if end < start {
		return ByteRange{}, newError(KindInvalidParam, "RangeStartEnd", ReasonEndBeforeStart, nil)
	}
	return ByteRange{start: start, end: end}, nil
}

// RangeFrom 构造“从起点到对象末尾”的范围。
func RangeFrom(start int64) (ByteRange, error) {
	if start < 0 {
		return ByteRange{}, newError(KindInvalidParam, "RangeFrom", ReasonNegativeStart, nil)
	}
	return ByteRange{start: start, end: -1}, nil
}

// RangeSuffix 构造“末尾 n 字节”范围。n==0 为可区分的不可满足范围。
func RangeSuffix(n int64) (ByteRange, error) {
	if n < 0 {
		return ByteRange{}, newError(KindInvalidParam, "RangeSuffix", ReasonNegativeSuffix, nil)
	}
	return ByteRange{suffix: true, suffixN: n}, nil
}

// resolvedRange 为依据对象总长度解析后的具体区间 [start,end)（半开）。
type resolvedRange struct {
	start int64
	end   int64
}

func (b ByteRange) isSuffix() bool { return b.suffix }

// resolve 依据对象总长度 length 把三种写法解析为半开区间 [start,end)。
//
// 三种“不可满足/非法”必须可区分，以不同 Reason 返回：
//   - 终点小于起点：构造时即 KindInvalidParam / end_before_start（本函数不会遇到）
//   - 起点不小于总长度：KindRangeNotSatisfiable / start_at_or_after_length
//   - 后缀长度为零：KindRangeNotSatisfiable / zero_suffix
//
// 终点超出总长度时截断到末尾；长度为零的对象上任何非空范围都不可满足。
func (b ByteRange) resolve(op string, length int64) (resolvedRange, error) {
	if length < 0 {
		return resolvedRange{}, newError(KindInvalidParam, op, "negative_length", nil)
	}
	if b.suffix {
		if b.suffixN == 0 {
			return resolvedRange{}, newError(KindRangeNotSatisfiable, op, ReasonZeroSuffix, nil)
		}
		start := length - b.suffixN
		if start < 0 {
			start = 0
		}
		return resolvedRange{start: start, end: length}, nil
	}
	if b.start >= length {
		return resolvedRange{}, newError(KindRangeNotSatisfiable, op, ReasonStartAtOrAfterSize, nil)
	}
	end := b.end + 1
	if b.end < 0 || end > length {
		// RangeFrom（end==-1）或终点越过总长度：截到末尾。
		end = length
	}
	return resolvedRange{start: b.start, end: end}, nil
}

// chunkSpan 给出区间 [start,end) 覆盖的切片下标集合（含首含尾）。
// 调用方须保证 0<=start<end。切片 i 覆盖字节 [i*S, min((i+1)*S, length))。
func chunkSpan(s, start, end int64) (first, last int) {
	first = int(start / s)
	last = int((end - 1) / s)
	return first, last
}
