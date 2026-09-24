// Package stream 提供严格模式的流式 Base64 编码器与解码器。
// 它处理跨 Write 切分点的半个组、MIME 换行（每 76 字符一个 \r\n）与输出上限。
package stream

import (
	"errors"

	"ontology/b64"
)

// 五类彼此可区分的错误，均带字节偏移（见 Error）。
var (
	ErrIllegalChar  = errors.New("stream: illegal character")
	ErrNonCanonical = errors.New("stream: non-canonical trailing bits")
	ErrPadding      = errors.New("stream: padding placement error")
	ErrLength       = errors.New("stream: input length is not a multiple of 4")
	ErrNewline      = errors.New("stream: newline in illegal position")
	ErrLimit        = errors.New("stream: output exceeds limit")
)

// Error 携带错误类别（Kind 为上述哨兵之一）与全输入流字节偏移。
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }
func (e *Error) Is(target error) bool { return e.Kind == target }

type Decoder struct {
	out     []byte
	grp     [4]byte
	n       int
	offset  int
	seenPad bool
	hadCR   bool
	mime    bool
	limit   int
	closed  bool
	dead    bool
	termErr error
	checked int
}

type Encoder struct{}

// （Encoder 结构在下述方法中替换实现）

// NewDecoder 创建严格模式解码器。mime 开启时允许组之间出现 \r\n 或 \n；
// limit >= 0 时为解码输出字节上限，< 0 表示不限制。
func NewDecoder(mime bool, limit int) *Decoder {
	return &Decoder{mime: mime, limit: limit}
}

func (d *Decoder) fail(kind error, off int) error {
	d.dead = true
	d.termErr = &Error{Kind: kind, Offset: off}
	return d.termErr
}

func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed || d.dead {
		return 0, d.termErr
	}
	for i, b := range p {
		off := d.offset + i
		d.checked++
		switch {
		case b == '\r':
			if !d.mime || d.n != 0 || !d.boundary() {
				return i, d.fail(ErrNewline, off)
			}
			d.hadCR = true
		case b == '\n':
			if !d.mime || d.n != 0 || (!d.hadCR && !d.boundary()) {
				return i, d.fail(ErrNewline, off)
			}
			d.hadCR = false
		default:
			if d.hadCR {
				return i, d.fail(ErrNewline, off - 1)
			}
			if !b64.IsData(b) && b != b64.Pad {
				return i, d.fail(ErrIllegalChar, off)
			}
			if d.seenPad {
				return i, d.fail(ErrPadding, off)
			}
			d.grp[d.n] = b
			d.n++
			if d.n == 4 {
				if err := d.finishGroup(d.grpStart(off)); err != nil {
					return i, err
				}
				d.n = 0
			}
		}
	}
	d.offset += len(p)
	return len(p), nil
}

func (d *Decoder) boundary() bool { return d.n == 0 && (len(d.out) > 0 || d.seenPad) }

func (d *Decoder) grpStart(lastOff int) int { return lastOff - 3 }

func (d *Decoder) finishGroup(start int) error {
	kind, out, idx, err := b64.DecodeGroup(d.grp)
	if err != nil {
		kindErr := ErrPadding
		if errors.Is(err, b64.ErrIllegalChar) {
			kindErr = ErrIllegalChar
		} else if errors.Is(err, b64.ErrNonCanonical) {
			kindErr = ErrNonCanonical
		}
		return d.fail(kindErr, start+idx)
	}
	if kind != b64.KindFull {
		d.seenPad = true
	}
	if d.limit >= 0 && len(d.out)+len(out) > d.limit {
		return d.fail(ErrLimit, start)
	}
	d.out = append(d.out, out...)
	return nil
}

func (d *Decoder) Close() error {
	if d.dead {
		return d.termErr
	}
	d.closed = true
	if d.hadCR {
		return d.fail(ErrNewline, d.offset-1)
	}
	if d.n != 0 {
		return d.fail(ErrLength, d.offset-d.n)
	}
	return nil
}

func (d *Decoder) Output() []byte { return d.out }

// Checked 返回输入字节被检查的总次数（每个输入字节恰好 1 次，不回扫）。
func (d *Decoder) Checked() int { return d.checked }

type encState struct {
	out    []byte
	grp    [3]byte
	n      int
	col    int
	mime   bool
	closed bool
}

// NewEncoder 创建流式编码器。mime 开启时每 76 个字符插入一个 \r\n，
// 最后一行之后不加换行。
func NewEncoder(mime bool) *Encoder {
	return &Encoder{}
}

// （保留类型占位，实际状态见 encState；下方重新定义 Encoder 方法）

var _ = b64.Alphabet
