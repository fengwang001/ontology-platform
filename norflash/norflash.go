// Package norflash implements a NOR Flash device model with physical
// bit-level semantics: programming can only flip bits from 1 to 0, and
// erasing restores a whole sector to all 1s (0xFF).
package norflash

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// Sentinel errors so callers can distinguish rejection reasons with
// errors.Is. Rejected operations never change any byte or counter.
var (
	// ErrInvalidParams: S, Z, P, NOP, E must all be >= 1 and P must divide Z.
	ErrInvalidParams = errors.New("norflash: invalid construction parameters")
	// ErrEmptyData: Program called with an empty data slice.
	ErrEmptyData = errors.New("norflash: program data is empty")
	// ErrAddressOutOfRange: addr < 0 or addr+len exceeds total capacity.
	ErrAddressOutOfRange = errors.New("norflash: address range out of bounds")
	// ErrProgramCountExhausted: a touched page already reached NOP programs.
	ErrProgramCountExhausted = errors.New("norflash: page program count reached NOP")
	// ErrIllegalBit: data has a 1 bit where the stored byte has a 0 bit.
	ErrIllegalBit = errors.New("norflash: program would flip a bit from 0 back to 1")
	// ErrSectorOutOfRange: sector index outside [0, S).
	ErrSectorOutOfRange = errors.New("norflash: sector index out of range")
	// ErrKOutOfRange: partial erase length outside [0, Z].
	ErrKOutOfRange = errors.New("norflash: partial erase length out of range")
	// ErrEraseCountExhausted: sector erase count already reached E.
	ErrEraseCountExhausted = errors.New("norflash: sector erase count reached E")
	// ErrNegativeLength: Read called with n < 0.
	ErrNegativeLength = errors.New("norflash: negative read length")
)

// ProgramCountError reports the lowest-numbered touched page whose program
// count has already reached NOP.
type ProgramCountError struct {
	Page int
}

func (e *ProgramCountError) Error() string {
	return fmt.Sprintf("%v (page %d)", ErrProgramCountExhausted, e.Page)
}

func (e *ProgramCountError) Unwrap() error { return ErrProgramCountExhausted }

// IllegalBitError reports the lowest address whose data byte has a 1 bit
// where the stored byte has a 0 bit.
type IllegalBitError struct {
	Addr int
}

func (e *IllegalBitError) Error() string {
	return fmt.Sprintf("%v (address %d)", ErrIllegalBit, e.Addr)
}

func (e *IllegalBitError) Unwrap() error { return ErrIllegalBit }

// EraseCountError reports the sector whose erase count has already
// reached E.
type EraseCountError struct {
	Sector int
}

func (e *EraseCountError) Error() string {
	return fmt.Sprintf("%v (sector %d)", ErrEraseCountExhausted, e.Sector)
}

func (e *EraseCountError) Unwrap() error { return ErrEraseCountExhausted }

// Device models a NOR Flash chip. All methods are safe for concurrent use;
// the result is equivalent to some serial order of the calls.
type Device struct {
	mu          sync.Mutex
	sectors     int
	sectorBytes int
	pageBytes   int
	nop         int
	maxErase    int
	data        []byte
	progCount   []int // per page
	eraseCount  []int // per sector
}

// New creates a device with the given geometry. All parameters must be
// >= 1 and pageBytes must divide sectorBytes. The device starts fully
// erased (all bytes 0xFF) with all counters at zero.
func New(sectors, sectorBytes, pageBytes, nop, maxErase int) (*Device, error) {
	if sectors < 1 || sectorBytes < 1 || pageBytes < 1 || nop < 1 || maxErase < 1 {
		return nil, ErrInvalidParams
	}
	if sectorBytes%pageBytes != 0 {
		return nil, ErrInvalidParams
	}
	if sectors > math.MaxInt/sectorBytes {
		return nil, ErrInvalidParams
	}
	d := &Device{
		sectors:     sectors,
		sectorBytes: sectorBytes,
		pageBytes:   pageBytes,
		nop:         nop,
		maxErase:    maxErase,
		data:        make([]byte, sectors*sectorBytes),
		progCount:   make([]int, sectors*(sectorBytes/pageBytes)),
		eraseCount:  make([]int, sectors),
	}
	for i := range d.data {
		d.data[i] = 0xFF
	}
	return d, nil
}

