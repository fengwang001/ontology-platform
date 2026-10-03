package activator

import (
	"errors"
	"testing"

	"ontology/config"
)

func pushC(names ...string) config.PushInput {
	var in config.PushInput
	for _, n := range names {
		in.Clusters = append(in.Clusters, config.Cluster{Name: n})
	}
	return in
}

func withRoute(in config.PushInput, name string, refs ...string) config.PushInput {
	in.Routes = append(in.Routes, config.Route{Name: name, Clusters: refs})
	return in
}

// TestSpecExampleW10 复现题面 W=10 示例的三条结局路径。
func TestSpecExampleW10(t *testing.T) {
	setup := func(t *testing.T) *Activator {
		a := New(10)
		if err := a.Push(1, withRoute(pushC("c1"), "r1", "c1"), 0); err != nil {
			t.Fatalf("push1: %v", err)
		}
		if v, ok, _ := a.Serving("r1", 0); ok || v != 0 {
			t.Fatalf("r1 should not serve before ready: v=%d ok=%v", v, ok)
		}
		if err := a.Ready("c1", 4); err != nil {
			t.Fatalf("ready c1: %v", err)
		}
		if v, ok, _ := a.Serving("r1", 4); !ok || v != 1 {
			t.Fatalf("r1 should serve v1: v=%d ok=%v", v, ok)
		}
		if err := a.Push(2, withRoute(pushC("c1", "c2"), "r1", "c1", "c2"), 5); err != nil {
			t.Fatalf("push2: %v", err)
		}
		if v, ok, _ := a.Serving("r1", 5); !ok || v != 1 {
			t.Fatalf("r1 must keep v1 during warming: v=%d ok=%v", v, ok)
		}
		return a
	}

	t.Run("path-A-both-ready-then-delete-in-use", func(t *testing.T) {
		a := setup(t)
		if err := a.Ready("c2", 7); err != nil {
			t.Fatal(err)
		}
		if v, ok, _ := a.Serving("r1", 7); !ok || v != 1 {
			t.Fatalf("partial readiness must not switch: v=%d ok=%v", v, ok)
		}
		if err := a.Ready("c1", 9); err != nil {
			t.Fatal(err)
		}
		if v, ok, _ := a.Serving("r1", 9); !ok || v != 2 {
			t.Fatalf("r1 should serve v2: v=%d ok=%v", v, ok)
		}
		err := a.Push(3, config.PushInput{DeleteClusters: []string{"c1"}}, 10)
		if !errors.Is(err, config.ErrInUse) {
			t.Fatalf("want ErrInUse, got %v", err)
		}
	})

	t.Run("path-B-update-timeout-activates-v2-on-old-serving", func(t *testing.T) {
		a := setup(t)
		if err := a.Ready("c2", 7); err != nil {
			t.Fatal(err)
		}
		if v, ok, _ := a.Serving("r1", 15); !ok || v != 2 {
			t.Fatalf("r1 should activate v2 after c1 timeout: v=%d ok=%v", v, ok)
		}
		st, exists, _ := a.State("c1", 15)
		if !exists || st.ServingVer != 1 || st.WarmingVer != 0 {
			t.Fatalf("c1 keeps old serving v1, warming dropped: %+v %v", st, exists)
		}
	})

	t.Run("path-C-new-timeout-removes-cluster-and-drops-pending", func(t *testing.T) {
		a := setup(t)
		if err := a.Ready("c1", 9); err != nil {
			t.Fatal(err)
		}
		if _, exists, _ := a.State("c2", 15); exists {
			t.Fatalf("c2 must be removed after failed first warming")
		}
		if v, ok, _ := a.Serving("r1", 15); !ok || v != 1 {
			t.Fatalf("r1 must remain v1: v=%d ok=%v", v, ok)
		}
	})
}
