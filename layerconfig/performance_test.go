package layerconfig_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"ontology/layerconfig"
)

// This file verifies the performance/storage claims in a reproducible way:
//
//  1. Resolve latency must not grow with the total number of stored keys.
//  2. Resolve latency must not grow with the number of historical versions.
//  3. A publication that changes one (scope,key) must not copy the whole
//     configuration: most pmap shards remain shared (same backing map)
//     between adjacent versions.
//
// The shard-sharing check uses an internal whitebox helper exposed via the
// perfProbe type defined in the main package (test-only export).

type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

func buildManyKeys(b fataler, s *layerconfig.Store, n int) {
	b.Helper()
	changes := make([]layerconfig.Change, 0, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("perfkey%05d", i)
		if err := s.RegisterKey(layerconfig.Schema{
			Key: key, Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride,
		}); err != nil {
			b.Fatalf("register: %v", err)
		}
		changes = append(changes, layerconfig.Change{
			Op:    layerconfig.OpWrite,
			Scope: layerconfig.Scope{},
			Key:   key,
			Value: layerconfig.Value{Str: "v"},
		})
	}
	if _, err := s.Publish(changes); err != nil {
		b.Fatalf("publish: %v", err)
	}
}

// BenchmarkResolve_Keys1k vs _Keys64k demonstrates resolution is independent
// of total key count.
func BenchmarkResolve_Keys1k(b *testing.B)  { benchmarkResolve(b, 1000) }
func BenchmarkResolve_Keys16k(b *testing.B) { benchmarkResolve(b, 16000) }
func BenchmarkResolve_Keys64k(b *testing.B) { benchmarkResolve(b, 64000) }

func benchmarkResolve(b *testing.B, nKeys int) {
	s := layerconfig.NewStore()
	buildManyKeys(b, s, nKeys)
	target := layerconfig.Scope{}
	key := fmt.Sprintf("perfkey%05d", nKeys-1)
	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := s.Resolve(-1, target, key)
		if err != nil || !res.Present {
			b.Fatalf("resolve: %v present=%v", err, res.Present)
		}
	}
}

// TestResolutionIndependentOfVersions builds a long history by changing one
// key repeatedly and asserts resolve latency at the newest version is
// effectively identical to latency at a small-history store. It uses a ratio
// bound that is deliberately loose (5x) to stay robust on shared CI hardware
// while still proving no version-count dependence in the algorithm.
func TestResolutionIndependentOfVersions(t *testing.T) {
	l := newLogger(t)
	defer l.finish()

	measure := func(nVersions int) float64 {
		s := layerconfig.NewStore()
		if err := s.RegisterKey(layerconfig.Schema{
			Key: "moving", Type: layerconfig.TypeInt, Min: -1_000_000, Max: 1_000_000, Merge: layerconfig.MergeOverride,
		}); err != nil {
			t.Fatal(err)
		}
		for v := 1; v <= nVersions; v++ {
			if _, err := s.Publish([]layerconfig.Change{{
				Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "moving",
				Value: layerconfig.Value{Int: v},
			}}); err != nil {
				t.Fatal(err)
			}
		}
		// Warm up.
		for j := 0; j < 1000; j++ {
			if _, err := s.Resolve(-1, layerconfig.Scope{}, "moving"); err != nil {
				t.Fatal(err)
			}
		}
		const iters = 200000
		startT := time.Now()
		var sink int64
		for j := 0; j < iters; j++ {
			r, err := s.Resolve(-1, layerconfig.Scope{}, "moving")
			if err != nil || !r.Present {
				t.Fatal("bad resolve")
			}
			sink += int64(r.Value.Int)
		}
		elapsed := float64(time.Since(startT).Nanoseconds()) / float64(iters)
		_ = sink
		return elapsed
	}

	small := measure(10)
	big := measure(2000)
	l.line("resolve ns/op: small-history=%.2f  2000-version-history=%.2f  ratio=%.2fx (must be <= 5x)",
		small, big, big/small)
	if big > 5*small {
		t.Fatalf("resolve latency grows with versions: %.2f vs %.2f ns/op", big, small)
	}
}

// TestStructuralSharingNoFullCopy verifies a single-key publish does not copy
// the whole configuration: with 64 shards, changing one key may rebuild only
// the affected shard map; the other 63 shard backing maps must be shared by
// identity between adjacent versions.
func TestStructuralSharingNoFullCopy(t *testing.T) {
	l := newLogger(t)
	defer l.finish()
	s := layerconfig.NewStore()
	if err := s.RegisterKey(layerconfig.Schema{
		Key: "probe", Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride,
	}); err != nil {
		t.Fatal(err)
	}
	// Populate many keys across multiple shards.
	var seed []layerconfig.Change
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("share%04d", i)
		if err := s.RegisterKey(layerconfig.Schema{
			Key: key, Type: layerconfig.TypeString, Merge: layerconfig.MergeOverride,
		}); err != nil {
			t.Fatal(err)
		}
		seed = append(seed, layerconfig.Change{
			Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: key,
			Value: layerconfig.Value{Str: "x"},
		})
	}
	if _, err := s.Publish(seed); err != nil {
		t.Fatal(err)
	}
	before := layerconfig.SharedWriteShards(s)
	if _, err := s.Publish([]layerconfig.Change{{
		Op: layerconfig.OpWrite, Scope: layerconfig.Scope{}, Key: "share0001",
		Value: layerconfig.Value{Str: "y"},
	}}); err != nil {
		t.Fatal(err)
	}
	after := layerconfig.SharedWriteShards(s)
	shared := after - before
	l.line("write pmap shards shared (same backing map identity) between adjacent versions: %d/64",
		shared)
	if shared < 60 {
		t.Fatalf("expected >=60 of 64 shards to remain shared after one-key publish, got %d", shared)
	}

	// Also assert history does not duplicate entries: version count grows by
	// one but the number of distinct shard maps stays bounded by the number
	// of touched shards across history rather than versions*64.
	totalShardMaps := layerconfig.TotalWriteShardMaps(s)
	versions := s.CurrentVersion()
	l.line("distinct write-shard maps across all retained versions: %d (versions=%d; full-copy upper bound=%d)",
		totalShardMaps, versions, versions*64)
	if totalShardMaps >= versions*64 {
		t.Fatal("history appears to copy full configuration per version")
	}
}

var _ = runtime.GC
