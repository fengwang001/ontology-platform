package cue

import "errors"

const (
	MinDMin     = 1
	MaxDMin     = 1_000_000
	MaxTime     = 1_000_000_000
	MaxTextSize = 200
	MaxCues     = 10_000
)

// Cue 是一条半开区间 [Start, End) 的字幕。
type Cue struct {
	Start int64
	End   int64
	Text  string
}

var (
	// ErrBadTime：时间戳越界或 start >= end。
	ErrBadTime = errors.New("cue: illegal timestamp (need 0 <= start < end <= 1e9)")
	// ErrTooShort：时长小于 Dmin（恰等 Dmin 合法）。
	ErrTooShort = errors.New("cue: duration below Dmin")
	// ErrOrder：未按 start 升序或与前一条重叠（相接允许）。
	ErrOrder = errors.New("cue: cues must be sorted and non-overlapping")
	// ErrBadText：text 为空或超过 200 字节。
	ErrBadText = errors.New("cue: text must be non-empty and at most 200 bytes")
	// ErrTooMany：条数超过 10000。
	ErrTooMany = errors.New("cue: more than 10000 cues")
	// ErrDMinRange：构造参数 Dmin 越界（参数非法）。
	ErrDMinRange = errors.New("cue: Dmin out of range [1, 1e6]")
)

// InvalidError 携带首个违规下标与类别（Unwrap 可得到上述哨兵错误）。
type InvalidError struct {
	Index int
	Err   error
}

func (e *InvalidError) Error() string {
	return "cue: invalid cue at index " + itoa(e.Index) + ": " + e.Err.Error()
}
func (e *InvalidError) Unwrap() error { return e.Err }

// Validate 校验一组字幕。返回的错误为 *InvalidError（Dmin 越界时为
// ErrDMinRange，属于参数非法，不下标）。
func Validate(cues []Cue, dmin int64) error {
	if dmin < MinDMin || dmin > MaxDMin {
		return ErrDMinRange
	}
	var prevEnd int64
	for i, c := range cues {
		switch {
		case c.Start < 0 || c.End < 0 || c.Start > MaxTime || c.End > MaxTime || c.Start >= c.End:
			return &InvalidError{Index: i, Err: ErrBadTime}
		case c.End-c.Start < dmin:
			return &InvalidError{Index: i, Err: ErrTooShort}
		case i > 0 && c.Start < prevEnd:
			return &InvalidError{Index: i, Err: ErrOrder}
		}
		if n := len(c.Text); n == 0 || n > MaxTextSize {
			return &InvalidError{Index: i, Err: ErrBadText}
		}
		prevEnd = c.End
	}
	if len(cues) > MaxCues {
		return &InvalidError{Index: MaxCues, Err: ErrTooMany}
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
