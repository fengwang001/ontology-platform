package main

import (
	"errors"
	"time"

	"ontology/store"
	"ontology/txid"
)

func checkCrashPoints() bool {
	keys := []string{"k1", "k2"}
	var points []store.CrashPoint
	s0 := newStore()
	s0.SetCrashHook(func(p store.CrashPoint) bool {
		points = append(points, p)
		return false
	})
	tx0, _ := s0.BeginTx()
	for _, k := range keys {
		_ = tx0.Write(k, []byte("v-"+k))
	}
	if tx0.Commit() != nil || len(points) == 0 {
		return false
	}
	for _, p := range points {
		s := newStore()
		s.SetCrashHook(func(cp store.CrashPoint) bool { return cp == p })
		tx, _ := s.BeginTx()
		for _, k := range keys {
			_ = tx.Write(k, []byte("v-"+k))
		}
		_ = tx.Commit()
		s.Recover()
		snap, _ := s.Begin()
		committed := p.Stage == store.StageAfterMark
		for _, k := range keys {
			_, lk := s.ReadAt(snap, k)
			if committed && lk != store.LookupFound {
				return false
			}
			if !committed && lk != store.LookupNever {
				return false
			}
		}
		s.Release(snap)
	}
	return true
}

func checkLimits() bool {
	s1 := store.New(txid.NewCounter(), store.Config{MaxChainLen: 1})
	commitKV(s1, "k", "v1")
	tx1, _ := s1.BeginTx()
	_ = tx1.Write("k", []byte("v2"))
	err1 := tx1.Commit()
	s2 := store.New(txid.NewCounter(), store.Config{MaxVersions: 1})
	commitKV(s2, "a", "1")
	tx2, _ := s2.BeginTx()
	_ = tx2.Write("b", []byte("2"))
	err2 := tx2.Commit()
	s3 := store.New(txid.NewCounter(), store.Config{MaxSnapshots: 1})
	snap, _ := s3.Begin()
	defer s3.Release(snap)
	_, err3 := s3.Begin()
	return errors.Is(err1, store.ErrChainLenExceeded) &&
		errors.Is(err2, store.ErrVersionLimit) &&
		errors.Is(err3, store.ErrSnapshotLimit) &&
		!errors.Is(err1, err2) && !errors.Is(err2, err3) && !errors.Is(err1, err3)
}

func checkStatsStable() bool {
	s := newStore()
	commitKV(s, "k", "v1")
	a, b := s.Stats(), s.Stats()
	k1, k2 := s.KeyVersions("k"), s.KeyVersions("k")
	return a == b && k1 == k2 && s.KeyVersions("absent") == 0
}

func checkLongTx() bool {
	s := newStore()
	long, _ := s.BeginTx()
	_ = long.Write("keyA", []byte("held"))
	defer long.Rollback()
	done := make(chan bool, 1)
	go func() {
		tx, err := s.BeginTx()
		if err != nil {
			done <- false
			return
		}
		_ = tx.Write("keyB", []byte("ok"))
		done <- tx.Commit() == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(5 * time.Second):
		return false
	}
}
