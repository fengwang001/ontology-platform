package fib

import (
	"sort"
	"sync"

	"ontology/bucket"
	"ontology/nhgroup"
)

type Prefix struct {
	Address uint32
	Length  uint8
}

type FIB struct {
	mu         sync.Mutex
	controller *bucket.Controller
	routes     map[Prefix]uint32
}

func New(controller *bucket.Controller) *FIB {
	f := &FIB{controller: controller, routes: make(map[Prefix]uint32)}
	controller.SetInUseHook(func(gid uint32) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, group := range f.routes {
			if group == gid {
				return true
			}
		}
		return false
	})
	return f
}

func (f *FIB) AddRoute(prefix Prefix, gid uint32, now uint64) error {
	if err := validatePrefix(prefix); err != nil {
		return err
	}
	if now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	f.controller.Lock()
	defer f.controller.Unlock()
	f.mu.Lock()
	if !f.controller.GroupExistsLocked(gid) {
		f.mu.Unlock()
		return nhgroup.ErrNotFound
	}
	if _, ok := f.routes[prefix]; ok {
		f.mu.Unlock()
		return nhgroup.ErrAlreadyExists
	}
	if err := f.controller.PrepareLocked(now); err != nil {
		f.mu.Unlock()
		return err
	}
	f.routes[prefix] = gid
	f.mu.Unlock()
	return nil
}

func (f *FIB) DelRoute(prefix Prefix, now uint64) error {
	if err := validatePrefix(prefix); err != nil {
		return err
	}
	if now > 1_000_000_000_000 {
		return nhgroup.ErrInvalidArgument
	}
	f.controller.Lock()
	defer f.controller.Unlock()
	f.mu.Lock()
	if _, ok := f.routes[prefix]; !ok {
		f.mu.Unlock()
		return nhgroup.ErrNotFound
	}
	if err := f.controller.PrepareLocked(now); err != nil {
		f.mu.Unlock()
		return err
	}
	delete(f.routes, prefix)
	f.mu.Unlock()
	return nil
}

func (f *FIB) Route(dst, hash, now uint64) (uint32, error) {
	if dst > uint64(^uint32(0)) || now > 1_000_000_000_000 {
		return 0, nhgroup.ErrInvalidArgument
	}
	f.controller.Lock()
	defer f.controller.Unlock()
	if err := f.controller.PrepareLocked(now); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	prefixes := make([]Prefix, 0, len(f.routes))
	for prefix := range f.routes {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool {
		return prefixes[i].Length > prefixes[j].Length
	})
	dst32 := uint32(dst)
	sawCovering := false
	for _, prefix := range prefixes {
		if !contains(prefix, dst32) {
			continue
		}
		sawCovering = true
		gid := f.routes[prefix]
		if !f.controller.GroupAliveLocked(gid) {
			continue
		}
		return f.controller.LookupPreparedLocked(gid, hash, now)
	}
	if sawCovering {
		return 0, nhgroup.ErrNoNexthop
	}
	return 0, nhgroup.ErrNoRoute
}

func validatePrefix(prefix Prefix) error {
	if prefix.Length > 32 {
		return nhgroup.ErrInvalidArgument
	}
	mask := uint32(0xffffffff) << (32 - prefix.Length)
	if prefix.Length == 0 {
		mask = 0
	}
	if prefix.Address&^mask != 0 {
		return nhgroup.ErrInvalidArgument
	}
	return nil
}

func contains(prefix Prefix, dst uint32) bool {
	if prefix.Length == 0 {
		return true
	}
	mask := uint32(0xffffffff) << (32 - prefix.Length)
	return prefix.Address&mask == dst&mask
}