// Capacity returns the total device size in bytes (S*Z).
func (d *Device) Capacity() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.data)
}

// ProgramCount returns how many times the given page has been programmed
// since its last erase.
func (d *Device) ProgramCount(page int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if page < 0 || page >= len(d.progCount) {
		return -1
	}
	return d.progCount[page]
}

// EraseCount returns how many times the given sector has been erased.
func (d *Device) EraseCount(sector int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sector < 0 || sector >= len(d.eraseCount) {
		return -1
	}
	return d.eraseCount[sector]
}

// Program writes data at addr: each stored byte becomes the bitwise AND of
// its old value and the data byte. Every touched page is charged one
// program, regardless of how many bytes it contributes or whether the
// content changes. The whole operation is rejected (no byte or counter
// changes) on the first of: empty data, out-of-range address, a touched
// page at NOP, an illegal 0->1 bit.
func (d *Device) Program(addr int, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(data) == 0 {
		return ErrEmptyData
	}
	if addr < 0 || len(data) > len(d.data)-addr {
		return ErrAddressOutOfRange
	}
	firstPage := addr / d.pageBytes
	lastPage := (addr + len(data) - 1) / d.pageBytes
	for p := firstPage; p <= lastPage; p++ {
		if d.progCount[p] >= d.nop {
			return &ProgramCountError{Page: p}
		}
	}
	for i, b := range data {
		if d.data[addr+i]&b != b {
			return &IllegalBitError{Addr: addr + i}
		}
	}
	for i, b := range data {
		d.data[addr+i] &= b
	}
	for p := firstPage; p <= lastPage; p++ {
		d.progCount[p]++
	}
	return nil
}

// Erase restores sector sec to all 0xFF, clears its pages' program counts
// and increments its erase count. It succeeds only while the erase count
// is below E.
func (d *Device) Erase(sec int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sec < 0 || sec >= d.sectors {
		return ErrSectorOutOfRange
	}
	if d.eraseCount[sec] >= d.maxErase {
		return &EraseCountError{Sector: sec}
	}
	d.eraseLocked(sec, d.sectorBytes)
	return nil
}

// ErasePartial simulates an erase interrupted by power loss: only the
// first k bytes of sector sec are restored to 0xFF. Program counts are
// cleared only for pages lying entirely within the first k bytes. The
// erase count is incremented and limited by E exactly as in Erase.
func (d *Device) ErasePartial(sec, k int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if sec < 0 || sec >= d.sectors {
		return ErrSectorOutOfRange
	}
	if k < 0 || k > d.sectorBytes {
		return ErrKOutOfRange
	}
	if d.eraseCount[sec] >= d.maxErase {
		return &EraseCountError{Sector: sec}
	}
	d.eraseLocked(sec, k)
	return nil
}

// eraseLocked restores the first k bytes of sector sec to 0xFF, clears the
// program counts of the pages lying entirely within those k bytes and
// increments the sector erase count. The caller must hold d.mu and must
// have validated sec, k and the erase-count limit.
func (d *Device) eraseLocked(sec, k int) {
	base := sec * d.sectorBytes
	for i := 0; i < k; i++ {
		d.data[base+i] = 0xFF
	}
	pagesPerSector := d.sectorBytes / d.pageBytes
	fullPages := k / d.pageBytes
	for p := 0; p < fullPages; p++ {
		d.progCount[sec*pagesPerSector+p] = 0
	}
	d.eraseCount[sec]++
}

// Read returns a copy of the n bytes starting at addr. n == 0 is legal as
// long as addr does not exceed the capacity.
func (d *Device) Read(addr, n int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n < 0 {
		return nil, ErrNegativeLength
	}
	if addr < 0 || n > len(d.data)-addr {
		return nil, ErrAddressOutOfRange
	}
	out := make([]byte, n)
	copy(out, d.data[addr:addr+n])
	return out, nil
}
