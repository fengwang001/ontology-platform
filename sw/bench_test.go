package sw

import (
	"fmt"
	"testing"
	"time"
)

func registerMany(tb testing.TB, c *Coordinator, n int) {
	tb.Helper()
	for i := 0; i < n; i++ {
		if _, err := c.Register(fmt.Sprintf("/s%06d/", i), "sw.js", "x", at(int64(i)+1)); err != nil {
			tb.Fatalf("register %d: %v", i, err)
		}
	}
}

func TestMatchStepsIndependentOfRegistrationCount(t *testing.T) {
	var steps []int
	for _, n := range []int{10, 1000, 100000} {
		c := NewCoordinator(Config{})
		registerMany(t, c, n)
		if err := c.Navigate("c1", "/s000005/deep/page", at(int64(n)+1)); err != nil {
			t.Fatalf("navigate: %v", err)
		}
		steps = append(steps, c.LastMatchSteps())
		t.Logf("registrations=%d match steps=%d", n, c.LastMatchSteps())
	}
	if steps[0] != steps[1] || steps[1] != steps[2] {
		t.Fatalf("match steps %v grow with registration count", steps)
	}
	t.Log("basis: trie lookup walks only the URL's own path segments, never the registration set")
}

func TestManifestProbesIndependentOfManifestSize(t *testing.T) {
	for _, size := range []int{1, 1000, 100000} {
		c := NewCoordinator(Config{})
		if _, err := c.Register("/", "sw.js", "v1", at(1)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.CheckUpdate("/", "v1", at(2)); err != nil {
			t.Fatal(err)
		}
		if err := c.InstallSucceeded("/", 1, InstallOpts{}, at(3)); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < size; i++ {
			if err := c.PutManifest("/", 1, fmt.Sprintf("/res/%d", i), "d", at(4)); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Navigate("c1", "/page", at(5)); err != nil {
			t.Fatal(err)
		}
		ans, err := c.Request("c1", fmt.Sprintf("/res/%d", size-1))
		if err != nil || ans.Source != SourceCache {
			t.Fatalf("size=%d: request = %+v, %v", size, ans, err)
		}
		if probes := c.LastManifestProbes(); probes != 1 {
			t.Fatalf("size=%d: manifest probes = %d, want exactly 1", size, probes)
		}
		t.Logf("manifest size=%d probes=%d", size, c.LastManifestProbes())
	}
	t.Log("basis: manifest is a hash map; every request performs exactly one lookup")
}

func benchmarkNavigate(b *testing.B, registrations int) {
	c := NewCoordinator(Config{})
	registerMany(b, c, registrations)
	now := time.Unix(1<<40, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := c.Navigate("c1", "/s000005/deep/page", now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNavigate100Registrations(b *testing.B)    { benchmarkNavigate(b, 100) }
func BenchmarkNavigate100000Registrations(b *testing.B) { benchmarkNavigate(b, 100000) }

func benchmarkRequest(b *testing.B, manifestSize int) {
	c := NewCoordinator(Config{})
	now := time.Unix(1<<40, 0)
	if _, err := c.Register("/", "sw.js", "v1", now); err != nil {
		b.Fatal(err)
	}
	if _, err := c.CheckUpdate("/", "v1", now); err != nil {
		b.Fatal(err)
	}
	if err := c.InstallSucceeded("/", 1, InstallOpts{}, now); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < manifestSize; i++ {
		if err := c.PutManifest("/", 1, fmt.Sprintf("/res/%d", i), "d", now); err != nil {
			b.Fatal(err)
		}
	}
	if err := c.Navigate("c1", "/page", now); err != nil {
		b.Fatal(err)
	}
	target := fmt.Sprintf("/res/%d", manifestSize-1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Request("c1", target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRequestManifest16(b *testing.B)    { benchmarkRequest(b, 16) }
func BenchmarkRequestManifest65536(b *testing.B) { benchmarkRequest(b, 65536) }
