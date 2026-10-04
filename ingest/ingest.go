// Package ingest tracks stream epochs and active publishers, and acts as
// the facade coordinating segment and dvr for all service operations.
package ingest

import (
	"errors"
	"sync"

	"ontology/dvr"
	"ontology/segment"
)

// maxNow is the inclusive upper bound of accepted wall-clock timestamps.
const maxNow = int64(1_000_000_000_000)

// maxDur is the inclusive upper bound of one group-of-pictures duration.
const maxDur = int64(60_000)

// maxPlaylistEnd mirrors the dvr query range bound for pre-validation.
const maxPlaylistEnd = int64(1_000_000_000_000_000)

var (
	ErrInvalidParam   = dvr.ErrInvalidParam
	ErrClock          = errors.New("ingest: clock regression")
	ErrStreamNotFound = errors.New("ingest: stream not found")
	ErrStreamExists   = errors.New("ingest: stream already exists")
	ErrStreamBusy     = errors.New("ingest: stream busy")
	ErrNotConnected   = errors.New("ingest: stream not connected")
	ErrTakenOver      = errors.New("ingest: epoch taken over")
	ErrEpochInvalid   = errors.New("ingest: invalid epoch")
)

type stream struct {
	epoch      int64
	active     bool
	lastActive int64
	seg        *segment.Segmenter
	win        *dvr.Window
}

// Service is the facade for stream ingest, segmentation and replay.
type Service struct {
	d, w, t int64
	mu      sync.Mutex
	maxNow  int64
	streams map[string]*stream
}

// New validates the construction parameters and returns an empty service.
func New(d, w, t int64) (*Service, error) {
	if d < 1 || d > maxDur || w < 1 || w > 1_000_000_000 || t < 1 || t > 1_000_000_000 {
		return nil, ErrInvalidParam
	}
	return &Service{d: d, w: w, t: t, maxNow: -1, streams: map[string]*stream{}}, nil
}

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

// CreateStream registers a new stream key.
func (s *Service) CreateStream(now int64, key []byte) error {
	if !validNow(now) || len(key) == 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	k := string(key)
	if _, ok := s.streams[k]; ok {
		return ErrStreamExists
	}
	s.streams[k] = &stream{seg: segment.New(s.d), win: dvr.NewWindow(s.w)}
	s.maxNow = now
	return nil
}

// Connect attaches a publisher and returns its epoch.
func (s *Service) Connect(now int64, key []byte, force bool) (int64, error) {
	if !validNow(now) || len(key) == 0 {
		return 0, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return 0, ErrClock
	}
	st, ok := s.streams[string(key)]
	if !ok {
		return 0, ErrStreamNotFound
	}
	if st.active {
		if !force && now-st.lastActive < s.t {
			return 0, ErrStreamBusy
		}
		// Takeover: seal the old publisher's leftover fragment as a short
		// segment belonging to the old epoch.
		if seg, ok := st.seg.Flush(); ok {
			st.win.Add(seg)
		}
	}
	st.epoch++
	st.active = true
	st.lastActive = now
	st.seg.BeginEpoch()
	s.maxNow = now
	return st.epoch, nil
}

// Disconnect lets the active publisher of the epoch detach.
func (s *Service) Disconnect(now int64, key []byte, epoch int64) error {
	if !validNow(now) || len(key) == 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	st, ok := s.streams[string(key)]
	if !ok {
		return ErrStreamNotFound
	}
	if !st.active {
		return ErrNotConnected
	}
	if epoch < st.epoch {
		return ErrTakenOver
	}
	if epoch > st.epoch {
		return ErrEpochInvalid
	}
	if seg, ok := st.seg.Flush(); ok {
		st.win.Add(seg)
	}
	st.active = false
	s.maxNow = now
	return nil
}

// Push ingests one group of pictures for the active publisher of the epoch.
func (s *Service) Push(now int64, key []byte, epoch int64, dur int64) error {
	if !validNow(now) || len(key) == 0 || dur < 1 || dur > maxDur {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.maxNow {
		return ErrClock
	}
	st, ok := s.streams[string(key)]
	if !ok {
		return ErrStreamNotFound
	}
	if !st.active {
		return ErrNotConnected
	}
	if epoch < st.epoch {
		return ErrTakenOver
	}
	if epoch > st.epoch {
		return ErrEpochInvalid
	}
	st.lastActive = now
	if seg, ok := st.seg.Push(dur); ok {
		st.win.Add(seg)
	}
	s.maxNow = now
	return nil
}

// Playlist queries the replay window of a stream.
func (s *Service) Playlist(key []byte, from, to int64) (dvr.Playlist, error) {
	if len(key) == 0 || from < 0 || from >= to || to > maxPlaylistEnd {
		return dvr.Playlist{}, ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[string(key)]
	if !ok {
		return dvr.Playlist{}, ErrStreamNotFound
	}
	return st.win.Playlist(from, to)
}
