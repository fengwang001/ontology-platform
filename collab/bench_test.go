package collab

import "testing"

// BenchmarkSyncSmallVsLargeDoc shows Sync cost for a fixed-size batch does
// not grow with the number of schema fields or stored documents.
func BenchmarkSyncBatchSize(b *testing.B) {
	for _, fields := range []int{10, 1000, 10000} {
		schema := map[string]Kind{}
		for i := 0; i < fields; i++ {
			if i%2 == 0 {
				schema["f"+itoa(i)] = KindSet
			} else {
				schema["f"+itoa(i)] = KindAdd
			}
		}
		s := NewServer()
		s.Create("d", schema)
		// also create many unrelated documents
		for i := 0; i < 1000; i++ {
			s.Create("other"+itoa(i), map[string]Kind{"x": KindAdd})
		}
		makeOps := func(base int64, n int) []Op {
			ops := make([]Op, n)
			for i := 0; i < n; i++ {
				f := "f" + itoa((i*2)%fields) // overwrite field, unique per group
				ops[i] = setOp(base+int64(i), f, int64(i), 0, "g"+itoa(i))
			}
			return ops
		}
		b.Run("fields="+itoa(fields), func(b *testing.B) {
			// pre-seed history so replay lookups hit a populated ring
			for i := 0; i < 2000; i++ {
				s.Sync("h", "d", makeOps(int64(i*10)+1, 10), int64(i))
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				client := "bench"
				base := int64(i*50) + 1
				ops := makeOps(base, 50)
				if _, err := s.Sync(client, "d", ops, int64(100000+i)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkReplayLookup verifies replay from a full 1000-slot ring stays
// constant time regardless of total client history.
func BenchmarkReplayWindow(b *testing.B) {
	s := NewServer()
	s.Create("d", map[string]Kind{"f0": KindSet})
	client := "c"
	// submit far more than RetainWindow ops
	total := RetainWindow * 10
	for seq := int64(1); seq <= int64(total); seq++ {
		if _, err := s.Sync(client, "d",
			[]Op{setOp(seq, "f0", seq, 0, "g")}, seq); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		seq := int64(total - (i % RetainWindow))
		if _, err := s.Sync(client, "d",
			[]Op{setOp(seq, "f0", seq, 0, "g")}, int64(total+i)); err != nil {
			b.Fatal(err)
		}
	}
}
