package presence

import "testing"

func TestSkeleton(t *testing.T) {
	svc, err := New(Config{LeaseSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if svc == nil {
		t.Fatal("nil service")
	}
	if _, err := New(Config{LeaseSeconds: 0}); err != ErrInvalidArgument {
		t.Fatalf("got %v", err)
	}
}
