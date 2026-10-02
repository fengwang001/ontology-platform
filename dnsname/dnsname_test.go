package dnsname

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// naiveWrite 是不压缩的朴素写法：逐标签写长度+内容，最后写 0。
func naiveWrite(msg []byte, labels []string) []byte {
	for _, l := range labels {
		msg = append(msg, byte(len(l)))
		msg = append(msg, l...)
	}
	return append(msg, 0)
}

func joinName(labels []string) string {
	if len(labels) == 0 {
		return "."
	}
	return strings.Join(labels, ".")
}

func ptrTarget(msg []byte, off int) int {
	return int(msg[off]&0x3F)<<8 | int(msg[off+1])
}

// 整名命中：第二次写同一名字只写 2 字节指针。
func TestWholeNameHit(t *testing.T) {
	e := NewEncoder()
	name := []string{"www", "example", "com"}
	off1, err := e.Write(name)
	if err != nil {
		t.Fatalf("第一次写入失败: %v", err)
	}
	off2, err := e.Write(name)
	if err != nil {
		t.Fatalf("第二次写入失败: %v", err)
	}
	msg := e.Bytes()
	got := msg[off2:]
	t.Logf("输入: 连续两次写入 %q", joinName(name))
	t.Logf("输出: 第一次偏移=%d 线格式=% x, 第二次偏移=%d 线格式=% x", off1, msg[off1:off2], off2, got)
	t.Logf("判定依据: 整名命中时第二次写入应仅为指向 %d 的 2 字节指针", off1)
	if len(got) != 2 || got[0]&0xC0 != 0xC0 || ptrTarget(msg, off2) != off1 {
		t.Fatalf("整名命中应为指向 %d 的 2 字节指针, 实际 % x", off1, got)
	}
	labels, n, err := Decode(msg, off2, DefaultBase)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	t.Logf("解码: labels=%v 消费字节=%d", labels, n)
	if !reflect.DeepEqual(labels, name) || n != 2 {
		t.Fatalf("解码应得 %v 且消费 2 字节, 实际 %v / %d", name, labels, n)
	}
}

// 后缀命中：只写未前缀标签，再接指向已登记后缀的指针。
func TestSuffixHit(t *testing.T) {
	e := NewEncoder()
	first := []string{"www", "example", "com"}
	second := []string{"mail", "example", "com"}
	off1, err := e.Write(first)
	if err != nil {
		t.Fatalf("写入 %q 失败: %v", joinName(first), err)
	}
	off2, err := e.Write(second)
	if err != nil {
		t.Fatalf("写入 %q 失败: %v", joinName(second), err)
	}
	msg := e.Bytes()
	got := msg[off2:]
	// "example.com" 在第一次写入中的起点：off1 + 1+len("www") = off1+4
	suffixOff := off1 + 4
	t.Logf("输入: 先写 %q 再写 %q", joinName(first), joinName(second))
	t.Logf("输出: 第二次线格式=% x (偏移 %d)", got, off2)
	t.Logf("判定依据: 最长命中后缀 \"example.com\" 登记于 %d, 应写 mail 标签 + 指向 %d 的指针", suffixOff, suffixOff)
	want := []byte{4, 'm', 'a', 'i', 'l'}
	if !bytes.Equal(got[:len(want)], want) || len(got) != len(want)+2 ||
		got[len(want)]&0xC0 != 0xC0 || ptrTarget(msg, off2+len(want)) != suffixOff {
		t.Fatalf("应为 mail 标签 + 指向 %d 的指针, 实际 % x", suffixOff, got)
	}
	labels, n, err := Decode(msg, off2, DefaultBase)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	t.Logf("解码: labels=%v 消费字节=%d", labels, n)
	if !reflect.DeepEqual(labels, second) || n != len(want)+2 {
		t.Fatalf("解码应得 %v, 实际 %v", second, labels)
	}
}

