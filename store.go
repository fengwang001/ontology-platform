package doublewrite

import (
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
)

const (
	// marker layout inside its single sector:
	// magic(4) "DWMC" | count(4) | crc32(4) over the preceding bytes
	markerMagic   = 0x44574D43
	markerBaseOff = 0
	markerMagicSz = 4
	markerCountSz = 4
	markerCRCSz   = 4
)

// Config describes the geometry of a store on one block device.
type Config struct {
	NumPages       int
	SectorsPerPage int
	BatchCapacity  int
}

// Store implements atomic batch page flushing backed by a doublewrite area.
type Store struct {
	disk       *Disk
	cfg        Config
	logger     *log.Logger
	pageSize   int
	dataStart  int // sector index of the first in-place page
	dwaStart   int // sector index of the doublewrite area
	markerSec  int // sector index of the completion marker
	sectorsAll int
	mu         sync.RWMutex
}

func NewStore(disk *Disk, cfg Config, logger *log.Logger) *Store {
	if cfg.NumPages <= 0 || cfg.SectorsPerPage <= 0 || cfg.BatchCapacity <= 0 {
		panic("doublewrite: invalid config")
	}
	pageSize := cfg.SectorsPerPage * disk.SectorSize()
	dataStart := 0
	dwaStart := dataStart + cfg.NumPages*cfg.SectorsPerPage
	markerSec := dwaStart + cfg.BatchCapacity*cfg.SectorsPerPage
	need := markerSec + 1
	if disk.NumSectors() < need {
		panic(fmt.Sprintf("doublewrite: disk has %d sectors, need %d", disk.NumSectors(), need))
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Store{
		disk:       disk,
		cfg:        cfg,
		logger:     logger,
		pageSize:   pageSize,
		dataStart:  dataStart,
		dwaStart:   dwaStart,
		markerSec:  markerSec,
		sectorsAll: need,
	}
}

func (s *Store) inPlaceOffset(pageNo int) int {
	return (s.dataStart + pageNo*s.cfg.SectorsPerPage) * s.disk.SectorSize()
}

func (s *Store) dwaOffset(slot int) int {
	return (s.dwaStart + slot*s.cfg.SectorsPerPage) * s.disk.SectorSize()
}

func (s *Store) readInPlace(pageNo int) []byte {
	return s.disk.readBytes(s.inPlaceOffset(pageNo), s.pageSize)
}

func (s *Store) writeInPlace(pageNo int, img []byte) error {
	return s.disk.writeBytes(s.inPlaceOffset(pageNo), img)
}

func (s *Store) writeDWA(slot int, img []byte) error {
	return s.disk.writeBytes(s.dwaOffset(slot), img)
}

func (s *Store) readDWA(slot int) []byte {
	return s.disk.readBytes(s.dwaOffset(slot), s.pageSize)
}

func (s *Store) encodeMarker(count int) []byte {
	sec := make([]byte, s.disk.SectorSize())
	putUint32(sec[0:4], markerMagic)
	putUint32(sec[4:8], uint32(count))
	putUint32(sec[8:12], checksum(sec[0:8]))
	return sec
}

// readMarker returns the validated batch count. A torn or corrupted marker
// (including a never-written, all-zero sector) yields ErrMarkerCorrupt.
func (s *Store) readMarker() (int, error) {
	sec := s.disk.ReadSector(s.markerSec)
	if getUint32(sec[0:4]) != markerMagic {
		return 0, ErrMarkerCorrupt
	}
	count := int(getUint32(sec[4:8]))
	if count <= 0 || count > s.cfg.BatchCapacity {
		return 0, ErrMarkerCorrupt
	}
	if checksum(sec[0:8]) != getUint32(sec[8:12]) {
		return 0, ErrMarkerCorrupt
	}
	return count, nil
}

func (s *Store) clearMarker() error {
	return s.disk.WriteSector(s.markerSec, make([]byte, s.disk.SectorSize()))
}

type validatedPage struct {
	pageNo  int
	version uint64
	img     []byte
}

// validateBatch performs every whole-batch check. On any failure not one
// sector is written: callers run it before touching the doublewrite area.
func (s *Store) validateBatch(pages [][]byte) ([]validatedPage, error) {
	s.logger.Printf("input: flush batch of %d pages", len(pages))
	if len(pages) == 0 {
		return nil, ErrEmptyBatch
	}
	if len(pages) > s.cfg.BatchCapacity {
		return nil, fmt.Errorf("%w: %d > capacity %d", ErrBatchTooLarge, len(pages), s.cfg.BatchCapacity)
	}
	seen := make(map[int]bool, len(pages))
	vp := make([]validatedPage, 0, len(pages))
	for i, raw := range pages {
		info, _, err := ParsePage(raw, s.pageSize)
		if err != nil {
			return nil, fmt.Errorf("batch slot %d: %w", i, err)
		}
		pageNo := int(info.PageNo)
		if pageNo < 0 || pageNo >= s.cfg.NumPages {
			return nil, fmt.Errorf("%w: page %d", ErrPageOutOfRange, pageNo)
		}
		if seen[pageNo] {
			return nil, fmt.Errorf("%w: page %d", ErrDuplicatePage, pageNo)
		}
		seen[pageNo] = true
		vp = append(vp, validatedPage{pageNo: pageNo, version: info.Version, img: raw})
	}
	// Version monotonicity is checked per page against the current in-place
	// image. A corrupt in-place page carries no trustworthy version, so the
	// new page may overwrite it (its version is counted as zero in reports).
	sort.Slice(vp, func(i, j int) bool { return vp[i].pageNo < vp[j].pageNo })
	for _, p := range vp {
		cur := s.readInPlace(p.pageNo)
		info, _, err := ParsePage(cur, s.pageSize)
		if err != nil {
			s.logger.Printf("check: page %d in-place corrupt, treating its version as 0", p.pageNo)
			continue
		}
		if p.version <= info.Version {
			return nil, fmt.Errorf("%w: page %d new=%d in-place=%d",
				ErrVersionNotNewer, p.pageNo, p.version, info.Version)
		}
	}
	s.logger.Printf("batch validated: %s", describeBatch(vp))
	return vp, nil
}

func describeBatch(vp []validatedPage) string {
	parts := make([]string, len(vp))
	for i, p := range vp {
		parts[i] = fmt.Sprintf("{page:%d ver:%d}", p.pageNo, p.version)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// Flush validates the batch, writes every page into the doublewrite area,
// stamps the completion marker, then writes each page back in place.
//
// Write order (sector-by-sector):
//  1. every batch page into its doublewrite slot,
//  2. the completion marker,
//  3. every batch page back to its in-place location.
//
// Flush holds the writer lock, so batches become visible one at a time.
func (s *Store) Flush(pages [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	vp, err := s.validateBatch(pages)
	if err != nil {
		s.logger.Printf("output: flush rejected: %v", err)
		return err
	}

	for slot, p := range vp {
		if err := s.writeDWA(slot, p.img); err != nil {
			s.logger.Printf("output: power cut while staging page %d in doublewrite area: %v", p.pageNo, err)
			return err
		}
	}
	if err := s.disk.WriteSector(s.markerSec, s.encodeMarker(len(vp))); err != nil {
		s.logger.Printf("output: power cut while writing completion marker: %v", err)
		return err
	}
	s.logger.Printf("decision: completion marker committed, batch is now roll-forward committed")

	for _, p := range vp {
		if err := s.writeInPlace(p.pageNo, p.img); err != nil {
			s.logger.Printf("output: power cut while writing page %d in place: %v", p.pageNo, err)
			return err
		}
	}
	if err := s.clearMarker(); err != nil {
		s.logger.Printf("output: power cut while clearing completion marker after roll-forward: %v", err)
		return err
	}
	s.logger.Printf("output: flush complete, %d pages durable in place and doublewrite area retired", len(vp))
	return nil
}

// ReadPage returns one durable, checksum-valid page.
// Concurrent readers only ever observe a fully committed state: they take the
// read lock, which blocks only while a batch is being made effective.
func (s *Store) ReadPage(pageNo int) ([]byte, PageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if pageNo < 0 || pageNo >= s.cfg.NumPages {
		return nil, PageInfo{}, fmt.Errorf("%w: page %d", ErrPageOutOfRange, pageNo)
	}
	img := s.readInPlace(pageNo)
	info, payload, err := ParsePage(img, s.pageSize)
	if err != nil {
		s.logger.Printf("read: page %d corrupt in place (version counted as 0): %v", pageNo, err)
		return nil, PageInfo{PageNo: uint32(pageNo), Version: 0}, err
	}
	return payload, info, nil
}

// PageDecision records how recovery treated one page.
type PageDecision struct {
	PageNo        int
	InPlaceValid  bool
	InPlaceVer    uint64
	CopyValid     bool
	CopyVer       uint64
	CopyPageNo    uint32
	Action        string
	Unrecoverable bool
}

// RecoveryReport describes one recovery run.
type RecoveryReport struct {
	MarkerValid bool
	Decisions   []PageDecision
}

// Recover replays or ignores the doublewrite area after a restart.
//
// Marker valid: each doublewrite slot whose page image checks out is rolled
// forward over in-place pages that are corrupt or carry an older version.
// In-place pages whose version is not lower are left untouched (a valid newer
// in-place page is never rolled back to an older doublewrite residue).
//
// Marker invalid: the doublewrite area is ignored wholesale and in-place
// pages are never modified.
//
// A page that is corrupt in place and has no checksum-valid copy anywhere is
// reported Unrecoverable; its bytes are never guessed and its version is zero.
//
// Recover is idempotent: after a committed roll-forward the marker is cleared,
// so a second run finds no marker and changes no bytes.
func (s *Store) Recover() (RecoveryReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	report := RecoveryReport{}
	count, err := s.readMarker()
	if err != nil {
		s.logger.Printf("decision: completion marker invalid/absent (%v); doublewrite area ignored, in-place untouched", err)
		report.MarkerValid = false
		for pageNo := 0; pageNo < s.cfg.NumPages; pageNo++ {
			if _, _, perr := ParsePage(s.readInPlace(pageNo), s.pageSize); perr != nil {
				report.Decisions = append(report.Decisions, PageDecision{
					PageNo:        pageNo,
					InPlaceVer:    0,
					Action:        "unrecoverable",
					Unrecoverable: true,
				})
				s.logger.Printf("decision: page %d corrupt in place with no usable copy -> unrecoverable", pageNo)
			}
		}
		return report, nil
	}

	report.MarkerValid = true
	s.logger.Printf("decision: completion marker valid, committed batch size %d -> roll forward", count)
	for slot := 0; slot < count; slot++ {
		copyImg := s.readDWA(slot)
		copyInfo, _, copyErr := ParsePage(copyImg, s.pageSize)
		d := PageDecision{
			PageNo:     slot,
			CopyValid:  copyErr == nil,
			CopyVer:    0,
			CopyPageNo: 0,
		}
		if copyErr == nil {
			d.PageNo = int(copyInfo.PageNo)
			d.CopyPageNo = copyInfo.PageNo
			d.CopyVer = copyInfo.Version
		}

		curImg := s.readInPlace(d.PageNo)
		curInfo, _, curErr := ParsePage(curImg, s.pageSize)
		d.InPlaceValid = curErr == nil
		if curErr == nil {
			d.InPlaceVer = curInfo.Version
		}

		switch {
		case d.CopyValid && (!d.InPlaceValid || d.CopyVer > d.InPlaceVer):
			if werr := s.writeInPlace(d.PageNo, copyImg); werr != nil {
				return report, werr
			}
			d.Action = "roll-forward"
			s.logger.Printf("decision: page %d copy ver %d vs in-place ver %d (valid=%v) -> roll forward",
				d.PageNo, d.CopyVer, d.InPlaceVer, d.InPlaceValid)
		case d.CopyValid:
			d.Action = "keep-in-place"
			s.logger.Printf("decision: page %d copy ver %d <= in-place ver %d -> keep in place, never roll back",
				d.PageNo, d.CopyVer, d.InPlaceVer)
		case !d.InPlaceValid:
			d.Action = "unrecoverable"
			d.Unrecoverable = true
			s.logger.Printf("decision: page %d corrupt both in doublewrite area and in place -> unrecoverable, no guessing",
				d.PageNo)
		default:
			// Copy slot torn/corrupt but in-place is valid: the page already
			// reached its new version (or an even newer one), nothing to do.
			d.Action = "keep-in-place"
			s.logger.Printf("decision: page %d doublewrite copy unreadable but in-place ver %d valid -> keep in place",
				d.PageNo, d.InPlaceVer)
		}
		report.Decisions = append(report.Decisions, d)
	}

	sort.Slice(report.Decisions, func(i, j int) bool {
		return report.Decisions[i].PageNo < report.Decisions[j].PageNo
	})

	unrecoverable := 0
	for _, d := range report.Decisions {
		if d.Unrecoverable {
			unrecoverable++
		}
	}
	if unrecoverable == 0 {
		if err := s.clearMarker(); err != nil {
			return report, err
		}
		s.logger.Printf("output: recovery roll-forward complete, marker cleared; %d decisions", len(report.Decisions))
	} else {
		// Keep the valid marker so the unreadable slot is not mistaken for a
		// clean slate; the already-rolled pages stay byte-identical on rerun.
		s.logger.Printf("output: recovery found %d unrecoverable page(s), marker retained for audit", unrecoverable)
	}
	return report, nil
}
