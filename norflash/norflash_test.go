package norflash

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

type naiveDevice struct {
	sectors      int
	sectorSize   int
	pageSize     int
	maxPrograms  int
	maxErases    int
	data         []byte
	pagePrograms []int
	erasures     []int
}

func newNaive(sectors, sectorSize, pageSize, maxPrograms, maxErases int) *naiveDevice {
	return &naiveDevice{
		sectors:      sectors,
		sectorSize:   sectorSize,
		pageSize:     pageSize,
		maxPrograms:  maxPrograms,
		maxErases:    maxErases,
		data:         bytes.Repeat([]byte{0xFF}, sectors*sectorSize),
		pagePrograms: make([]int, sectors*(sectorSize/pageSize)),
		erasures:     make([]int, sectors),
	}
}

func (d *naiveDevice) touchedPages(addr, length int) []int {
	first := addr / d.pageSize
	last := (addr + length - 1) / d.pageSize
	pages := make([]int, 0, last-first+1)
	for page := first; page <= last; page++ {
		pages = append(pages, page)
	}
	return pages
}

func (d *naiveDevice) Program(addr int, data []byte) error {
	if len(data) == 0 {
		return ErrEmptyProgram
	}
	if addr < 0 || uint64(addr)+uint64(len(data)) > uint64(len(d.data)) {
		return ErrAddressOutOfRange
	}

	for _, page := range d.touchedPages(addr, len(data)) {
		if d.pagePrograms[page] >= d.maxPrograms {
			return ProgramLimitError{Page: page}
		}
	}
	for offset, value := range data {
		if value&^d.data[addr+offset] != 0 {
			return IllegalBitError{Address: addr + offset}
		}
	}

	for _, page := range d.touchedPages(addr, len(data)) {
		d.pagePrograms[page]++
	}
	for offset, value := range data {
		d.data[addr+offset] &= value
	}
	return nil
}

func (d *naiveDevice) ErasePartial(sector, size int) error {
	if sector < 0 || sector >= d.sectors {
		return ErrSectorOutOfRange
	}
	if size < 0 || size > d.sectorSize {
		return ErrInvalidPartialSize
	}
	if d.erasures[sector] >= d.maxErases {
		return ErrEraseLimitReached
	}

	start := sector * d.sectorSize
	for offset := 0; offset < size; offset++ {
		d.data[start+offset] = 0xFF
	}

	pagesPerSector := d.sectorSize / d.pageSize
	for page := 0; page < pagesPerSector; page++ {
		pageStart := start + page*d.pageSize
		if pageStart+d.pageSize <= start+size {
			d.pagePrograms[sector*pagesPerSector+page] = 0
		}
	}
	d.erasures[sector]++
	return nil
}

func (d *naiveDevice) Erase(sector int) error {
	return d.ErasePartial(sector, d.sectorSize)
}

func (d *naiveDevice) Read(addr, length int) ([]byte, error) {
	if length < 0 {
		return nil, ErrNegativeReadLength
	}
	if addr < 0 || uint64(addr)+uint64(length) > uint64(len(d.data)) {
		return nil, ErrAddressOutOfRange
	}
	return append([]byte(nil), d.data[addr:addr+length]...), nil
}

type stateSnapshot struct {
	data         []byte
	pagePrograms []int
	erasures     []int
}

func snapshotDevice(t *testing.T, d *Device) stateSnapshot {
	t.Helper()
	data, err := d.Read(0, d.Capacity())
	if err != nil {
		t.Fatalf("snapshot read: %v", err)
	}
	pages := make([]int, d.PageCount())
	for page := range pages {
		pages[page], _ = d.PageProgramCount(page)
	}
	erasures := make([]int, d.SectorCount())
	for sector := range erasures {
		erasures[sector], _ = d.SectorEraseCount(sector)
	}
	return stateSnapshot{data: data, pagePrograms: pages, erasures: erasures}
}

func snapshotNaive(d *naiveDevice) stateSnapshot {
	return stateSnapshot{
		data:         append([]byte(nil), d.data...),
		pagePrograms: append([]int(nil), d.pagePrograms...),
		erasures:     append([]int(nil), d.erasures...),
	}
}

