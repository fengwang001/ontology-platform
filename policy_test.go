package ontology

import "testing"

func TestDropNewestPolicy(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 2, Full: FullDropNewest,
	})
	for i := 0; i < 5; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	got := tryRecvSeqs(sub)
	if !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("drop-newest received %v, want [1 2]", got)
	}
	dropped, last := sub.Dropped()
	if dropped != 3 || last != 5 {
		t.Fatalf("dropped=%d last=%d, want 3/5", dropped, last)
	}
}

func TestDropOldestPolicy(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{
		Prefix: "e", BufferSize: 2, Full: FullDropOldest,
	})
	for i := 0; i < 5; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	got := tryRecvSeqs(sub)
	if !equalSeqs(got, []uint64{4, 5}) {
		t.Fatalf("drop-oldest received %v, want [4 5]", got)
	}
	dropped, last := sub.Dropped()
	if dropped != 3 || last != 3 {
		t.Fatalf("dropped=%d last=%d, want 3/3", dropped, last)
	}
}

func TestDisconnectPolicy(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	sub := mustSubscribe(t, d, SubscribeOptions{
		ID: "lagging", Prefix: "e", BufferSize: 1, Full: FullDisconnect,
	})
	for i := 0; i < 3; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	got := tryRecvSeqs(sub)
	if !equalSeqs(got, []uint64{1}) {
		t.Fatalf("disconnect received %v, want [1]", got)
	}
	dropped, last := sub.Dropped()
	if dropped != 1 || last != 2 {
		t.Fatalf("dropped=%d last=%d, want 1/2", dropped, last)
	}
	if ids := d.Match("e1", "p"); len(ids) != 0 {
		t.Fatalf("disconnected subscription still matched: %v", ids)
	}
	mustPublish(t, d, "e1", "p", 99)
	if m, ok := sub.TryReceive(); ok {
		t.Fatalf("disconnected subscription received %+v", m)
	}
}

func TestPoliciesAreIndependent(t *testing.T) {
	d := NewDispatcher()
	defer d.Close()
	oldest := mustSubscribe(t, d, SubscribeOptions{
		ID: "oldest", Prefix: "e", BufferSize: 2, Full: FullDropOldest,
	})
	newest := mustSubscribe(t, d, SubscribeOptions{
		ID: "newest", Prefix: "e", BufferSize: 2, Full: FullDropNewest,
	})
	disc := mustSubscribe(t, d, SubscribeOptions{
		ID: "disc", Prefix: "e", BufferSize: 1, Full: FullDisconnect,
	})
	full := mustSubscribe(t, d, SubscribeOptions{
		ID: "full", Prefix: "e", BufferSize: 8,
	})
	for i := 0; i < 5; i++ {
		mustPublish(t, d, "e1", "p", i)
	}
	if got := tryRecvSeqs(oldest); !equalSeqs(got, []uint64{4, 5}) {
		t.Fatalf("oldest got %v", got)
	}
	if got := tryRecvSeqs(newest); !equalSeqs(got, []uint64{1, 2}) {
		t.Fatalf("newest got %v", got)
	}
	if got := tryRecvSeqs(disc); !equalSeqs(got, []uint64{1}) {
		t.Fatalf("disc got %v", got)
	}
	if got := tryRecvSeqs(full); !equalSeqs(got, []uint64{1, 2, 3, 4, 5}) {
		t.Fatalf("unaffected subscriber got %v, want all five", got)
	}
	if dropped, _ := full.Dropped(); dropped != 0 {
		t.Fatalf("unaffected subscriber dropped %d", dropped)
	}
}