// 根名字：只写一个 0，既不压缩也不登记。
func TestRootName(t *testing.T) {
	e := NewEncoder()
	off1, err := e.Write(nil)
	if err != nil {
		t.Fatalf("根名字写入失败: %v", err)
	}
	off2, err := e.Write([]string{})
	if err != nil {
		t.Fatalf("根名字第二次写入失败: %v", err)
	}
	msg := e.Bytes()
	t.Logf("输入: 连续两次写入根名字 []")
	t.Logf("输出: 第一次偏移=%d 字节=% x, 第二次偏移=%d 字节=% x", off1, msg[off1:off1+1], off2, msg[off2:off2+1])
	t.Logf("判定依据: 根名字只写 0 字节, 不压缩不登记, 两次都应是 1 字节 0x00")
	if msg[off1] != 0 || msg[off2] != 0 || off2 != off1+1 {
		t.Fatalf("根名字应各写 1 个 0 字节, 实际 off1=%d off2=%d msg=% x", off1, off2, msg)
	}
	labels, n, err := Decode(msg, off2, DefaultBase)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	t.Logf("解码: labels=%v 消费字节=%d", labels, n)
	if len(labels) != 0 || n != 1 {
		t.Fatalf("根名字解码应为空标签序列且消费 1 字节, 实际 %v / %d", labels, n)
	}
}

// 大小写保真：命中的后缀解码得到首次写入时的大小写。
func TestCaseFidelity(t *testing.T) {
	e := NewEncoder()
	upper := []string{"WWW", "Example", "COM"}
	lower := []string{"www", "example", "com"}
	off1, err := e.Write(upper)
	if err != nil {
		t.Fatalf("写入 %q 失败: %v", joinName(upper), err)
	}
	off2, err := e.Write(lower)
	if err != nil {
		t.Fatalf("写入 %q 失败: %v", joinName(lower), err)
	}
	msg := e.Bytes()
	labels, _, err := Decode(msg, off2, DefaultBase)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	t.Logf("输入: 先写 %q 再写 %q (ASCII 不分大小写视为同一名字)", joinName(upper), joinName(lower))
	t.Logf("输出: 第二次写入为 % x (偏移 %d), 解码得 %v", msg[off2:], off2, labels)
	t.Logf("判定依据: 命中后缀解码应保真为首次写入的大小写 %v", upper)
	if !reflect.DeepEqual(labels, upper) {
		t.Fatalf("应保真为首次写入的大小写 %v, 实际 %v", upper, labels)
	}
	if off2-off1 != len(msg[off1:off2]) || len(msg[off2:]) != 2 {
		t.Fatalf("第二次写入应为 2 字节指针, 实际 % x", msg[off2:])
	}
}

// 起点偏移恰为 16383 时登记，16384 时不登记。
func TestRegisterBoundary16383(t *testing.T) {
	name := []string{"a", "b"}

	e1 := NewEncoder(MaxPointerOffset) // 16383
	off1, err := e1.Write(name)
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	off2, err := e1.Write(name)
	if err != nil {
		t.Fatalf("第二次写入失败: %v", err)
	}
	msg1 := e1.Bytes()
	t.Logf("输入: base=16383, 连续两次写入 %q", joinName(name))
	t.Logf("输出: 第一次偏移=%d, 第二次偏移=%d 线格式=% x", off1, off2, msg1[off2:])
	t.Logf("判定依据: 起点偏移 %d <= 16383 应登记, 第二次应为 2 字节指针", off1)
	if off1 != MaxPointerOffset {
		t.Fatalf("第一次写入起点应为 16383, 实际 %d", off1)
	}
	if got := msg1[off2:]; len(got) != 2 || got[0]&0xC0 != 0xC0 || ptrTarget(msg1, off2) != off1 {
		t.Fatalf("起点 16383 应已登记, 第二次应为指针, 实际 % x", got)
	}

	e2 := NewEncoder(MaxPointerOffset + 1) // 16384
	off3, err := e2.Write(name)
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	off4, err := e2.Write(name)
	if err != nil {
		t.Fatalf("第二次写入失败: %v", err)
	}
	msg2 := e2.Bytes()
	t.Logf("输入: base=16384, 连续两次写入 %q", joinName(name))
	t.Logf("输出: 第一次偏移=%d, 第二次偏移=%d 线格式=% x", off3, off4, msg2[off4:])
	t.Logf("判定依据: 起点偏移 %d > 16383 不登记, 第二次应仍为完整写法", off3)
	if off3 != MaxPointerOffset+1 {
		t.Fatalf("第一次写入起点应为 16384, 实际 %d", off3)
	}
	if got := msg2[off4:]; !bytes.Equal(got, []byte{1, 'a', 1, 'b', 0}) {
		t.Fatalf("起点 16384 不应登记, 第二次应为完整写法, 实际 % x", got)
	}
}

