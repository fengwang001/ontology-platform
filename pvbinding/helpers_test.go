package pvbinding

import (
	"fmt"
	"sort"
	"testing"
)

func am(modes ...AccessMode) map[AccessMode]bool {
	m := make(map[AccessMode]bool, len(modes))
	for _, k := range modes {
		m[k] = true
	}
	return m
}

func ss(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, k := range items {
		m[k] = true
	}
	return m
}

func labels(items ...string) map[string]string {
	if len(items)%2 != 0 {
		panic("labels must be key/value pairs")
	}
	m := make(map[string]string, len(items)/2)
	for i := 0; i < len(items); i += 2 {
		m[items[i]] = items[i+1]
	}
	return m
}

func volSpec(cap int64, sc string, modes map[AccessMode]bool) VolumeSpec {
	return VolumeSpec{
		Capacity:     cap,
		StorageClass: sc,
		AccessModes:  modes,
		Reclaim:      ReclaimRetain,
	}
}

func claimSpec(cap int64, sc string, modes map[AccessMode]bool) ClaimSpec {
	return ClaimSpec{
		RequestCapacity: cap,
		StorageClass:    sc,
		AccessModes:     modes,
		BindMode:        BindImmediate,
	}
}

func assertInvariants(t *testing.T, c *Controller) {
	t.Helper()
	if err := c.CheckInvariants(); err != nil {
		t.Fatalf("invariants violated: %v\n%s", err, dumpSnapshot(c.Snapshot()))
	}
}

func assertNoErr(t *testing.T, c *Controller, err error, op string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s returned unexpected error %v\n%s", op, err, dumpSnapshot(c.Snapshot()))
	}
}

func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	if got := KindOf(err); got != want {
		t.Fatalf("error kind = %q, want %q (err=%v)", got, want, err)
	}
}

func assertBound(t *testing.T, c *Controller, claimName, wantVolume string) {
	t.Helper()
	snap := c.Snapshot()
	cl := snap.Claims[claimName]
	if cl == nil {
		t.Fatalf("claim %s missing\n%s", claimName, dumpSnapshot(snap))
	}
	if !cl.Bound || cl.VolumeName != wantVolume {
		t.Fatalf("claim %s bound=%v volume=%q, want volume %q\n%s",
			claimName, cl.Bound, cl.VolumeName, wantVolume, dumpSnapshot(snap))
	}
	v := snap.Volumes[wantVolume]
	if v == nil || v.Phase != VolumeBound || v.ClaimName != claimName {
		t.Fatalf("volume %s not consistently bound to %s\n%s", wantVolume, claimName, dumpSnapshot(snap))
	}
}

func assertPending(t *testing.T, c *Controller, claimName string) {
	t.Helper()
	snap := c.Snapshot()
	cl := snap.Claims[claimName]
	if cl == nil {
		t.Fatalf("claim %s missing\n%s", claimName, dumpSnapshot(snap))
	}
	if cl.Bound {
		t.Fatalf("claim %s unexpectedly bound to %s\n%s",
			claimName, cl.VolumeName, dumpSnapshot(snap))
	}
}

func assertVolumePhase(t *testing.T, c *Controller, name string, want VolumePhase, exists bool) {
	t.Helper()
	snap := c.Snapshot()
	v := snap.Volumes[name]
	if !exists {
		if v != nil {
			t.Fatalf("volume %s should be removed, still present\n%s", name, dumpSnapshot(snap))
		}
		return
	}
	if v == nil {
		t.Fatalf("volume %s missing\n%s", name, dumpSnapshot(snap))
	}
	if v.Phase != want {
		t.Fatalf("volume %s phase=%s want %s\n%s", name, v.Phase, want, dumpSnapshot(snap))
	}
}

func dumpSnapshot(s Snapshot) string {
	out := "\nvolumes:\n"
	vn := make([]string, 0, len(s.Volumes))
	for n := range s.Volumes {
		vn = append(vn, n)
	}
	sort.Strings(vn)
	for _, n := range vn {
		v := s.Volumes[n]
		out += fmt.Sprintf("  %s phase=%s cap=%d sc=%s reserved=%q claim=%q\n",
			v.Name, v.Phase, v.Spec.Capacity, v.Spec.StorageClass, v.Spec.ReservedClaim, v.ClaimName)
	}
	out += "claims:\n"
	cn := make([]string, 0, len(s.Claims))
	for n := range s.Claims {
		cn = append(cn, n)
	}
	sort.Strings(cn)
	for _, n := range cn {
		cl := s.Claims[n]
		state := "pending"
		if cl.Bound {
			state = "bound:" + cl.VolumeName
		}
		out += fmt.Sprintf("  %s %s req=%d sc=%s mode=%s vol=%q\n",
			cl.Name, state, cl.Spec.RequestCapacity, cl.Spec.StorageClass, cl.Spec.BindMode, cl.Spec.VolumeName)
	}
	return out
}
