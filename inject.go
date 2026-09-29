package doublewrite

// CorruptInPlacePage flips one byte within an in-place page's sector at
// sector-local offset off. It simulates silent on-disk corruption that no
// in-flight write produced, used to drive the unrecoverable-page path.
func (s *Store) CorruptInPlacePage(pageNo, sectorInPage, off int) error {
	if pageNo < 0 || pageNo >= s.cfg.NumPages ||
		sectorInPage < 0 || sectorInPage >= s.cfg.SectorsPerPage ||
		off < 0 || off >= s.disk.SectorSize() {
		return ErrPageOutOfRange
	}
	sec := s.dataStart + pageNo*s.cfg.SectorsPerPage + sectorInPage
	return s.disk.CorruptSector(sec, off)
}

// CorruptDWAPage flips one byte inside a staged doublewrite page image.
func (s *Store) CorruptDWAPage(slot, sectorInPage, off int) error {
	if slot < 0 || slot >= s.cfg.BatchCapacity ||
		sectorInPage < 0 || sectorInPage >= s.cfg.SectorsPerPage ||
		off < 0 || off >= s.disk.SectorSize() {
		return ErrPageOutOfRange
	}
	sec := s.dwaStart + slot*s.cfg.SectorsPerPage + sectorInPage
	return s.disk.CorruptSector(sec, off)
}

// CorruptMarker flips one byte in the completion marker sector.
func (s *Store) CorruptMarker(off int) error {
	if off < 0 || off >= s.disk.SectorSize() {
		return ErrMarkerCorrupt
	}
	return s.disk.CorruptSector(s.markerSec, off)
}

// Disk exposes the underlying block device for tests.
func (s *Store) Disk() *Disk { return s.disk }

// Geometry returns the derived layout (test/inspection helper).
func (s *Store) Geometry() (dataStart, dwaStart, markerSec int) {
	return s.dataStart, s.dwaStart, s.markerSec
}
