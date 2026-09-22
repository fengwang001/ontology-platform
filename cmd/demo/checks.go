package main

import (
	"ontology/store"
	"ontology/txid"
)

func newStore() *store.Store { return store.New(txid.NewCounter(), store.Config{}) }

func commitKV(s *store.Store, key, val string) bool {
	tx, err := s.BeginTx()
	if err != nil {
		return false
	}
	if err := tx.Write(key, []byte(val)); err != nil {
		return false
	}
	return tx.Commit() == nil
}

func checkSnapshotIsolation() bool {
	s := newStore()
	if !commitKV(s, "k", "v1") {
		return false
	}
	snap, err := s.Begin()
	if err != nil {
		return false
	}
	defer s.Release(snap)
	if !commitKV(s, "k", "v2") {
		return false
	}
	for i := 0; i < 3; i++ {
		if got, lk := s.ReadAt(snap, "k"); lk != store.LookupFound || string(got) != "v1" {
			return false
		}
	}
	return true
}

func checkUncommitted() bool {
	s := newStore()
	tx, err := s.BeginTx()
	if err != nil {
		return false
	}
	if tx.Write("k", []byte("mine")) != nil {
		return false
	}
	if got, lk := tx.Read("k"); lk != store.LookupFound || string(got) != "mine" {
		return false
	}
	snap, _ := s.Begin()
	defer s.Release(snap)
	if _, lk := s.ReadAt(snap, "k"); lk != store.LookupNever {
		return false
	}
	return tx.Commit() == nil
}

func checkRollback() bool {
	s := newStore()
	tx, _ := s.BeginTx()
	if tx.Write("k", []byte("x")) != nil || tx.Rollback() != nil {
		return false
	}
	snap, _ := s.Begin()
	defer s.Release(snap)
	_, lk := s.ReadAt(snap, "k")
	return lk == store.LookupNever && s.Stats().TotalVersions == 0
}

func checkDeleteVsNever() bool {
	s := newStore()
	if !commitKV(s, "k", "v1") {
		return false
	}
	tx, _ := s.BeginTx()
	if tx.Delete("k") != nil || tx.Commit() != nil {
		return false
	}
	snap, _ := s.Begin()
	defer s.Release(snap)
	_, lkDel := s.ReadAt(snap, "k")
	_, lkNever := s.ReadAt(snap, "absent")
	return lkDel == store.LookupDeleted && lkNever == store.LookupNever && lkDel != lkNever
}

func checkBoundary() bool {
	s := newStore()
	if !commitKV(s, "k", "v1") {
		return false
	}
	snap, _ := s.Begin()
	defer s.Release(snap)
	tx, _ := s.BeginTx()
	if tx.ID() != snap.Point() {
		return false
	}
	if tx.Write("k", []byte("v2")) != nil || tx.Commit() != nil {
		return false
	}
	got, lk := s.ReadAt(snap, "k")
	return lk == store.LookupFound && string(got) == "v1"
}
