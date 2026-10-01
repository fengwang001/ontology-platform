package norflash

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sync"
)

var (
	ErrInvalidConfig       = errors.New("norflash: invalid configuration")
	ErrEmptyProgram        = errors.New("norflash: empty program data")
	ErrAddressOutOfRange   = errors.New("norflash: address out of range")
	ErrNegativeReadLength  = errors.New("norflash: negative read length")
	ErrProgramLimitReached = errors.New("norflash: page program limit reached")
	ErrIllegalBit          = errors.New("norflash: cannot program a zero bit back to one")
	ErrSectorOutOfRange    = errors.New("norflash: sector out of range")
	ErrInvalidPartialSize  = errors.New("norflash: partial erase size out of range")
	ErrEraseLimitReached   = errors.New("norflash: sector erase limit reached")
)

type ProgramLimitError struct {
	Page int
}

func (e ProgramLimitError) Error() string {
	return fmt.Sprintf("%s: page %d", ErrProgramLimitReached, e.Page)
}

func (e ProgramLimitError) Unwrap() error {
	return ErrProgramLimitReached
}

type IllegalBitError struct {
	Address int
}

func (e IllegalBitError) Error() string {
	return fmt.Sprintf("%s: address %d", ErrIllegalBit, e.Address)
}

func (e IllegalBitError) Unwrap() error {
	return ErrIllegalBit
}

type Device struct {
	mu             sync.RWMutex
	sectorCount    int
	sectorSize     int
	pageSize       int
	maxPrograms    int
	maxErases      int
	data           []byte
	pagePrograms   []int
	sectorErasures []int
}

// New creates an erased NOR flash device with the given geometry and limits.
func New(sectorCount, sectorSize, pageSize, maxPrograms, maxErases int) (*Device, error) {
	if sectorCount < 1 || sectorSize < 1 || pageSize < 1 || maxPrograms < 1 || maxErases < 1 {
		return nil, ErrInvalidConfig
	}
	if sectorSize%pageSize != 0 {
		return nil, ErrInvalidConfig
	}

	capacity64 := uint64(sectorCount) * uint64(sectorSize)
	pageCount64 := uint64(sectorCount) * uint64(sectorSize/pageSize)
	if capacity64 > math.MaxInt || pageCount64 > math.MaxInt {
		return nil, ErrInvalidConfig
	}

	pageCount := int(pageCount64)
	capacity := int(capacity64)
	return &Device{
		sectorCount:    sectorCount,
		sectorSize:     sectorSize,
		pageSize:       pageSize,
		maxPrograms:    maxPrograms,
		maxErases:      maxErases,
		data:           bytes.Repeat([]byte{0xFF}, capacity),
		pagePrograms:   make([]int, pageCount),
		sectorErasures: make([]int, sectorCount),
	}, nil
}

// Program applies data by ANDing every touched byte with the input byte.
func (d *Device) Program(addr int, data []byte) error {
	if len(data) == 0 {
		return ErrEmptyProgram
	}
	if uint64(addr)+uint64(len(data)) > uint64(len(d.data)) {
		return ErrAddressOutOfRange
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if addr < 0 || uint64(addr)+uint64(len(data)) > uint64(len(d.data)) {
		return ErrAddressOutOfRange
	}

	firstPage := addr / d.pageSize
	lastPage := (addr + len(data) - 1) / d.pageSize
	for page := firstPage; page <= lastPage; page++ {
		if d.pagePrograms[page] >= d.maxPrograms {
			return ProgramLimitError{Page: page}
		}
	}

	for offset, value := range data {
		current := d.data[addr+offset]
		if value&^current != 0 {
			return IllegalBitError{Address: addr + offset}
		}
	}

	for page := firstPage; page <= lastPage; page++ {
		d.pagePrograms[page]++
	}
	for offset, value := range data {
		d.data[addr+offset] &= value
	}

	return nil
}

// Erase performs a successful lifetime-counted full sector erase.
func (d *Device) Erase(sector int) error {
	if sector < 0 || sector >= d.sectorCount {
		return ErrSectorOutOfRange
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sectorErasures[sector] >= d.maxErases {
		return ErrEraseLimitReached
	}

	d.eraseSector(sector, d.sectorSize)
	return nil
}

// ErasePartial restores the first size bytes of a sector after a power interruption.
func (d *Device) ErasePartial(sector, size int) error {
	if sector < 0 || sector >= d.sectorCount {
		return ErrSectorOutOfRange
	}
	if size < 0 || size > d.sectorSize {
		return ErrInvalidPartialSize
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.sectorErasures[sector] >= d.maxErases {
		return ErrEraseLimitReached
	}

	d.eraseSector(sector, size)
	return nil
}

// Read returns an independent copy of the requested byte range.
func (d *Device) Read(addr, length int) ([]byte, error) {
	if length < 0 {
		return nil, ErrNegativeReadLength
	}
	if uint64(addr)+uint64(length) > uint64(len(d.data)) {
		return nil, ErrAddressOutOfRange
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	if addr < 0 || uint64(addr)+uint64(length) > uint64(len(d.data)) {
		return nil, ErrAddressOutOfRange
	}

	result := make([]byte, length)
	copy(result, d.data[addr:addr+length])
	return result, nil
}

// SectorCount returns the number of sectors.
func (d *Device) SectorCount() int {
	return d.sectorCount
}

// SectorSize returns bytes per sector.
func (d *Device) SectorSize() int {
	return d.sectorSize
}

// PageSize returns bytes per page.
func (d *Device) PageSize() int {
	return d.pageSize
}

// Capacity returns S*Z.
func (d *Device) Capacity() int {
	return d.sectorCount * d.sectorSize
}

// PageCount returns the number of pages across all sectors.
func (d *Device) PageCount() int {
	return len(d.pagePrograms)
}

// MaxPrograms returns the per-page program count permitted between erases.
func (d *Device) MaxPrograms() int {
	return d.maxPrograms
}

// MaxErases returns the number of successful erases permitted per sector.
func (d *Device) MaxErases() int {
	return d.maxErases
}

// PageProgramCount returns the current program count of one page.
func (d *Device) PageProgramCount(page int) (int, bool) {
	if page < 0 || page >= len(d.pagePrograms) {
		return 0, false
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.pagePrograms[page], true
}

// SectorEraseCount returns the successful erase count of one sector.
func (d *Device) SectorEraseCount(sector int) (int, bool) {
	if sector < 0 || sector >= d.sectorCount {
		return 0, false
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.sectorErasures[sector], true
}

func (d *Device) eraseSector(sector, size int) {
	start := sector * d.sectorSize
	for offset := 0; offset < size; offset++ {
		d.data[start+offset] = 0xFF
	}

	pagesPerSector := d.sectorSize / d.pageSize
	firstPage := sector * pagesPerSector
	clearedPages := size / d.pageSize
	for page := firstPage; page < firstPage+clearedPages; page++ {
		d.pagePrograms[page] = 0
	}
	d.sectorErasures[sector]++
}