func (s stateSnapshot) equal(other stateSnapshot) bool {
	return bytes.Equal(s.data, other.data) &&
		equalInts(s.pagePrograms, other.pagePrograms) &&
		equalInts(s.erasures, other.erasures)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertSameState(t *testing.T, d *Device, n *naiveDevice, basis string) {
	t.Helper()
	actual := snapshotDevice(t, d)
	reference := snapshotNaive(n)
	if !actual.equal(reference) {
		t.Fatalf("%s\nactual data=%v pages=%v erasures=%v\nnaive  data=%v pages=%v erasures=%v",
			basis, actual.data, actual.pagePrograms, actual.erasures,
			reference.data, reference.pagePrograms, reference.erasures)
	}
	t.Logf("判定依据=%s；输出=data:%x pageCounts:%v eraseCounts:%v", basis, actual.data, actual.pagePrograms, actual.erasures)
}

func sameError(actual, expected error) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	if !errors.Is(actual, expected) {
		return false
	}
	switch want := expected.(type) {
	case ProgramLimitError:
		got, ok := actual.(ProgramLimitError)
		return ok && got.Page == want.Page
	case IllegalBitError:
		got, ok := actual.(IllegalBitError)
		return ok && got.Address == want.Address
	default:
		return true
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name                                  string
		sectors, sectorSize, pageSize, nop, e int
	}{
		{"zero sector", 0, 8, 4, 1, 1},
		{"zero sector size", 1, 0, 4, 1, 1},
		{"zero page size", 1, 8, 0, 1, 1},
		{"zero nop", 1, 8, 4, 0, 1},
		{"zero erase limit", 1, 8, 4, 1, 0},
		{"page does not divide sector", 1, 10, 4, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.sectors, tc.sectorSize, tc.pageSize, tc.nop, tc.e)
			t.Logf("输入=New(%d,%d,%d,%d,%d)；输出=%v；判定依据=参数均需不小于1且页整除扇区",
				tc.sectors, tc.sectorSize, tc.pageSize, tc.nop, tc.e, err)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestMetadataAndInvalidQueries(t *testing.T) {
	dev, err := New(2, 8, 4, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if dev.SectorCount() != 2 || dev.SectorSize() != 8 || dev.PageSize() != 4 ||
		dev.Capacity() != 16 || dev.PageCount() != 4 || dev.MaxPrograms() != 3 || dev.MaxErases() != 5 {
		t.Fatalf("unexpected metadata: S=%d Z=%d P=%d cap=%d pages=%d NOP=%d E=%d",
			dev.SectorCount(), dev.SectorSize(), dev.PageSize(), dev.Capacity(),
			dev.PageCount(), dev.MaxPrograms(), dev.MaxErases())
	}
	if _, ok := dev.PageProgramCount(-1); ok {
		t.Fatal("negative page query was accepted")
	}
	if _, ok := dev.PageProgramCount(4); ok {
		t.Fatal("page query beyond end was accepted")
	}
	if _, ok := dev.SectorEraseCount(-1); ok {
		t.Fatal("negative sector query was accepted")
	}
	if _, ok := dev.SectorEraseCount(2); ok {
		t.Fatal("sector query beyond end was accepted")
	}
	t.Log("输入=构造(2,8,4,3,5)及越界计数查询；输出=元数据正确，非法查询返回false；判定依据=查询接口只暴露合法页/扇区")
}

func TestProgramSemanticsAndLimits(t *testing.T) {
	dev, err := New(2, 12, 4, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	ref := newNaive(2, 12, 4, 2, 3)

	steps := []struct {
		name string
		addr int
		data []byte
		want error
	}{
		{"cross two pages", 3, []byte{0x7F, 0x7F}, nil},
		{"same bytes still program once", 3, []byte{0x7F, 0x7F}, nil},
		{"smallest touched page is full", 3, []byte{0x7F, 0x7F}, ProgramLimitError{Page: 0}},
		{"one program crosses three pages", 13, []byte{0xFF, 0xFF, 0x00, 0x3F, 0x2F, 0x1F, 0x0F, 0x07, 0x03, 0x0F}, nil},
		{"fresh page first program", 8, []byte{0x00}, nil},
		{"fresh page reaches nop", 8, []byte{0x00}, nil},
		{"program limit precedes illegal bit", 8, []byte{0x01}, ProgramLimitError{Page: 2}},
		{"only illegal zero-to-one bit", 15, []byte{0x01}, IllegalBitError{Address: 15}},
		{"empty data rejected", 10, []byte{}, ErrEmptyProgram},
		{"negative address", -1, []byte{0xFF}, ErrAddressOutOfRange},
		{"end exceeds capacity", 23, []byte{0xFF, 0xFF}, ErrAddressOutOfRange},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			input := append([]byte(nil), step.data...)
			err = dev.Program(step.addr, input)
			refErr := ref.Program(step.addr, append([]byte(nil), step.data...))
			t.Logf("输入=Program(addr=%d,data=%x)；输出错误=%v；判定依据=%s",
				step.addr, step.data, err, step.name)
			if !sameError(err, step.want) || !sameError(refErr, step.want) {
				t.Fatalf("actual=%v reference=%v want=%v", err, refErr, step.want)
			}
			assertSameState(t, dev, ref, step.name)
		})
	}

	read, err := dev.Read(0, 24)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入=Read(0,16)；输出=%x；判定依据=编程结果是原字节与输入逐位相与", read)
	wantData := []byte{
		0xFF, 0xFF, 0xFF, 0x7F, 0x7F, 0xFF, 0xFF, 0xFF,
		0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00,
		0x3F, 0x2F, 0x1F, 0x0F, 0x07, 0x03, 0x0F, 0xFF,
	}
	if !bytes.Equal(read, wantData) {
		t.Fatalf("data=%x, want=%x", read, wantData)
	}

	read[0] = 0x00
	again, _ := dev.Read(0, 1)
	t.Logf("输入=修改Read返回切片后再次读取；输出=%x；判定依据=Read必须返回副本", again)
	if again[0] != 0xFF {
		t.Fatalf("Read returned mutable device storage")
	}

	empty, err := dev.Read(24, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("read at end with n=0: data=%v err=%v", empty, err)
	}
	if _, err = dev.Read(0, -1); !errors.Is(err, ErrNegativeReadLength) {
		t.Fatalf("negative read length: %v", err)
	}
	if _, err = dev.Read(24, 1); !errors.Is(err, ErrAddressOutOfRange) {
		t.Fatalf("read beyond end: %v", err)
	}
}

func TestErasePartialAndLifetime(t *testing.T) {
	dev, err := New(2, 16, 4, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	ref := newNaive(2, 16, 4, 2, 2)

	mustProgram(t, dev, ref, 0, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if err := dev.ErasePartial(0, 6); err != nil {
		t.Fatal(err)
	}
	if err := ref.ErasePartial(0, 6); err != nil {
		t.Fatal(err)
	}
	assertSameState(t, dev, ref, "k=6：字节0..5恢复，页0整页覆盖而清零，页1跨在k中间不清零")

	page0, _ := dev.PageProgramCount(0)
	page1, _ := dev.PageProgramCount(1)
	covered, _ := dev.Read(0, 6)
	unchanged, _ := dev.Read(6, 1)
	t.Logf("输入=ErasePartial(0,6)；输出=covered=%x unchanged=%x page0=%d page1=%d；判定依据=只有整页落在前k字节内才清零",
		covered, unchanged, page0, page1)
	if page0 != 0 || page1 != 1 || !bytes.Equal(covered, []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) || unchanged[0] != 0x00 {
		t.Fatalf("partial erase state wrong: covered=%x unchanged=%x pages=%d,%d", covered, unchanged, page0, page1)
	}

	before := snapshotDevice(t, dev)
	err = dev.ErasePartial(2, 4)
	t.Logf("输入=ErasePartial(sector=2,k=4)；输出=%v；判定依据=扇区越界优先，操作不得改状态", err)
	if !errors.Is(err, ErrSectorOutOfRange) || !snapshotDevice(t, dev).equal(before) {
		t.Fatal("invalid sector must reject without changes")
	}
	before = snapshotDevice(t, dev)
	err = dev.ErasePartial(0, 17)
	t.Logf("输入=ErasePartial(0,17)；输出=%v；判定依据=k必须在0到Z之间，操作不得改状态", err)
	if !errors.Is(err, ErrInvalidPartialSize) || !snapshotDevice(t, dev).equal(before) {
		t.Fatal("invalid k must reject without changes")
	}

	if err := dev.Erase(0); err != nil {
		t.Fatal(err)
	}
	if err := ref.Erase(0); err != nil {
		t.Fatal(err)
	}
	count, _ := dev.SectorEraseCount(0)
	assertSameState(t, dev, ref, "第E=2次成功擦除：整扇区为FF，所有页计数清零")
	if count != 2 {
		t.Fatalf("erase count=%d, want 2", count)
	}

	before = snapshotDevice(t, dev)
	err = dev.Erase(0)
	t.Logf("输入=Erase(0)；输出=%v；判定依据=第E+1次擦除因寿命已尽被拒绝", err)
	if !errors.Is(err, ErrEraseLimitReached) || !snapshotDevice(t, dev).equal(before) {
		t.Fatal("Erase after E successes must reject without changes")
	}
	err = dev.ErasePartial(0, 0)
	t.Logf("输入=寿命已尽后ErasePartial(0,0)；输出=%v；判定依据=被拒操作不得增加擦除计数", err)
	if !errors.Is(err, ErrEraseLimitReached) || !snapshotDevice(t, dev).equal(before) {
		t.Fatal("partial erase after E successes must reject without changes")
	}
}

func TestErasePartialFullAndZeroEquivalence(t *testing.T) {
	eraseDevice, err := New(1, 16, 4, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	partialDevice, err := New(1, 16, 4, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	eraseRef := newNaive(1, 16, 4, 2, 2)
	partialRef := newNaive(1, 16, 4, 2, 2)

	for _, target := range []struct {
		device *Device
		ref    *naiveDevice
	}{
		{eraseDevice, eraseRef},
		{partialDevice, partialRef},
	} {
		if err := target.device.Program(0, []byte{0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
			t.Fatal(err)
		}
		if err := target.ref.Program(0, []byte{0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
			t.Fatal(err)
		}
		if err := target.device.ErasePartial(0, 0); err != nil {
			t.Fatal(err)
		}
		if err := target.ref.ErasePartial(0, 0); err != nil {
			t.Fatal(err)
		}
	}

	zeroSnapshot := snapshotDevice(t, eraseDevice)
	t.Logf("输入=ErasePartial(0,0)；输出=data:%x pages:%v erasures:%v；判定依据=k=0不改字节和页计数，只增加擦除计数",
		zeroSnapshot.data, zeroSnapshot.pagePrograms, zeroSnapshot.erasures)
	if !bytes.Equal(zeroSnapshot.data, append(bytes.Repeat([]byte{0x00}, 5), bytes.Repeat([]byte{0xFF}, 11)...)) ||
		zeroSnapshot.pagePrograms[0] != 1 || zeroSnapshot.pagePrograms[1] != 1 || zeroSnapshot.erasures[0] != 1 {
		t.Fatalf("k=0 changed more than erase count: %+v", zeroSnapshot)
	}

	if err := eraseDevice.Erase(0); err != nil {
		t.Fatal(err)
	}
	if err := eraseRef.Erase(0); err != nil {
		t.Fatal(err)
	}
	if err := partialDevice.ErasePartial(0, 16); err != nil {
		t.Fatal(err)
	}
	if err := partialRef.ErasePartial(0, 16); err != nil {
		t.Fatal(err)
	}

	eraseSnapshot := snapshotDevice(t, eraseDevice)
	partialSnapshot := snapshotDevice(t, partialDevice)
	t.Logf("输入=Erase(0) 与 ErasePartial(0,16)；输出=erased:%x partial:%x pages:%v；判定依据=k=Z与完整擦除效果完全相同",
		eraseSnapshot.data, partialSnapshot.data, partialSnapshot.pagePrograms)
	if !eraseSnapshot.equal(partialSnapshot) ||
		!eraseSnapshot.equal(snapshotNaive(eraseRef)) ||
		!partialSnapshot.equal(snapshotNaive(partialRef)) {
		t.Fatalf("k=Z is not equivalent to Erase: %+v vs %+v", eraseSnapshot, partialSnapshot)
	}
}

func mustProgram(t *testing.T, d *Device, ref *naiveDevice, addr int, data []byte) {
	t.Helper()
	if err := d.Program(addr, data); err != nil {
		t.Fatal(err)
	}
	if err := ref.Program(addr, data); err != nil {
		t.Fatal(err)
	}
}

type replayStep struct {
	name         string
	program      bool
	read         bool
	erase        bool
	erasePartial bool
	addr         int
	data         []byte
	sector       int
	size         int
}

func TestDeterministicReplay(t *testing.T) {
	steps := []replayStep{
		{name: "program pages 0-2", program: true, addr: 2, data: []byte{0x0F, 0xF0, 0x00, 0x77, 0x33, 0x11, 0x00}},
		{name: "illegal attempt", program: true, addr: 3, data: []byte{0xFF}},
		{name: "partial erase k=4", erasePartial: true, sector: 0, size: 4},
		{name: "program after page reset", program: true, addr: 0, data: []byte{0xAA}},
		{name: "illegal after aa", program: true, addr: 0, data: []byte{0xFF}},
		{name: "read sector zero", read: true, addr: 0, size: 16},
		{name: "full erase", erase: true, sector: 0},
		{name: "erase beyond life", erase: true, sector: 0},
	}

	first := runReplay(t, steps)
	second := runReplay(t, steps)
	for i := range first {
		if !first[i].equal(second[i]) || sameError(first[i].err, second[i].err) == false {
			t.Fatalf("step %q is not reproducible: %+v vs %+v", steps[i].name, first[i], second[i])
		}
		t.Logf("重放输入=%s；两次输出错误=%v；判定依据=相同操作序列得到相同内容、计数与错误", steps[i].name, first[i].err)
	}
}

type replayResult struct {
	state stateSnapshot
	data  []byte
	err   error
}

func (r replayResult) equal(other replayResult) bool {
	return r.state.equal(other.state) && bytes.Equal(r.data, other.data)
}

func runReplay(t *testing.T, steps []replayStep) []replayResult {
	t.Helper()
	dev, err := New(2, 16, 4, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]replayResult, len(steps))
	for i, step := range steps {
		var read []byte
		switch {
		case step.program:
			err = dev.Program(step.addr, append([]byte(nil), step.data...))
		case step.read:
			read, err = dev.Read(step.addr, step.size)
		case step.erase:
			err = dev.Erase(step.sector)
		case step.erasePartial:
			err = dev.ErasePartial(step.sector, step.size)
		}
		results[i] = replayResult{state: snapshotDevice(t, dev), data: read, err: err}
	}
	return results
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	dev, err := New(2, 16, 4, 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for page := 0; page < 8; page++ {
		wait.Add(2)
		addr := page * 4
		go func() {
			defer wait.Done()
			_ = dev.Program(addr, []byte{0x00})
		}()
		go func() {
			defer wait.Done()
			_ = dev.Program(addr, []byte{0x00})
		}()
	}

	for reader := 0; reader < 8; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			data, err := dev.Read(0, dev.Capacity())
			if err != nil || len(data) != dev.Capacity() {
				t.Errorf("concurrent read: data=%d err=%v", len(data), err)
			}
		}()
	}

	successes := make(chan error, 4)
	limits := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := dev.Erase(1)
			if err == nil {
				successes <- nil
			} else if errors.Is(err, ErrEraseLimitReached) {
				limits <- err
			} else {
				t.Errorf("concurrent erase: %v", err)
			}
		}()
	}
	wait.Wait()
	close(successes)
	close(limits)

	if len(successes) != 2 || len(limits) != 2 {
		t.Fatalf("concurrent erase successes=%d limits=%d, want 2 and 2", len(successes), len(limits))
	}
	for page := 0; page < 8; page++ {
		count, _ := dev.PageProgramCount(page)
		if count < 0 || count > 1 {
			t.Fatalf("page %d count %d exceeds invariant", page, count)
		}
	}
	eraseCount, _ := dev.SectorEraseCount(1)
	t.Logf("输入=8页各2次并发编程、并发读、4次并发擦除；输出=成功擦除=%d 寿命拒绝=%d sector1计数=%d；判定依据=擦除可成为并发编程之间的串行点，最终计数不越界",
		len(successes), len(limits), eraseCount)
	if eraseCount != 2 {
		t.Fatalf("sector erase count=%d, want 2", eraseCount)
	}
}
