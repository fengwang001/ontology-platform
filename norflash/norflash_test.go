package norflash

import (
	"bytes"
	"errors"
	"testing"
)

// mustNew builds a device or fails the test.
func mustNew(t *testing.T, s, z, p, nop, e int) *Device {
	t.Helper()
	d, err := New(s, z, p, nop, e)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d) = %v, want nil", s, z, p, nop, e, err)
	}
	return d
}

// mustRead reads n bytes at addr and fails the test on error.
func mustRead(t *testing.T, d *Device, addr, n int) []byte {
	t.Helper()
	b, err := d.Read(addr, n)
	if err != nil {
		t.Fatalf("Read(%d,%d) = %v, want nil", addr, n, err)
	}
	return b
}

func TestNewInvalidParams(t *testing.T) {
	cases := []struct {
		name            string
		s, z, p, nop, e int
	}{
		{"zero sectors", 0, 16, 4, 2, 2},
		{"zero sector bytes", 1, 0, 4, 2, 2},
		{"zero page bytes", 1, 16, 0, 2, 2},
		{"zero NOP", 1, 16, 4, 0, 2},
		{"zero E", 1, 16, 4, 2, 0},
		{"negative", -1, 16, 4, 2, 2},
		{"P does not divide Z", 1, 16, 6, 2, 2},
	}
	for _, c := range cases {
		_, err := New(c.s, c.z, c.p, c.nop, c.e)
		t.Logf("New(%d,%d,%d,%d,%d) -> err=%v (判定: 参数不合法须报 ErrInvalidParams)",
			c.s, c.z, c.p, c.nop, c.e, err)
		if !errors.Is(err, ErrInvalidParams) {
			t.Errorf("%s: got %v, want ErrInvalidParams", c.name, err)
		}
	}
}

func TestInitialState(t *testing.T) {
	d := mustNew(t, 2, 16, 4, 3, 2)
	got := mustRead(t, d, 0, 32)
	want := bytes.Repeat([]byte{0xFF}, 32)
	t.Logf("初始读 32 字节 -> %X (判定: 初始全为 0xFF)", got)
	if !bytes.Equal(got, want) {
		t.Fatalf("initial content = %X, want all 0xFF", got)
	}
	if d.ProgramCount(0) != 0 || d.EraseCount(0) != 0 {
		t.Fatalf("initial counters not zero")
	}
}

func TestProgramBasicAndReadCopy(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 3, 2)
	if err := d.Program(2, []byte{0xF0, 0x0F}); err != nil {
		t.Fatalf("Program = %v", err)
	}
	got := mustRead(t, d, 0, 4)
	t.Logf("Program(2,[F0 0F]) 后 Read(0,4) -> %X (判定: 按位与, 0xFF&0xF0=0xF0)", got)
	if !bytes.Equal(got, []byte{0xFF, 0xFF, 0xF0, 0x0F}) {
		t.Fatalf("content = %X", got)
	}
	// Mutating the returned slice must not affect the device.
	got[2] = 0x00
	again := mustRead(t, d, 0, 4)
	if again[2] != 0xF0 {
		t.Fatalf("Read did not return a copy")
	}
}

// 编程计数恰满 NOP 后，再多一次编程被拒，且报页号最小者。
func TestProgramCountReachNOPThenRejected(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 2, 2) // NOP = 2
	for i := 1; i <= 2; i++ {
		err := d.Program(0, []byte{0x0F})
		t.Logf("第 %d 次 Program(0,[0F]) -> err=%v, 页0计数=%d (判定: 不超过 NOP=2 应成功)",
			i, err, d.ProgramCount(0))
		if err != nil {
			t.Fatalf("program #%d = %v, want nil", i, err)
		}
	}
	if got := d.ProgramCount(0); got != 2 {
		t.Fatalf("ProgramCount(0) = %d, want 2", got)
	}
	before := mustRead(t, d, 0, 16)
	err := d.Program(0, []byte{0x03})
	t.Logf("第 3 次 Program(0,[03]) -> err=%v (判定: 页0计数已达 NOP=2, 应拒并报页0)", err)
	var pcErr *ProgramCountError
	if !errors.As(err, &pcErr) || pcErr.Page != 0 {
		t.Fatalf("err = %v, want ProgramCountError{Page:0}", err)
	}
	if got := mustRead(t, d, 0, 16); !bytes.Equal(got, before) {
		t.Fatalf("rejected program changed content: %X -> %X", before, got)
	}
	if got := d.ProgramCount(0); got != 2 {
		t.Fatalf("rejected program changed counter to %d", got)
	}
}

