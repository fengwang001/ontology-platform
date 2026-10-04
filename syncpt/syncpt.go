package syncpt

import (
	"errors"
	"sort"
)

var (
	ErrDupSync = errors.New("duplicate sync point")
	ErrSkew    = errors.New("sync point skew")
)

type Point struct {
	K int64
	W int64
}

type Set struct {
	points []Point
	seen   map[int64]struct{}
}

func NewSet() *Set {
	return &Set{seen: make(map[int64]struct{})}
}

func (s *Set) Add(k, w int64) error {
	if err := s.Check(k, w); err != nil {
		return err
	}
	pos := sort.Search(len(s.points), func(i int) bool {
		return s.points[i].K >= k
	})
	s.points = append(s.points, Point{})
	copy(s.points[pos+1:], s.points[pos:])
	s.points[pos] = Point{K: k, W: w}
	s.seen[k] = struct{}{}
	return nil
}

func (s *Set) Check(k, w int64) error {
	if _, ok := s.seen[k]; ok {
		return ErrDupSync
	}
	pos := sort.Search(len(s.points), func(i int) bool {
		return s.points[i].K >= k
	})
	if pos > 0 && s.points[pos-1].W >= w {
		return ErrSkew
	}
	if pos < len(s.points) && w >= s.points[pos].W {
		return ErrSkew
	}
	return nil
}

func (s *Set) AtOrBelow(k int64) (Point, bool) {
	pos := sort.Search(len(s.points), func(i int) bool {
		return s.points[i].K > k
	})
	if pos == 0 {
		return Point{}, false
	}
	return s.points[pos-1], true
}

func (s *Set) AtOrAbove(k int64) (Point, bool) {
	pos := sort.Search(len(s.points), func(i int) bool {
		return s.points[i].K >= k
	})
	if pos == len(s.points) {
		return Point{}, false
	}
	return s.points[pos], true
}

func (s *Set) Last() (Point, bool) {
	if len(s.points) == 0 {
		return Point{}, false
	}
	return s.points[len(s.points)-1], true
}

func (s *Set) Len() int {
	return len(s.points)
}
