package norflash

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naive 是按规则逐字节写成的朴素参考模拟, 用于与 Device 对照。
// 它刻意只用最简单的循环与集合, 不共享 Device 的任何实现。
type naive struct {
	s, z, p, nop, e int
	data            []byte
	prog            []int
	eraseCnt        []int
}

func newNaive(s, z, p, nop, e int) *naive {
	m := &naive{
		s: s, z: z, p: p, nop: nop, e: e,
		data:     make([]byte, s*z),
		prog:     make([]int, s*(z/p)),
		eraseCnt: make([]int, s),
	}
	for i := range m.data {
		m.data[i] = 0xFF
	}
	return m
}

func (m *naive) program(addr int, data []byte) error {
	if len(data) == 0 {
		return ErrEmptyData
	}
	if addr < 0 || addr+len(data) > len(m.data) {
		return ErrAddressOutOfRange
	}
	// 按地址升序检查页计数: 第一个达 NOP 的页即页号最小者。
	for a := addr; a < addr+len(data); a++ {
		pg := a / m.p
		if m.prog[pg] >= m.nop {
			return &ProgramCountError{Page: pg}
		}
	}
	// 逐字节检查非法位: 数据为 1 而存储为 0。
	for i := 0; i < len(data); i++ {
		stored := m.data[addr+i]
		for bit := 0; bit < 8; bit++ {
			if data[i]&(1<<uint(bit)) != 0 && stored&(1<<uint(bit)) == 0 {
				return &IllegalBitError{Addr: addr + i}
			}
		}
	}
	for i := 0; i < len(data); i++ {
		m.data[addr+i] &= data[i]
	}
	touched := map[int]bool{}
	for a := addr; a < addr+len(data); a++ {
		touched[a/m.p] = true
	}
	for pg := range touched {
		m.prog[pg]++
	}
	return nil
}

func (m *naive) erase(sec int) error {
	if sec < 0 || sec >= m.s {
		return ErrSectorOutOfRange
	}
	if m.eraseCnt[sec] >= m.e {
		return &EraseCountError{Sector: sec}
	}
	for i := 0; i < m.z; i++ {
		m.data[sec*m.z+i] = 0xFF
	}
	for pg := sec * (m.z / m.p); pg < (sec+1)*(m.z/m.p); pg++ {
		m.prog[pg] = 0
	}
	m.eraseCnt[sec]++
	return nil
}

func (m *naive) erasePartial(sec, k int) error {
	if sec < 0 || sec >= m.s {
		return ErrSectorOutOfRange
	}
	if k < 0 || k > m.z {
		return ErrKOutOfRange
	}
	if m.eraseCnt[sec] >= m.e {
		return &EraseCountError{Sector: sec}
	}
	for i := 0; i < k; i++ {
		m.data[sec*m.z+i] = 0xFF
	}
	// 整页落在前 k 字节内才清零: 页尾偏移 <= k。
	for pg := 0; pg < m.z/m.p; pg++ {
		if (pg+1)*m.p <= k {
			m.prog[sec*(m.z/m.p)+pg] = 0
		}
	}
	m.eraseCnt[sec]++
	return nil
}

func (m *naive) read(addr, n int) ([]byte, error) {
	if n < 0 {
		return nil, ErrNegativeLength
	}
	if addr < 0 || addr+n > len(m.data) {
		return nil, ErrAddressOutOfRange
	}
	out := make([]byte, n)
	copy(out, m.data[addr:addr+n])
	return out, nil
}

// sameError 判定两个错误是否属于同一可区分原因(含携带的页号/地址/扇区号)。
func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	var aPC, bPC *ProgramCountError
	if errors.As(a, &aPC) || errors.As(b, &bPC) {
		return errors.As(a, &aPC) && errors.As(b, &bPC) && aPC.Page == bPC.Page
	}
	var aIB, bIB *IllegalBitError
	if errors.As(a, &aIB) || errors.As(b, &bIB) {
		return errors.As(a, &aIB) && errors.As(b, &bIB) && aIB.Addr == bIB.Addr
	}
	var aEC, bEC *EraseCountError
	if errors.As(a, &aEC) || errors.As(b, &bEC) {
		return errors.As(a, &aEC) && errors.As(b, &bEC) && aEC.Sector == bEC.Sector
	}
	for _, sent := range []error{
		ErrEmptyData, ErrAddressOutOfRange, ErrSectorOutOfRange,
		ErrKOutOfRange, ErrNegativeLength,
	} {
		if errors.Is(a, sent) || errors.Is(b, sent) {
			return errors.Is(a, sent) && errors.Is(b, sent)
		}
	}
	return false
}

// compareState 校验 Device 与朴素模拟的内容和所有计数完全一致。
func compareState(t *testing.T, step int, d *Device, m *naive) {
	t.Helper()
	got, err := d.Read(0, len(m.data))
	if err != nil {
		t.Fatalf("step %d: Read all = %v", step, err)
	}
	for i := range m.data {
		if got[i] != m.data[i] {
			t.Fatalf("step %d: byte %d = %02X, naive = %02X", step, i, got[i], m.data[i])
		}
	}
	for pg := range m.prog {
		if d.ProgramCount(pg) != m.prog[pg] {
			t.Fatalf("step %d: ProgramCount(%d) = %d, naive = %d",
				step, pg, d.ProgramCount(pg), m.prog[pg])
		}
	}
	for sec := range m.eraseCnt {
		if d.EraseCount(sec) != m.eraseCnt[sec] {
			t.Fatalf("step %d: EraseCount(%d) = %d, naive = %d",
				step, sec, d.EraseCount(sec), m.eraseCnt[sec])
		}
	}
}