// 一次编程跨三页，每页各计一次。
func TestProgramSpanningThreePages(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 2) // 页大小 4, 页 0..3
	// 地址 3 起写 6 字节: 触及页 0 (字节3), 页 1 (字节4-7), 页 2 (字节8-9... 实际 3..8 -> 页0,1,2)
	err := d.Program(3, []byte{0xFE, 0x11, 0x22, 0x33, 0x44, 0x7F})
	t.Logf("Program(3, 6字节) -> err=%v, 页计数=[%d %d %d %d] (判定: 跨页0/1/2, 各计一次, 页3不计)",
		err, d.ProgramCount(0), d.ProgramCount(1), d.ProgramCount(2), d.ProgramCount(3))
	if err != nil {
		t.Fatalf("Program = %v", err)
	}
	for p, want := range []int{1, 1, 1, 0} {
		if got := d.ProgramCount(p); got != want {
			t.Errorf("ProgramCount(%d) = %d, want %d", p, got, want)
		}
	}
}

// 写入内容与原内容相同仍计一次编程。
func TestProgramSameContentStillCounts(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 2)
	if err := d.Program(0, []byte{0x0F}); err != nil {
		t.Fatalf("Program = %v", err)
	}
	err := d.Program(0, []byte{0x0F}) // 与当前内容完全相同
	t.Logf("重复 Program(0,[0F]) -> err=%v, 页0计数=%d (判定: 内容相同仍计一次)", err, d.ProgramCount(0))
	if err != nil {
		t.Fatalf("Program = %v", err)
	}
	if got := d.ProgramCount(0); got != 2 {
		t.Fatalf("ProgramCount(0) = %d, want 2", got)
	}
}

// 只差一个非法位即被拒，且内容与计数不变；非法位报地址最小者。
func TestProgramIllegalBitRejectsAtomically(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 2)
	if err := d.Program(1, []byte{0x00}); err != nil { // 地址1 变为 0x00
		t.Fatalf("Program = %v", err)
	}
	before := mustRead(t, d, 0, 16)
	// 数据 [0x0F, 0x01]: 地址0 合法(0xFF&0x0F), 地址1 非法(0x00 写回 1)
	err := d.Program(0, []byte{0x0F, 0x01})
	t.Logf("Program(0,[0F 01]) -> err=%v (判定: 地址1 要把 0 写回 1, 报最小非法地址1)", err)
	var ibErr *IllegalBitError
	if !errors.As(err, &ibErr) || ibErr.Addr != 1 {
		t.Fatalf("err = %v, want IllegalBitError{Addr:1}", err)
	}
	if got := mustRead(t, d, 0, 16); !bytes.Equal(got, before) {
		t.Fatalf("rejected program changed content: %X -> %X", before, got)
	}
	if got := d.ProgramCount(0); got != 1 {
		t.Fatalf("rejected program changed counter to %d, want 1", got)
	}
}

