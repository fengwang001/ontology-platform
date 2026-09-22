package source

import "io"

// Flaky 包装一个字节源，用于注入三类故障：
// 短读（一次只返回部分字节且不报错）、读错误、以及组装过程中长度变小。
// 它本身不依赖其他包，供组装器与测试共同使用。
type Flaky struct {
	base Source

	// MaxChunk 限制每次 ReadAt 最多返回的字节数；<=0 表示不限制。
	MaxChunk int
	// ShortCalls 中的调用序号（从 0 起，每次 ReadAt 调用 +1）强制短读。
	ShortCalls map[int]bool
	// FailCall 是注入错误的调用序号；FailErr 为 nil 时注入 io.ErrUnexpectedEOF。
	FailCall int
	FailErr  error
	// NewLength >= 0 时，第 ShrinkCall 次调用起 Length 返回该值，
	// 并令越界读取返回 io.EOF，模拟"读到一半长度改变"。
	NewLength  int64
	ShrinkCall int

	calls int
}

// NewFlaky 创建包装器，默认不注入任何故障。
func NewFlaky(base Source) *Flaky {
	return &Flaky{base: base, FailCall: -1, NewLength: -1, ShrinkCall: -1}
}

// Calls 返回截至目前 ReadAt 被调用的次数。
func (f *Flaky) Calls() int { return f.calls }

// Length 反映可能被改写过的当前长度。
func (f *Flaky) Length() int64 {
	if f.NewLength >= 0 && f.calls > f.ShrinkCall {
		return f.NewLength
	}
	return f.base.Length()
}

// ReadAt 按注入策略决定本次调用返回多少字节、是否报错。
func (f *Flaky) ReadAt(p []byte, off int64) (int, error) {
	seq := f.calls
	f.calls++

	if seq == f.FailCall {
		if f.FailErr != nil {
			return 0, f.FailErr
		}
		return 0, io.ErrUnexpectedEOF
	}

	length := f.base.Length()
	if f.NewLength >= 0 && seq > f.ShrinkCall {
		length = f.NewLength
	}
	if off >= length {
		return 0, io.EOF
	}

	want := len(p)
	if f.MaxChunk > 0 && want > f.MaxChunk {
		want = f.MaxChunk
	}
	if f.ShortCalls[seq] && want > 1 {
		want = 1 // 强制只返回 1 字节且不报错
	}
	if avail := int(length - off); want > avail {
		want = avail
	}
	// off 是 ReadAt 语义下的绝对偏移；直接透传，底层负责从该位置拷贝。
	n, err := f.base.ReadAt(p[:want], off)
	if err == io.EOF && n > 0 && n < len(p) {
		// 底层用 EOF 表示到末尾；短读但无致命错误时保持 nil，
		// 由组装器在后续补齐调用中自行发现真正的不足。
		err = nil
	}
	return n, err
}