// 指针链：指针指向另一个指针是合法的，每跳都满足方向规则。
func TestPointerChain(t *testing.T) {
	// 布局(base=0):
	//  0: 3 "com" 0            -> com
	//  5: 7 "example" ptr->0   -> example.com (指针位于 13)
	// 15: 3 "www" ptr->5       -> www.example.com (指针位于 19)
	msg := []byte{
		3, 'c', 'o', 'm', 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0xC0, 0,
		3, 'w', 'w', 'w', 0xC0, 5,
	}
	labels, n, err := Decode(msg, 15, 0)
	if err != nil {
		t.Fatalf("指针链解码失败: %v", err)
	}
	t.Logf("输入: 偏移 15 处名字, 指针链 15->5->0")
	t.Logf("输出: labels=%v 消费字节=%d", labels, n)
	t.Logf("判定依据: 指针链每跳目标都严格小于自身偏移, 应展开为 www.example.com, 消费 6 字节")
	if !reflect.DeepEqual(labels, []string{"www", "example", "com"}) || n != 6 {
		t.Fatalf("应解码为 www.example.com 且消费 6 字节, 实际 %v / %d", labels, n)
	}
}

// 非法指针与畸形标签：按读取顺序只报第一个错误，errors.Is 可区分。
func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		msg  []byte
		off  int
		base int
		want error
	}{
		{"高两位01", []byte{0x40, 'a', 0}, 0, 0, ErrReservedBits},
		{"高两位10", []byte{0x80, 'a', 0}, 0, 0, ErrReservedBits},
		{"标签被截断", []byte{3, 'a'}, 0, 0, ErrTruncated},
		{"指针被截断", []byte{0xC0}, 0, 0, ErrTruncated},
		{"起始偏移越界", []byte{0}, 5, 0, ErrTruncated},
		{"自指指针", []byte{0xC0, 0x00}, 0, 0, ErrPointerNotBackward},
		{"前向指针", []byte{0xC0, 0x05, 0, 0, 0, 0}, 0, 0, ErrPointerNotBackward},
		{"指针目标小于base", []byte{0xC0, 0x05}, 0, 12, ErrPointerBeforeBase},
		{"链中第二跳自指", []byte{1, 'a', 0xC0, 0x05, 0, 0xC0, 0x05}, 0, 0, ErrPointerNotBackward},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode(tc.msg, tc.off, tc.base)
			t.Logf("输入: msg=% x off=%d base=%d", tc.msg, tc.off, tc.base)
			t.Logf("输出: err=%v", err)
			t.Logf("判定依据: 按读取顺序应报 %v", tc.want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("应报 %v, 实际 %v", tc.want, err)
			}
		})
	}
}

// 编码器拒绝非法写入，且被拒绝的写入不追加字节也不登记后缀。
func TestEncoderReject(t *testing.T) {
	e := NewEncoder()
	off, err := e.Write([]string{"example", "com"})
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	before := e.Len()
	beforeBytes := e.Bytes()

	bad := [][]string{
		{"ok", ""},                           // 空标签
		{strings.Repeat("x", MaxLabelLen+1)}, // 超长标签
		{strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 62)}, // 线格式 256
	}
	for _, name := range bad {
		if _, err := e.Write(name); err == nil {
			t.Fatalf("写入 %v 应被拒绝", name)
		} else {
			t.Logf("输入: %d 个标签的名字, 输出: err=%v", len(name), err)
		}
	}
	t.Logf("判定依据: 被拒绝的写入不得追加任何字节")
	if e.Len() != before || !bytes.Equal(e.Bytes(), beforeBytes) {
		t.Fatalf("被拒绝的写入不应改变报文, 之前长度 %d 之后 %d", before, e.Len())
	}

	// 未被登记的 "b.example.com" 不应因失败写入而意外可压缩。
	off2, err := e.Write([]string{"b", "example", "com"})
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	msg := e.Bytes()
	t.Logf("输入: 写入 \"b.example.com\", 输出: 线格式=% x", msg[off2:])
	t.Logf("判定依据: 仅 \"example.com\"(偏移 %d) 已登记, 应写 b 标签 + 指针", off)
	if msg[off2] != 1 || msg[off2+1] != 'b' || msg[off2+2]&0xC0 != 0xC0 || ptrTarget(msg, off2+2) != off {
		t.Fatalf("应为 b 标签 + 指向 %d 的指针, 实际 % x", off, msg[off2:])
	}
}

