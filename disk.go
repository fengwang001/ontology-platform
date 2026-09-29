package doublewrite

import "fmt"

// Disk is an in-memory block device. Writes only hit persistent storage when
// each sector is submitted via WriteSector; a power cut before submission loses
// everything not yet submitted. A partially written sector is torn: only the
// first sectorsBeforeCut bytes survive.
type Disk struct {
	sectorSize int
	sectors    [][]byte
	// BeforeSectorWrite, if set, is invoked before sector index idx is made
	// durable. Returning false simulates a power cut: the sector stays in its
	// prior state. A non-nil bytesWritten (0..sectorSize) tears the sector.
	BeforeSectorWrite func(idx int) (crash bool, bytesWritten int)
}

func NewDisk(numSectors, sectorSize int) *Disk {
	if numSectors < 0 || sectorSize <= 0 {
		panic("doublewrite: invalid disk geometry")
	}
	sectors := make([][]byte, numSectors)
	for i := range sectors {
		sectors[i] = make([]byte, sectorSize)
	}
	return &Disk{sectorSize: sectorSize, sectors: sectors}
}

func (d *Disk) NumSectors() int { return len(d.sectors) }

func (d *Disk) SectorSize() int { return d.sectorSize }

// ReadSector returns a copy so callers can never mutate durable state.
func (d *Disk) ReadSector(idx int) []byte {
	if idx < 0 || idx >= len(d.sectors) {
		panic("doublewrite: sector index out of range")
	}
	out := make([]byte, d.sectorSize)
	copy(out, d.sectors[idx])
	return out
}

// WriteSector durably stores one sector, honoring the crash hook.
func (d *Disk) WriteSector(idx int, data []byte) error {
	if idx < 0 || idx >= len(d.sectors) || len(data) != d.sectorSize {
		panic("doublewrite: invalid sector write")
	}
	crash := false
	bytesWritten := d.sectorSize
	if d.BeforeSectorWrite != nil {
		crash, bytesWritten = d.BeforeSectorWrite(idx)
	}
	if bytesWritten < 0 || bytesWritten > d.sectorSize {
		panic("doublewrite: invalid torn-write length")
	}
	if crash {
		if bytesWritten > 0 {
			copy(d.sectors[idx][:bytesWritten], data[:bytesWritten])
		}
		return ErrPowerCut
	}
	copy(d.sectors[idx], data)
	return nil
}

// Snapshot returns a deep copy of all sector bytes.
func (d *Disk) Snapshot() [][]byte {
	out := make([][]byte, len(d.sectors))
	for i, sec := range d.sectors {
		out[i] = append([]byte(nil), sec...)
	}
	return out
}

// Clone returns an independent disk with identical contents.
func (d *Disk) Clone() *Disk {
	return &Disk{sectorSize: d.sectorSize, sectors: d.Snapshot()}
}

// CorruptSector flips one byte at sector offset off to simulate silent corruption.
func (d *Disk) CorruptSector(idx, off int) error {
	if idx < 0 || idx >= len(d.sectors) || off < 0 || off >= d.sectorSize {
		return fmt.Errorf("doublewrite: corruption target out of range")
	}
	d.sectors[idx][off] ^= 0xFF
	return nil
}

func (d *Disk) readBytes(offset, length int) []byte {
	out := make([]byte, length)
	for i := 0; i < length; i++ {
		abs := offset + i
		out[i] = d.sectors[abs/d.sectorSize][abs%d.sectorSize]
	}
	return out
}

func (d *Disk) writeBytes(offset int, data []byte) error {
	for off := 0; off < len(data); off += d.sectorSize {
		secIdx := (offset + off) / d.sectorSize
		end := off + d.sectorSize
		if end > len(data) {
			end = len(data)
		}
		buf := d.ReadSector(secIdx)
		copy(buf[(offset+off)%d.sectorSize:], data[off:end])
		if err := d.WriteSector(secIdx, buf); err != nil {
			return err
		}
	}
	return nil
}