// Program 错误优先级: 空 data > 越界 > 计数满 > 非法位。
func TestProgramErrorPrecedence(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 1, 2)
	if err := d.Program(0, []byte{0x00}); err != nil { // 页0 计数=1=NOP, 地址0 内容=0x00
		t.Fatalf("Program = %v", err)
	}
	// 空 data 与越界同时存在 -> 报空 data
	err := d.Program(100, nil)
	t.Logf("Program(100,nil) -> %v (判定: 空 data 优先于越界)", err)
	if !errors.Is(err, ErrEmptyData) {
		t.Errorf("got %v, want ErrEmptyData", err)
	}
	// 越界 与 计数满/非法位同时存在 -> 报越界
	err = d.Program(14, []byte{0xFF, 0xFF, 0xFF})
	t.Logf("Program(14,3字节) -> %v (判定: 越界优先)", err)
	if !errors.Is(err, ErrAddressOutOfRange) {
		t.Errorf("got %v, want ErrAddressOutOfRange", err)
	}
	// 计数满 与 非法位同时存在(地址0内容0x00, 数据0xFF非法) -> 报计数满
	err = d.Program(0, []byte{0xFF})
	t.Logf("Program(0,[FF]) -> %v (判定: 页0计数已达NOP, 优先于非法位)", err)
	if !errors.Is(err, ErrProgramCountExhausted) {
		t.Errorf("got %v, want ErrProgramCountExhausted", err)
	}
	// 负地址越界
	if err := d.Program(-1, []byte{0xFF}); !errors.Is(err, ErrAddressOutOfRange) {
		t.Errorf("negative addr: got %v, want ErrAddressOutOfRange", err)
	}
}

// 计数满时报触及页中页号最小者。
func TestProgramCountErrorReportsLowestPage(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 1, 2)
	if err := d.Program(5, []byte{0x00}); err != nil { // 页1 计数=1=NOP
		t.Fatalf("Program = %v", err)
	}
	// 跨页 1,2: 页1 已满, 页2 未满 -> 报页1
	err := d.Program(5, []byte{0x00, 0x00, 0x00, 0x00})
	var pcErr *ProgramCountError
	t.Logf("跨页 Program 触及满页 -> %v (判定: 报触及页中页号最小者=1)", err)
	if !errors.As(err, &pcErr) || pcErr.Page != 1 {
		t.Fatalf("err = %v, want ProgramCountError{Page:1}", err)
	}
}

// 第 E 次擦除成功, 第 E+1 次被拒且不改变任何状态。
func TestEraseLifetimeLimit(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 2) // E = 2
	if err := d.Program(0, []byte{0x00}); err != nil {
		t.Fatalf("Program = %v", err)
	}
	for i := 1; i <= 2; i++ {
		err := d.Erase(0)
		t.Logf("第 %d 次 Erase(0) -> err=%v, 擦除计数=%d (判定: 不超过 E=2 应成功)",
			i, err, d.EraseCount(0))
		if err != nil {
			t.Fatalf("erase #%d = %v, want nil", i, err)
		}
	}
	if got := d.ProgramCount(0); got != 0 {
		t.Fatalf("erase did not clear program count, got %d", got)
	}
	if got := mustRead(t, d, 0, 4); !bytes.Equal(got, []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Fatalf("erase did not restore 0xFF, got %X", got)
	}
	err := d.Erase(0)
	t.Logf("第 3 次 Erase(0) -> err=%v (判定: 擦除计数已达 E=2, 被拒)", err)
	var ecErr *EraseCountError
	if !errors.As(err, &ecErr) || ecErr.Sector != 0 {
		t.Fatalf("err = %v, want EraseCountError{Sector:0}", err)
	}
	if got := d.EraseCount(0); got != 2 {
		t.Fatalf("rejected erase changed counter to %d", got)
	}
}

// Erase 与 ErasePartial 的扇区号越界 / k 越界, 且越界优先于寿命已尽。
func TestEraseRangeErrors(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 1)    // E = 1
	if err := d.Erase(0); err != nil { // 用掉唯一一次擦除
		t.Fatalf("Erase = %v", err)
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"Erase 扇区越界", d.Erase(1), ErrSectorOutOfRange},
		{"Erase 负扇区", d.Erase(-1), ErrSectorOutOfRange},
		{"ErasePartial 扇区越界", d.ErasePartial(2, 4), ErrSectorOutOfRange},
		{"ErasePartial k 为负", d.ErasePartial(0, -1), ErrKOutOfRange},
		{"ErasePartial k 超过 Z", d.ErasePartial(0, 17), ErrKOutOfRange},
		{"Erase 寿命已尽", d.Erase(0), ErrEraseCountExhausted},
		{"ErasePartial 寿命已尽", d.ErasePartial(0, 4), ErrEraseCountExhausted},
	}
	for _, c := range cases {
		t.Logf("%s -> %v", c.name, c.err)
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.err, c.want)
		}
	}
	if got := d.EraseCount(0); got != 1 {
		t.Fatalf("rejected erases changed counter to %d, want 1", got)
	}
}