// 报文总长超过 65535 的写入被拒绝。
func TestMessageTooLong(t *testing.T) {
	e := NewEncoder(MaxMessageLen - 1) // 65534
	if _, err := e.Write(nil); err != nil {
		t.Fatalf("根名字写 1 字节到 65535 应成功, 实际 %v", err)
	}
	if _, err := e.Write(nil); !errors.Is(err, ErrMessageTooLong) {
		t.Fatalf("再写 1 字节应报 ErrMessageTooLong, 实际 %v", err)
	}
	t.Logf("输入: base=65534, 写两个根名字")
	t.Logf("输出: 第一次成功(总长 65535), 第二次 err=%v", ErrMessageTooLong)
	t.Logf("判定依据: 总长超过 65535 的写入被拒绝, 当前长度 %d", e.Len())
	if e.Len() != MaxMessageLen {
		t.Fatalf("报文总长应为 65535, 实际 %d", e.Len())
	}
}

// 总长恰为 255 的名字可写可读；256 被拒绝。
func TestNameLengthBoundary(t *testing.T) {
	ok := []string{strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 61)}
	tooLong := []string{strings.Repeat("a", 63), strings.Repeat("b", 63), strings.Repeat("c", 63), strings.Repeat("d", 62)}

	e := NewEncoder()
	off, err := e.Write(ok)
	if err != nil {
		t.Fatalf("线格式 255 的名字应写入成功, 实际 %v", err)
	}
	msg := e.Bytes()
	t.Logf("输入: 4 标签名字, 线格式总长 %d", e.Len()-off)
	t.Logf("输出: 写入成功, 起点偏移 %d", off)
	t.Logf("判定依据: 含结尾线格式总长恰为 255 不超过上限")
	if e.Len()-off != MaxNameLen {
		t.Fatalf("线格式总长应为 255, 实际 %d", e.Len()-off)
	}
	labels, n, err := Decode(msg, off, DefaultBase)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if !reflect.DeepEqual(labels, ok) || n != MaxNameLen {
		t.Fatalf("解码应还原 255 字节名字, 实际消费 %d", n)
	}

	if _, err := e.Write(tooLong); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("线格式 256 的名字应报 ErrNameTooLong, 实际 %v", err)
	}
	t.Logf("输入: 线格式 256 的名字, 输出: err=%v (编码器拒绝)", ErrNameTooLong)

	// 解码侧：展开后总长 256 应报 ErrNameTooLong。
	wire := naiveWrite(nil, tooLong)
	if _, _, err := Decode(wire, 0, 0); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("解码展开 256 应报 ErrNameTooLong, 实际 %v", err)
	}
	t.Logf("输入: 朴素写法的 256 字节名字, 输出: 解码 err=%v", ErrNameTooLong)
}

// 与不压缩的朴素写法对照：同一名字序列，两种报文解码结果等价。
func TestCompareWithNaive(t *testing.T) {
	names := [][]string{
		{"www", "example", "com"},
		{"mail", "example", "com"},
		{"example", "com"},
		{"WWW", "EXAMPLE", "COM"},
		nil,
		{"a", "b", "c"},
		{"x", "b", "c"},
		{"com"},
	}
	e := NewEncoder()
	naive := make([]byte, DefaultBase)
	offs := make([]int, 0, len(names))
	naiveOffs := make([]int, 0, len(names))
	for _, name := range names {
		off, err := e.Write(name)
		if err != nil {
			t.Fatalf("写入 %q 失败: %v", joinName(name), err)
		}
		offs = append(offs, off)
		naiveOffs = append(naiveOffs, len(naive))
		naive = naiveWrite(naive, name)
	}
	msg := e.Bytes()
	t.Logf("输入: %d 个名字的写入序列", len(names))
	t.Logf("输出: 压缩报文 %d 字节, 朴素报文 %d 字节", len(msg), len(naive))
	for i, name := range names {
		got, _, err := Decode(msg, offs[i], DefaultBase)
		if err != nil {
			t.Fatalf("压缩报文解码 %q 失败: %v", joinName(name), err)
		}
		want, _, err := Decode(naive, naiveOffs[i], DefaultBase)
		if err != nil {
			t.Fatalf("朴素报文解码 %q 失败: %v", joinName(name), err)
		}
		// 大小写保真约定：命中后得到首次写入的大小写，故与朴素解码按 ASCII 不分大小写比较。
		if !equalFoldLabels(got, want) {
			t.Fatalf("名字 %q 两种报文解码应等价, 压缩=%v 朴素=%v", joinName(name), got, want)
		}
		t.Logf("判定依据: %q 压缩解码 %v 与朴素解码 %v 等价(ASCII 不分大小写)", joinName(name), got, want)
	}
	if len(msg) >= len(naive) {
		t.Fatalf("含重复后缀的序列压缩后应更短, 压缩 %d 朴素 %d", len(msg), len(naive))
	}
}