// 随机操作序列下, Device 与朴素模拟的内容、计数、错误必须逐步一致;
// 日志打印每步的输入、输出与判定依据。
func TestAgainstNaiveModel(t *testing.T) {
	const (
		s, z, p = 3, 16, 4
		nop, e  = 3, 2
		steps   = 500
	)
	d := mustNew(t, s, z, p, nop, e)
	m := newNaive(s, z, p, nop, e)
	rng := rand.New(rand.NewSource(1095))
	cap := s * z

	for step := 0; step < steps; step++ {
		var dErr, mErr error
		var desc string
		switch rng.Intn(4) {
		case 0: // Program
			addr := rng.Intn(cap+2) - 1 // 偶尔越界
			n := rng.Intn(8)
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(rng.Intn(256))
			}
			dErr = d.Program(addr, data)
			mErr = m.program(addr, data)
			desc = fmt.Sprintf("Program(%d, % X)", addr, data)
		case 1: // Erase
			sec := rng.Intn(s+2) - 1
			dErr = d.Erase(sec)
			mErr = m.erase(sec)
			desc = fmt.Sprintf("Erase(%d)", sec)
		case 2: // ErasePartial
			sec := rng.Intn(s+2) - 1
			k := rng.Intn(z+3) - 1
			dErr = d.ErasePartial(sec, k)
			mErr = m.erasePartial(sec, k)
			desc = fmt.Sprintf("ErasePartial(%d, %d)", sec, k)
		case 3: // Read
			addr := rng.Intn(cap+2) - 1
			n := rng.Intn(6) - 1 // 偶尔为负
			dOut, de := d.Read(addr, n)
			mOut, me := m.read(addr, n)
			dErr, mErr = de, me
			if de == nil && me == nil {
				for i := range mOut {
					if dOut[i] != mOut[i] {
						t.Fatalf("step %d: Read(%d,%d) = % X, naive = % X",
							step, addr, n, dOut, mOut)
					}
				}
			}
			desc = fmt.Sprintf("Read(%d, %d)", addr, n)
		}
		t.Logf("step %d: %s -> device=%v naive=%v (判定: 两者错误原因与状态须一致)",
			step, desc, dErr, mErr)
		if !sameError(dErr, mErr) {
			t.Fatalf("step %d: %s: device err = %v, naive err = %v", step, desc, dErr, mErr)
		}
		compareState(t, step, d, m)
	}
}

// 相同操作序列重放两次, 内容、计数与错误必须完全相同。
func TestReplayDeterminism(t *testing.T) {
	run := func(t *testing.T) (*Device, []error) {
		d := mustNew(t, 2, 16, 4, 2, 2)
		rng := rand.New(rand.NewSource(7))
		var errs []error
		for i := 0; i < 100; i++ {
			switch rng.Intn(3) {
			case 0:
				data := []byte{byte(rng.Intn(256)), byte(rng.Intn(256))}
				errs = append(errs, d.Program(rng.Intn(32), data))
			case 1:
				errs = append(errs, d.Erase(rng.Intn(2)))
			case 2:
				errs = append(errs, d.ErasePartial(rng.Intn(2), rng.Intn(17)))
			}
		}
		return d, errs
	}
	d1, errs1 := run(t)
	d2, errs2 := run(t)
	for i := range errs1 {
		if !sameError(errs1[i], errs2[i]) {
			t.Fatalf("op %d: errors differ: %v vs %v", i, errs1[i], errs2[i])
		}
	}
	c1, c2 := mustRead(t, d1, 0, 32), mustRead(t, d2, 0, 32)
	for i := range c1 {
		if c1[i] != c2[i] {
			t.Fatalf("byte %d differs between replays: %02X vs %02X", i, c1[i], c2[i])
		}
	}
	t.Logf("两次重放 100 步后内容=%X, 判定: 完全一致", c1)
}

// 并发调用下不变量始终成立: 页计数不超过 NOP, 扇区擦除计数不超过 E,
// 且与 -race 一起运行时无数据竞争。
func TestConcurrentInvariants(t *testing.T) {
	const (
		s, z, p = 4, 16, 4
		nop, e  = 5, 3
		workers = 8
		iters   = 200
	)
	d := mustNew(t, s, z, p, nop, e)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < iters; i++ {
				switch rng.Intn(4) {
				case 0:
					data := []byte{byte(rng.Intn(256)), byte(rng.Intn(256))}
					_ = d.Program(rng.Intn(s*z), data)
				case 1:
					_ = d.Erase(rng.Intn(s))
				case 2:
					_ = d.ErasePartial(rng.Intn(s), rng.Intn(z+1))
				case 3:
					_, _ = d.Read(rng.Intn(s*z), rng.Intn(4))
				}
			}
		}(int64(w))
	}
	wg.Wait()
	for pg := 0; pg < s*(z/p); pg++ {
		if got := d.ProgramCount(pg); got > nop {
			t.Fatalf("ProgramCount(%d) = %d exceeds NOP=%d", pg, got, nop)
		}
	}
	for sec := 0; sec < s; sec++ {
		if got := d.EraseCount(sec); got > e {
			t.Fatalf("EraseCount(%d) = %d exceeds E=%d", sec, got, e)
		}
	}
	t.Logf("并发 %d 协程 x %d 次操作后, 所有页计数 <= %d, 所有扇区擦除计数 <= %d (判定: 不变量成立)",
		workers, iters, nop, e)
}
