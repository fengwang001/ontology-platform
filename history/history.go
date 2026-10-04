package history

import "fmt"

type Mark int

const (
	Clean Mark = iota
	Flaky
	Broken
)

type Record struct {
	Samples     []int
	Window      []Mark
	Quarantined bool
	CleanStreak int
}

type Result struct {
	Mark     Mark
	Duration int
	Passed   bool
}

type Store struct {
	d       int
	records map[string]*Record
}

func New(samples int) *Store {
	if samples < 1 || samples > 10 {
		panic(fmt.Sprintf("history: samples out of range: %d", samples))
	}
	return &Store{d: samples, records: make(map[string]*Record)}
}

func (s *Store) Snapshot(name string) Record {
	if r := s.records[name]; r != nil {
		return r.copy()
	}
	return Record{}
}

func (s *Store) Finish(name string, result Result, windowSize, flakyThreshold, cleanReleases int) {
	r := s.records[name]
	if r == nil {
		r = &Record{}
		s.records[name] = r
	}

	if result.Passed {
		r.Samples = append(r.Samples, result.Duration)
		if len(r.Samples) > s.d {
			r.Samples = append([]int(nil), r.Samples[len(r.Samples)-s.d:]...)
		}
	}

	if !r.Quarantined {
		r.Window = append(r.Window, result.Mark)
		if len(r.Window) > windowSize {
			r.Window = append([]Mark(nil), r.Window[len(r.Window)-windowSize:]...)
		}
		flaky := 0
		for _, mark := range r.Window {
			if mark == Flaky {
				flaky++
			}
		}
		if flaky >= flakyThreshold {
			r.Quarantined = true
			r.CleanStreak = 0
		}
		return
	}

	if result.Mark == Clean {
		r.CleanStreak++
		if r.CleanStreak >= cleanReleases {
			r.Quarantined = false
			r.CleanStreak = 0
			r.Window = nil
		}
		return
	}

	r.CleanStreak = 0
}

func (r Record) copy() Record {
	r.Samples = append([]int(nil), r.Samples...)
	r.Window = append([]Mark(nil), r.Window...)
	return r
}