func equalFoldLabels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

// 相同写入序列重放得到逐字节相同的报文。
func TestReplayDeterministic(t *testing.T) {
	names := [][]string{
		{"www", "example", "com"},
		{"mail", "example", "com"},
		nil,
		{"www", "example", "com"},
		{"a"},
	}
	build := func() []byte {
		e := NewEncoder()
		for _, name := range names {
			if _, err := e.Write(name); err != nil {
				t.Fatalf("写入失败: %v", err)
			}
		}
		return e.Bytes()
	}
	m1, m2 := build(), build()
	t.Logf("输入: 同一写入序列重放两次, 输出: 报文1=% x, 报文2=% x", m1, m2)
	t.Logf("判定依据: 重放应逐字节相同")
	if !bytes.Equal(m1, m2) {
		t.Fatalf("重放结果不同: % x vs % x", m1, m2)
	}
}

// 并发写入等价于某个串行顺序：每个返回偏移处解码都得到写入的名字。
func TestConcurrentWrite(t *testing.T) {
	names := [][]string{
		{"www", "example", "com"},
		{"mail", "example", "com"},
		{"www", "example", "org"},
		{"api", "example", "com"},
	}
	e := NewEncoder()
	const workers = 8
	const perWorker = 100
	type record struct {
		off  int
		name []string
	}
	recs := make(chan record, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := names[(w+i)%len(names)]
				off, err := e.Write(name)
				if err != nil {
					t.Errorf("并发写入失败: %v", err)
					return
				}
				recs <- record{off, name}
			}
		}(w)
	}
	wg.Wait()
	close(recs)

	msg := e.Bytes()
	count := 0
	for r := range recs {
		got, _, err := Decode(msg, r.off, DefaultBase)
		if err != nil {
			t.Fatalf("偏移 %d 解码失败: %v", r.off, err)
		}
		if !reflect.DeepEqual(got, r.name) {
			t.Fatalf("偏移 %d 应解码为 %v, 实际 %v", r.off, r.name, got)
		}
		count++
	}
	t.Logf("输入: %d 个 goroutine 各写 %d 次, 共 %d 个名字", workers, perWorker, count)
	t.Logf("输出: 报文 %d 字节, 全部 %d 个返回偏移解码均还原写入的名字", len(msg), count)
	t.Logf("判定依据: 并发结果等价于某个串行顺序, 登记表与已写字节一致")
	if count != workers*perWorker {
		t.Fatalf("应有 %d 条写入记录, 实际 %d", workers*perWorker, count)
	}
}

// 并发解码同一报文互不影响。
func TestConcurrentDecode(t *testing.T) {
	e := NewEncoder()
	names := [][]string{
		{"www", "example", "com"},
		{"mail", "example", "com"},
		nil,
	}
	offs := make([]int, 0, len(names))
	for _, name := range names {
		off, err := e.Write(name)
		if err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		offs = append(offs, off)
	}
	msg := e.Bytes()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				idx := (g + i) % len(names)
				got, _, err := Decode(msg, offs[idx], DefaultBase)
				if err != nil {
					t.Errorf("并发解码失败: %v", err)
					return
				}
				if !reflect.DeepEqual(got, names[idx]) {
					t.Errorf("解码 %d 应得 %v, 实际 %v", idx, names[idx], got)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("输入: 16 个 goroutine 对同一报文并发解码 200 次")
	t.Logf("输出: 全部解码结果与写入名字一致")
	t.Logf("判定依据: 解码为只读操作, 并发互不影响")
}