// ErasePartial: 只有整页落在前 k 字节内的页计数清零; k 落在页中间时该页不清。
func TestErasePartialClearsOnlyFullPages(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 3) // 页 0..3, 每页 4 字节
	for p := 0; p < 4; p++ {
		if err := d.Program(p*4, []byte{0x00}); err != nil {
			t.Fatalf("Program page %d = %v", p, err)
		}
	}
	// k=6: 前 6 字节覆盖页0(0-3)整页, 页1(4-7)只覆盖一半 -> 只清页0
	err := d.ErasePartial(0, 6)
	t.Logf("ErasePartial(0,6) -> err=%v, 页计数=[%d %d %d %d] (判定: 仅页0整页落在前6字节内被清零)",
		err, d.ProgramCount(0), d.ProgramCount(1), d.ProgramCount(2), d.ProgramCount(3))
	if err != nil {
		t.Fatalf("ErasePartial = %v", err)
	}
	for p, want := range []int{0, 1, 1, 1} {
		if got := d.ProgramCount(p); got != want {
			t.Errorf("ProgramCount(%d) = %d, want %d", p, got, want)
		}
	}
	// 前 6 字节恢复 0xFF, 其余不变 (每页只有首字节被写成 0x00)
	got := mustRead(t, d, 0, 16)
	want := []byte{
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // 前 6 字节被擦除
		0xFF, 0xFF, // 页1 其余字节原本就是 0xFF
		0x00, 0xFF, 0xFF, 0xFF, // 页2 未受影响
		0x00, 0xFF, 0xFF, 0xFF, // 页3 未受影响
	}
	t.Logf("内容 -> %X (判定: 前6字节0xFF, 其余不变)", got)
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %X, want %X", got, want)
	}
	if got := d.EraseCount(0); got != 1 {
		t.Fatalf("EraseCount(0) = %d, want 1", got)
	}
}

// k=Z 时 ErasePartial 与 Erase 效果完全相同。
func TestErasePartialFullLengthEqualsErase(t *testing.T) {
	d1 := mustNew(t, 2, 16, 4, 5, 3)
	d2 := mustNew(t, 2, 16, 4, 5, 3)
	for _, d := range []*Device{d1, d2} {
		if err := d.Program(3, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
			t.Fatalf("Program = %v", err)
		}
	}
	if err := d1.Erase(0); err != nil {
		t.Fatalf("Erase = %v", err)
	}
	if err := d2.ErasePartial(0, 16); err != nil {
		t.Fatalf("ErasePartial = %v", err)
	}
	c1, c2 := mustRead(t, d1, 0, 32), mustRead(t, d2, 0, 32)
	t.Logf("Erase(0) 内容=%X, ErasePartial(0,16) 内容=%X (判定: k=Z 与 Erase 等价)", c1, c2)
	if !bytes.Equal(c1, c2) {
		t.Fatalf("content differs: %X vs %X", c1, c2)
	}
	for p := 0; p < 8; p++ {
		if d1.ProgramCount(p) != d2.ProgramCount(p) {
			t.Fatalf("page %d count differs: %d vs %d", p, d1.ProgramCount(p), d2.ProgramCount(p))
		}
	}
	if d1.EraseCount(0) != d2.EraseCount(0) {
		t.Fatalf("erase count differs: %d vs %d", d1.EraseCount(0), d2.EraseCount(0))
	}
}

