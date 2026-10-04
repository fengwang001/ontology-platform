package room

import (
	"errors"
	"sort"
)

var (
	ErrEmptyID = errors.New("room id is empty")
	ErrExists  = errors.New("room already exists")
	ErrBadTurn = errors.New("room turn must be in [0,240]")
)

type Room struct {
	ID   string
	Turn int64
}

type Registry struct {
	rooms map[string]Room
}

func NewRegistry() *Registry {
	return &Registry{rooms: map[string]Room{}}
}

func (r *Registry) Add(id string, turn int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if turn < 0 || turn > 240 {
		return ErrBadTurn
	}
	if _, ok := r.rooms[id]; ok {
		return ErrExists
	}
	r.rooms[id] = Room{ID: id, Turn: turn}
	return nil
}

func (r *Registry) Get(id string) (Room, bool) {
	got, ok := r.rooms[id]
	return got, ok
}

func (r *Registry) All() []Room {
	all := make([]Room, 0, len(r.rooms))
	for _, got := range r.rooms {
		all = append(all, got)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

func Conflicts(startA, endA int64, startB, endB int64, turn int64) bool {
	return startA < endB+turn && startB < endA+turn
}
