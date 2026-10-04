package pool

import (
	"errors"
	"sort"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnavailable     = errors.New("hero unavailable")
)

const (
	Ban = iota
	Pick
)

type Record struct {
	Hero   int
	Kind   int
	Team   int
	Player int
}

type Pool struct {
	h       int
	records map[int]Record
	blocked []int
	touched int
}

func (p *Pool) HeroCount() int { return p.h }

func New(heroCount int) *Pool {
	return &Pool{h: heroCount, records: make(map[int]Record)}
}

func (p *Pool) Touch() int {
	return p.touched
}

func (p *Pool) ResetTouch() {
	p.touched = 0
}

func (p *Pool) Used(hero int) bool {
	if !p.valid(hero) {
		return false
	}
	p.touched++
	_, ok := p.records[hero]
	return ok
}

func (p *Pool) Ban(hero, team int) error {
	if team < 1 || team > 2 {
		return ErrInvalidArgument
	}
	return p.add(hero, Record{Hero: hero, Kind: Ban, Team: team})
}

func (p *Pool) Pick(hero, team, player int) error {
	if team < 1 || team > 2 || player < 0 {
		return ErrInvalidArgument
	}
	return p.add(hero, Record{Hero: hero, Kind: Pick, Team: team, Player: player})
}

func (p *Pool) Records() []Record {
	out := make([]Record, 0, len(p.records))
	for _, hero := range p.blocked {
		out = append(out, p.records[hero])
	}
	return out
}

func (p *Pool) Clone() *Pool {
	records := make(map[int]Record, len(p.records))
	for hero, record := range p.records {
		records[hero] = record
	}
	return &Pool{h: p.h, records: records, blocked: append([]int(nil), p.blocked...)}
}

func (p *Pool) Restore(from *Pool) {
	records := make(map[int]Record, len(from.records))
	for hero, record := range from.records {
		records[hero] = record
	}
	p.records = records
	p.blocked = append([]int(nil), from.blocked...)
}

func (p *Pool) Choose(preferred int) (hero int, touched int, ok bool) {
	p.touched = 0
	if p.valid(preferred) {
		p.touched++
		if _, used := p.records[preferred]; !used {
			return preferred, p.touched, true
		}
	}

	candidate := 1
	for _, blocked := range p.blocked {
		p.touched++
		if blocked > candidate {
			return candidate, p.touched, true
		}
		if blocked == candidate {
			candidate++
		}
	}
	p.touched++
	if candidate <= p.h {
		return candidate, p.touched, true
	}
	return 0, p.touched, false
}

func (p *Pool) valid(hero int) bool {
	return hero >= 1 && hero <= p.h
}

func (p *Pool) add(hero int, record Record) error {
	if !p.valid(hero) {
		return ErrInvalidArgument
	}
	p.touched++
	if _, ok := p.records[hero]; ok {
		return ErrUnavailable
	}
	p.records[hero] = record
	index := sort.SearchInts(p.blocked, hero)
	p.blocked = append(p.blocked, 0)
	copy(p.blocked[index+1:], p.blocked[index:])
	p.blocked[index] = hero
	return nil
}