// k=0 时只增加擦除计数, 内容与页计数不变。
func TestErasePartialZeroLength(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 3)
	if err := d.Program(0, []byte{0x00}); err != nil {
		t.Fatalf("Program = %v", err)
	}
	before := mustRead(t, d, 0, 16)
	err := d.ErasePartial(0, 0)
	t.Logf("ErasePartial(0,0) -> err=%v, 擦除计数=%d, 页0计数=%d (判定: k=0 只增加擦除计数)",
		err, d.EraseCount(0), d.ProgramCount(0))
	if err != nil {
		t.Fatalf("ErasePartial = %v", err)
	}
	if got := d.EraseCount(0); got != 1 {
		t.Fatalf("EraseCount(0) = %d, want 1", got)
	}
	if got := d.ProgramCount(0); got != 1 {
		t.Fatalf("ProgramCount(0) = %d, want 1", got)
	}
	if got := mustRead(t, d, 0, 16); !bytes.Equal(got, before) {
		t.Fatalf("content changed: %X -> %X", before, got)
	}
}

// 被拒的 ErasePartial(寿命已尽) 不增加任何计数、不改变内容。
func TestRejectedErasePartialChangesNothing(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 1) // E = 1
	if err := d.Program(0, []byte{0x00}); err != nil {
		t.Fatalf("Program = %v", err)
	}
	if err := d.Erase(0); err != nil { // 用掉唯一一次擦除
		t.Fatalf("Erase = %v", err)
	}
	if err := d.Program(0, []byte{0x0F}); err != nil {
		t.Fatalf("Program = %v", err)
	}
	before := mustRead(t, d, 0, 16)
	err := d.ErasePartial(0, 8)
	t.Logf("ErasePartial(0,8) -> err=%v (判定: 寿命已尽被拒, 计数与内容不变)", err)
	if !errors.Is(err, ErrEraseCountExhausted) {
		t.Fatalf("err = %v, want ErrEraseCountExhausted", err)
	}
	if got := d.EraseCount(0); got != 1 {
		t.Fatalf("EraseCount(0) = %d, want 1", got)
	}
	if got := d.ProgramCount(0); got != 1 {
		t.Fatalf("ProgramCount(0) = %d, want 1", got)
	}
	if got := mustRead(t, d, 0, 16); !bytes.Equal(got, before) {
		t.Fatalf("content changed: %X -> %X", before, got)
	}
}

// Read 边界: n 为负报错; n=0 且 addr 不超过容量合法; 越界报错。
func TestReadBounds(t *testing.T) {
	d := mustNew(t, 1, 16, 4, 5, 2)
	if _, err := d.Read(0, -1); !errors.Is(err, ErrNegativeLength) {
		t.Errorf("Read(0,-1) = %v, want ErrNegativeLength", err)
	}
	b, err := d.Read(16, 0) // n=0, addr=容量, 合法
	t.Logf("Read(16,0) -> len=%d err=%v (判定: n=0 且 addr 不超过容量合法)", len(b), err)
	if err != nil || len(b) != 0 {
		t.Errorf("Read(16,0) = (%v,%v), want empty,nil", b, err)
	}
	if _, err := d.Read(17, 0); !errors.Is(err, ErrAddressOutOfRange) {
		t.Errorf("Read(17,0) = %v, want ErrAddressOutOfRange", err)
	}
	if _, err := d.Read(-1, 0); !errors.Is(err, ErrAddressOutOfRange) {
		t.Errorf("Read(-1,0) = %v, want ErrAddressOutOfRange", err)
	}
	if _, err := d.Read(15, 2); !errors.Is(err, ErrAddressOutOfRange) {
		t.Errorf("Read(15,2) = %v, want ErrAddressOutOfRange", err)
	}
}

// 跨扇区编程合法。
func TestProgramAcrossSectors(t *testing.T) {
	d := mustNew(t, 2, 8, 4, 5, 2)
	err := d.Program(6, []byte{0x11, 0x22, 0x33, 0x44}) // 跨扇区0/1
	t.Logf("Program(6,4字节) -> err=%v (判定: 允许跨扇区)", err)
	if err != nil {
		t.Fatalf("Program = %v", err)
	}
	got := mustRead(t, d, 6, 4)
	if !bytes.Equal(got, []byte{0x11, 0x22, 0x33, 0x44}) {
		t.Fatalf("content = %X", got)
	}
	if d.ProgramCount(1) != 1 || d.ProgramCount(2) != 1 {
		t.Fatalf("counts = %d,%d, want 1,1", d.ProgramCount(1), d.ProgramCount(2))
	}
}
