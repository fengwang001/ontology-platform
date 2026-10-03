package cond

import "testing"

func strp(s string) *string { return &s }
func i64p(v int64) *int64   { return &v }

var (
	dataCur      = Current{Exists: true, Etag: "e1", Mtime: 100}
	tombstoneCur = Current{Exists: true, Tombstone: true}
	noCur        = Current{}
)

func TestCheck(t *testing.T) {
	cases := []struct {
		name       string
		c          Cond
		cur        Current
		wantFailed string
		wantOK     bool
	}{
		{"empty cond always passes", Cond{}, dataCur, "", true},
		{"empty cond on missing", Cond{}, noCur, "", true},

		{"IfMatch hit", Cond{IfMatch: strp("e1")}, dataCur, "", true},
		{"IfMatch mismatch", Cond{IfMatch: strp("e2")}, dataCur, IfMatchName, false},
		{"IfMatch missing current", Cond{IfMatch: strp("e1")}, noCur, IfMatchName, false},
		{"IfMatch tombstone current", Cond{IfMatch: strp("e1")}, tombstoneCur, IfMatchName, false},

		{"IfUnmodifiedSince newer s", Cond{IfUnmodifiedSince: i64p(200)}, dataCur, "", true},
		{"IfUnmodifiedSince equal passes", Cond{IfUnmodifiedSince: i64p(100)}, dataCur, "", true},
		{"IfUnmodifiedSince older s", Cond{IfUnmodifiedSince: i64p(99)}, dataCur, IfUnmodifiedSinceName, false},
		{"IfUnmodifiedSince missing current", Cond{IfUnmodifiedSince: i64p(100)}, noCur, IfUnmodifiedSinceName, false},
		{"IfUnmodifiedSince tombstone current", Cond{IfUnmodifiedSince: i64p(100)}, tombstoneCur, IfUnmodifiedSinceName, false},

		{"IfNoneMatch missing current", Cond{IfNoneMatchStar: true}, noCur, "", true},
		{"IfNoneMatch tombstone current", Cond{IfNoneMatchStar: true}, tombstoneCur, "", true},
		{"IfNoneMatch data current", Cond{IfNoneMatchStar: true}, dataCur, IfNoneMatchName, false},

		{"all three pass", Cond{IfMatch: strp("e1"), IfUnmodifiedSince: i64p(100), IfNoneMatchStar: false}, dataCur, "", true},
		{"order: all fail reports IfMatch", Cond{IfMatch: strp("e2"), IfUnmodifiedSince: i64p(1), IfNoneMatchStar: true}, dataCur, IfMatchName, false},
		{"order: IfMatch ok reports IfUnmodifiedSince", Cond{IfMatch: strp("e1"), IfUnmodifiedSince: i64p(1), IfNoneMatchStar: true}, dataCur, IfUnmodifiedSinceName, false},
		{"order: first two ok reports IfNoneMatch", Cond{IfMatch: strp("e1"), IfUnmodifiedSince: i64p(100), IfNoneMatchStar: true}, dataCur, IfNoneMatchName, false},
		{"order: tombstone reports IfMatch first", Cond{IfMatch: strp("e1"), IfUnmodifiedSince: i64p(100)}, tombstoneCur, IfMatchName, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failed, ok := tc.c.Check(tc.cur)
			if ok != tc.wantOK || failed != tc.wantFailed {
				t.Errorf("Check() = (%q, %v), want (%q, %v)", failed, ok, tc.wantFailed, tc.wantOK)
			}
		})
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		c    Cond
		want bool
	}{
		{"no s", Cond{}, true},
		{"s = 0", Cond{IfUnmodifiedSince: i64p(0)}, true},
		{"s = 1e12", Cond{IfUnmodifiedSince: i64p(MaxTime)}, true},
		{"s = -1", Cond{IfUnmodifiedSince: i64p(-1)}, false},
		{"s = 1e12+1", Cond{IfUnmodifiedSince: i64p(MaxTime + 1)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Valid(); got != tc.want {
				t.Errorf("Valid() = %v, want %v", got, tc.want)
			}
		})
	}
}
