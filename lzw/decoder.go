package lzw

import (
	"io"
	"sync"
)

// bitReader 按低位在先从字节流中取变长码。
// 维护从底层读出的原始字节缓冲，consumed 为取码已消费的总位数；
// 需要判定“当前码之后是否还有码”时可一次性读尽底层数据。
type bitReader struct {
	r        io.Reader
	buf      []byte
	consumed int
	eof      bool
}

func newBitReader(r io.Reader) *bitReader {
	return &bitReader{r: r, buf: make([]byte, 0, 256)}
}

func (b *bitReader) compact() {
	full := b.consumed / 8
	if full == 0 {
		return
	}
	b.buf = append(b.buf[:0], b.buf[full:]...)
	b.consumed -= full * 8
}

func (b *bitReader) pull() error {
	b.compact()
	if b.consumed < len(b.buf)*8 {
		return nil
	}
	tmp := make([]byte, 256)
	n, err := b.r.Read(tmp)
	b.buf = append(b.buf, tmp[:n]...)
	if n == 0 && err == nil {
		err = io.EOF // 防御不合规 Reader 的 (0,nil)，避免忙等
	}
	if err != nil {
		b.eof = true
	}
	return err
}

func (b *bitReader) drain() error {
	for {
		if b.eof {
			b.compact()
			return nil
		}
		b.compact()
		tmp := make([]byte, 4096)
		n, err := b.r.Read(tmp)
		b.buf = append(b.buf, tmp[:n]...)
		if n == 0 && err == nil {
			err = io.EOF
		}
		if err != nil {
			b.eof = true
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// readCode 读取 bits 位码；返回 ok=false 表示剩余位不足 bits
// （不足时仍会把可用位全部消费，使流位置前进到字节末尾）。
func (b *bitReader) readCode(bits uint) (int, bool) {
	available := true
	for b.consumed+int(bits) > len(b.buf)*8 {
		if b.eof {
			available = false
			break
		}
		_ = b.pull() // EOF 已记录在 b.eof，循环条件下一轮判定
	}
	if !available {
		// 消费剩余的全部残位（随后 remainingBits 为 0，不会重复报错）。
		availBits := len(b.buf)*8 - b.consumed
		if availBits < 0 {
			availBits = 0
		}
		b.consumed += availBits
		return 0, false
	}
	var code uint32
	for i := uint(0); i < bits; i++ {
		idx := b.consumed / 8
		bit := uint(b.buf[idx]>>(uint(b.consumed)%8)) & 1
		code |= uint32(bit) << i
		b.consumed++
	}
	return int(code), true
}

// remainingBits 读尽底层数据后返回剩余总位数。
func (b *bitReader) remainingBits() (int, error) {
	if err := b.drain(); err != nil {
		return 0, err
	}
	return len(b.buf)*8 - b.consumed, nil
}

// tailInfo 描述结束码之后的内容。
type tailInfo struct {
	partial byte   // 结束码所在字节
	padBits uint   // 该字节内未消费位数（高位），0 表示恰在字节边界
	extra   []byte // 其后的完整字节
}

func (b *bitReader) tail() (tailInfo, error) {
	if err := b.drain(); err != nil {
		return tailInfo{}, err
	}
	var info tailInfo
	idx := b.consumed / 8
	off := uint(b.consumed % 8)
	if off > 0 && idx < len(b.buf) {
		info.partial = b.buf[idx]
		info.padBits = 8 - off
		idx++
	}
	info.extra = b.buf[idx:]
	return info, nil
}

// Decoder 是 GIF 变宽 LZW 流式解码器。
// 所有方法可并发调用；解码出错后进入粘滞失败态，后续调用返回同一
// *CodeError 且不再改变任何状态。
type Decoder struct {
	mu          sync.Mutex
	br          *bitReader
	tab         []dictEntry
	free        int  // 下一个待新增编号
	expectClear bool // 4095 已建后，下一码必须是清除码
	prevCode    int  // 上一数据码编号；-1 表示清除后尚未收到数据码
	firstAfter  bool // 上一码是否为清除码（其后首码不建条目）
	out         []byte
	done        bool
	sticky      error
	index       int // 已处理码序号（从 1 起，含清除码）
}

// NewDecoder 创建从 r 读取的解码器。
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{
		br:         newBitReader(r),
		tab:        make([]dictEntry, firstFree, 4096),
		free:       firstFree,
		prevCode:   -1,
		firstAfter: true,
	}
}

func (d *Decoder) fail(err error) error {
	if d.sticky == nil {
		d.sticky = &CodeError{Err: err, Index: d.index}
	}
	return d.sticky
}

// reset 按清除码复位；码宽随 free=258 自然回到 9。
func (d *Decoder) reset() {
	d.tab = d.tab[:firstFree]
	d.free = firstFree
	d.expectClear = false
	d.prevCode = -1
	d.firstAfter = true
}

// build 重建编号 code 的字节串并追加到 dst。
func (d *Decoder) build(dst []byte, code int) []byte {
	start := len(dst)
	for code >= 256 {
		e := d.tab[code]
		dst = append(dst, e.b)
		code = e.prefix
	}
	dst = append(dst, byte(code))
	for i, j := start, len(dst)-1; i < j; i, j = i+1, j-1 {
		dst[i], dst[j] = dst[j], dst[i]
	}
	return dst
}

func (d *Decoder) firstByte(code int) byte {
	for code >= 256 {
		code = d.tab[code].prefix
	}
	return byte(code)
}

// Read 实现 io.Reader。
func (d *Decoder) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sticky != nil {
		return 0, d.sticky
	}
	for len(d.out) == 0 && !d.done && d.sticky == nil {
		d.pump()
	}
	if len(d.out) > 0 {
		n := copy(p, d.out)
		d.out = d.out[n:]
		if n > 0 {
			return n, nil
		}
	}
	if d.sticky != nil {
		return 0, d.sticky
	}
	if d.done {
		return 0, io.EOF
	}
	// pump 未产出却未标记结束/失败属于内部不变量被破坏；防御性返回截断错误。
	d.index++
	return 0, d.fail(ErrTruncated)
}

// pump 处理码，直到产出字节、遇到结束码或失败。
func (d *Decoder) pump() {
	d.readAndProcess()
}

// readAndProcess 按当前状态推导码宽，读一个码并处理。
func (d *Decoder) readAndProcess() {
	// 码宽推导（严格由编码规则推出，无额外约定）：
	//
	// 编码端每处理一个数据码 D（不是清除后首码），都同时占用一个新
	// 条目编号：该码是“w+c 不匹配”时写出的，条目 e=w+c 在写码的同
	// 一步被记录；Close 时若最后一个数据码之后没有下一码，还会再把
	// “下一个空闲编号”无条件计为已占用（无内容、不被引用），然后才
	// 输出结束码。因此解码端读取“清除后首码之外的任何码”时，编号
	// free 必已在编码端被占用，输出该码所用的码宽是 widthFor(free+1)
	// ——即使读到的是结束码（对应 Close 的补计）或表满后的清除码。
	// 读到数据码时，条目内容随后回填到编号 free；读到结束/清除码时
	// 该编号没有内容，与编码端完全一致。
	width := widthFor(d.free)
	if d.prevCode >= 0 {
		width = widthFor(d.free + 1)
	}
	rem, err := d.br.remainingBits()
	if err != nil {
		d.fail(err)
		return
	}
	if rem < int(width) {
		d.index++
		d.fail(ErrTruncated)
		return
	}
	code, ok := d.br.readCode(width)
	if !ok {
		d.index++
		d.fail(ErrTruncated)
		return
	}
	d.index++
	if d.index == 1 && code != ClearCode {
		d.fail(ErrNotCleared)
		return
	}

	if code == ClearCode {
		if d.expectClear {
			d.reset()
			return
		}
		d.reset()
		return
	}
	if d.expectClear {
		// 表满后编码端必发清除码；其它码按越界拒绝。
		d.fail(ErrCodeOutOfRange)
		return
	}
	if code == EndCode {
		d.done = true
		d.checkTail()
		return
	}

	if d.firstAfter {
		// 清除码之后首码必须是 0..255 字面码；该码不建条目。
		if code >= 256 {
			d.fail(ErrCodeAfterClear)
			return
		}
		d.out = append(d.out, byte(code))
		d.prevCode = code
		d.firstAfter = false
		return
	}

	// 常规数据码：合法性上界即 d.free（编码端本步将新增的编号）；
	// 等于它为自引用 KwKwK。
	if code > d.free {
		d.fail(ErrCodeOutOfRange)
		return
	}
	addedByte := d.firstByte(d.prevCode)
	if code == d.free {
		// 自引用 KwKwK：输出串 = 旧串 + 旧串首字节。
		d.out = d.build(d.out, d.prevCode)
		d.out = append(d.out, addedByte)
	} else {
		d.out = d.build(d.out, code)
		addedByte = d.firstByte(code)
	}
	newID := d.free
	if newID <= maxCode {
		d.tab = append(d.tab, dictEntry{prefix: d.prevCode, b: addedByte})
		d.free = newID + 1
		if newID == maxCode {
			d.expectClear = true
		}
	}
	d.prevCode = code
}

// checkTail 结束码之后：补齐位必须为 0，且不得有多余字节。
func (d *Decoder) checkTail() {
	info, err := d.br.tail()
	if err != nil {
		d.fail(err)
		return
	}
	if info.padBits > 0 && info.partial>>(8-info.padBits) != 0 {
		d.fail(ErrPaddingNonZero)
		return
	}
	if len(info.extra) > 0 {
		d.fail(ErrTrailingData)
		return
	}
}

// Err 返回粘滞失败原因（nil 表示尚未失败）。
func (d *Decoder) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sticky
}
