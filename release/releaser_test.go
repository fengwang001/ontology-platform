package release

import (
	"strings"
	"sync"
	"testing"
)

func mustTag(t *testing.T, r *Releaser, vs ...string) {
	t.Helper()
	for _, s := range vs {
		if err := r.Tag(s); err != nil {
			t.Fatalf("Tag(%q) unexpected error: %v", s, err)
		}
	}
}

func TestChannelsIndependentN(t *testing.T) {
	r := New()
	mustTag(t, r, "1.0.0", "2.0.0-rc.2", "2.0.0-beta.5")
	for _, tc := range []struct {
		ch, want string
	}{
		{"rc", "2.0.0-rc.3"},
		{"beta", "2.0.0-beta.6"},
		{"alpha", "2.0.0-alpha.1"},
	} {
		v, err := r.Next([]Commit{{Type: "feat"}}, tc.ch)
		if err != nil || v.String() != tc.want {
			t.Fatalf("channel %s -> %v, %v, want %s", tc.ch, v, err, tc.want)
		}
	}
}

func TestBaselineIsMaxStableNotLast(t *testing.T) {
	r := New()
	mustTag(t, r, "1.2.0", "1.10.0", "1.5.3")
	v, err := r.Next([]Commit{{Type: "fix"}}, "")
	if err != nil || v.String() != "1.10.1" {
		t.Fatalf("baseline = %v, %v", v, err)
	}
}

func TestPrereleaseLineMonotonic(t *testing.T) {
	r := New()
	mustTag(t, r, "1.0.0", "3.0.0-rc.1", "2.0.0-rc.9", "2.5.0-beta.2")
	v, err := r.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil || v.String() != "3.0.0-rc.2" {
		t.Fatalf("line max core = %v, %v", v, err)
	}
	r2 := New()
	mustTag(t, r2, "2.0.0", "1.9.0-rc.1")
	v, err = r2.Next([]Commit{{Type: "fix"}}, "")
	if err != nil || v.String() != "2.0.1" {
		t.Fatalf("old prerelease ignored = %v, %v", v, err)
	}
	mustTag(t, r, "3.0.0")
	v, err = r.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil || v.String() != "3.0.1-rc.1" {
		t.Fatalf("line reset after stable = %v, %v", v, err)
	}
}

func TestOverflowAtBoundary(t *testing.T) {
	r := New()
	mustTag(t, r, "999999.999999.999999")
	if _, err := r.Next([]Commit{{Type: "fix"}}, ""); reasonOf(err) != ErrOverflow {
		t.Fatalf("patch overflow = %v", err)
	}
	r2 := New()
	mustTag(t, r2, "999999.999999.0")
	if _, err := r2.Next([]Commit{{Type: "feat"}}, ""); reasonOf(err) != ErrOverflow {
		t.Fatalf("minor overflow = %v", err)
	}
	r3 := New()
	mustTag(t, r3, "999999.0.0")
	if _, err := r3.Next([]Commit{{Type: "x", Breaking: true}}, ""); reasonOf(err) != ErrOverflow {
		t.Fatalf("major overflow = %v", err)
	}
	r4 := New()
	mustTag(t, r4, "1.0.0-rc.999999")
	if _, err := r4.Next([]Commit{{Type: "feat"}}, "rc"); reasonOf(err) != ErrOverflow {
		t.Fatalf("N overflow = %v", err)
	}
	// T 自身超限，但更大的 P 未超限：K 取 P，不报溢出。
	r5 := New()
	mustTag(t, r5, "1.0.999999", "1.1.0-rc.1")
	v, err := r5.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil || v.String() != "1.1.0-rc.2" {
		t.Fatalf("bigger P should mask T overflow: %v, %v", v, err)
	}
}

func TestErrorOrder(t *testing.T) {
	r := New()
	if _, err := r.Next([]Commit{{Type: "BAD"}}, "RC"); reasonOf(err) != ErrInvalidChannel {
		t.Fatalf("want invalid channel first, got %v", err)
	}
	if _, err := r.Next([]Commit{{Type: "docs"}, {Type: "2"}, {Type: ""}}, ""); reasonOf(err) != ErrInvalidCommitType {
		t.Fatalf("want invalid commit type, got %v", err)
	}
	if _, err := r.Next(nil, ""); reasonOf(err) != ErrNoRelease {
		t.Fatalf("want no release, got %v", err)
	}
	if err := r.Tag("01.0.0"); reasonOf(err) != ErrInvalidVersion {
		t.Fatalf("tag invalid = %v", err)
	}
}

func TestRejectionDoesNotMutate(t *testing.T) {
	r := New()
	mustTag(t, r, "1.0.0", "1.0.0-rc.1")
	before := versionsStr(r)
	var wg sync.WaitGroup
	try := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fn()
		}()
	}
	for i := 0; i < 20; i++ {
		try(func() error { return r.Tag("1.0.0") })
		try(func() error { return r.Tag("bad") })
		try(func() error { _, _ = r.Next(nil, ""); return nil })
		try(func() error {
			_, err := r.Release(nil, "")
			return err
		})
	}
	wg.Wait()
	if got := versionsStr(r); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("published set changed after rejections: %v", got)
	}
}

func TestConcurrentReleasesDistinct(t *testing.T) {
	r := New()
	mustTag(t, r, "1.0.0")
	const n = 64
	var wg sync.WaitGroup
	results := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := r.Release([]Commit{{Type: "fix"}}, "rc")
			if err != nil {
				t.Errorf("Release: %v", err)
				return
			}
			results <- v.String()
		}()
	}
	wg.Wait()
	close(results)

	seen := map[string]int{}
	for s := range results {
		seen[s]++
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct of %d: %v", len(seen), n, seen)
	}
	for s, count := range seen {
		if count != 1 {
			t.Fatalf("duplicate result %s x%d", s, count)
		}
	}
	if len(r.Versions()) != 1+n {
		t.Fatalf("published set size = %d, want %d", len(r.Versions()), 1+n)
	}
	for i := 1; i <= n; i++ {
		want := "1.0.1-rc." + itoa(i)
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing %s in results", want)
		}
	}
}

func TestReplayDeterminism(t *testing.T) {
	ops := []struct {
		kind    int // 0 tag, 1 release
		arg     string
		commits []Commit
		channel string
	}{
		{kind: 0, arg: "1.2.3"},
		{kind: 0, arg: "2.0.0-rc.1"},
		{kind: 1, commits: []Commit{{Type: "fix"}}, channel: "rc"},
		{kind: 1, commits: []Commit{{Type: "feat"}}, channel: "rc"},
		{kind: 1, commits: []Commit{{Type: "docs"}}, channel: ""},
	}
	run := func() []string {
		r := New()
		out := []string{}
		for _, op := range ops {
			switch op.kind {
			case 0:
				if err := r.Tag(op.arg); err != nil {
					out = append(out, "err:"+string(reasonOf(err)))
				} else {
					out = append(out, "ok:"+op.arg)
				}
			case 1:
				v, err := r.Release(op.commits, op.channel)
				if err != nil {
					out = append(out, "err:"+string(reasonOf(err)))
				} else {
					out = append(out, "ok:"+v.String())
				}
			}
		}
		return out
	}
	first := run()
	for i := 0; i < 10; i++ {
		if got := run(); strings.Join(got, "|") != strings.Join(first, "|") {
			t.Fatalf("replay mismatch:\n%v\n%v", first, got)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestExampleRcBumpAndStable(t *testing.T) {
	// S=1.2.3、已发布 2.0.0-rc.1，提交只有 fix。
	r := New()
	mustTag(t, r, "1.2.3", "2.0.0-rc.1")

	v, err := r.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil || v.String() != "2.0.0-rc.2" {
		t.Fatalf("Next rc = %v, %v", v, err)
	}
	v, err = r.Next([]Commit{{Type: "fix"}}, "")
	if err != nil || v.String() != "2.0.0" {
		t.Fatalf("Next stable = %v, %v", v, err)
	}
	if len(r.Versions()) != 2 {
		t.Fatalf("Next mutated published set: %v", r.Versions())
	}

	v, err = r.Release([]Commit{{Type: "fix"}}, "rc")
	if err != nil || v.String() != "2.0.0-rc.2" {
		t.Fatalf("Release rc = %v, %v", v, err)
	}
	v, err = r.Release([]Commit{{Type: "docs"}}, "")
	if err != nil || v.String() != "2.0.0" {
		t.Fatalf("Release stable = %v, %v", v, err)
	}
}

func TestZeroXDegradation(t *testing.T) {
	// S=0.4.0，feat 与 breaking 同时存在：breaking 降为 Minor，得 0.5.0。
	r := New()
	mustTag(t, r, "0.4.0")
	v, err := r.Next([]Commit{{Type: "feat"}, {Type: "fix", Breaking: true}}, "")
	if err != nil || v.String() != "0.5.0" {
		t.Fatalf("0.x breaking = %v, %v", v, err)
	}
	v, err = r.Next([]Commit{{Type: "feat"}}, "")
	if err != nil || v.String() != "0.4.1" {
		t.Fatalf("0.x feat = %v, %v", v, err)
	}
	v, err = r.Next([]Commit{{Type: "chore", Breaking: true}}, "")
	if err != nil || v.String() != "0.5.0" {
		t.Fatalf("0.x breaking only = %v, %v", v, err)
	}
	r0 := New()
	v, err = r0.Next([]Commit{{Type: "x", Breaking: true}}, "")
	if err != nil || v.String() != "0.1.0" {
		t.Fatalf("0.0.0 breaking = %v, %v", v, err)
	}
}

func TestOneXLevelMapping(t *testing.T) {
	r := New()
	mustTag(t, r, "1.2.3")
	cases := []struct {
		c    Commit
		want string
	}{
		{Commit{Type: "fix"}, "1.2.4"},
		{Commit{Type: "perf"}, "1.2.4"},
		{Commit{Type: "feat"}, "1.3.0"},
		{Commit{Type: "feat", Breaking: true}, "2.0.0"},
		{Commit{Type: "docs", Breaking: true}, "2.0.0"},
	}
	for _, tc := range cases {
		v, err := r.Next([]Commit{tc.c}, "")
		if err != nil || v.String() != tc.want {
			t.Fatalf("%+v -> %v, %v, want %s", tc.c, v, err, tc.want)
		}
	}
	if _, err := r.Next([]Commit{{Type: "docs"}, {Type: "style"}}, ""); reasonOf(err) != ErrNoRelease {
		t.Fatalf("no-level err = %v", err)
	}
}

func TestNoLevelWithPrereleaseLine(t *testing.T) {
	r := New()
	mustTag(t, r, "1.0.0", "2.0.0-alpha.3")
	v, err := r.Next([]Commit{{Type: "docs"}}, "alpha")
	if err != nil || v.String() != "2.0.0-alpha.4" {
		t.Fatalf("line continue alpha = %v, %v", v, err)
	}
	r2 := New()
	mustTag(t, r2, "1.0.0")
	if _, err := r2.Next([]Commit{{Type: "docs"}}, ""); reasonOf(err) != ErrNoRelease {
		t.Fatalf("expected no release, got %v", err)
	}
}

func reasonOf(err error) Reason {
	if e, ok := err.(*Error); ok {
		return e.Reason
	}
	return ""
}

func versionsStr(r *Releaser) []string {
	vs := r.Versions()
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return out
}

func TestParseAndOrder(t *testing.T) {
	good := []string{
		"0.0.0", "1.2.3", "999999.0.1", "1.0.0-a.1", "1.0.0-z.999999",
	}
	for _, s := range good {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", s, err)
		}
		if v.String() != s {
			t.Fatalf("round trip %q -> %q", s, v.String())
		}
	}
	bad := []string{
		"", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
		"1000000.0.0", "1.2.3-", "1.2.3-rc.", "1.2.3-.1",
		"1.2.3-RC.1", "1.2.3-rc.0", "1.2.3-rc.01", "1.2.3-rc.1000000",
		"1.2.3-rc1", "1.2.3-toolongchannelname.1", "1.2.3-rc-x.1",
		"v1.2.3", "1.2.3-rc.1.2",
	}
	for _, s := range bad {
		if _, err := ParseVersion(s); err == nil {
			t.Fatalf("ParseVersion(%q) expected error", s)
		}
	}

	r := New()
	mustTag(t, r,
		"2.0.0",
		"1.0.0-b.2", "1.0.0-b.1", "1.0.0-a.9",
		"1.0.0",
		"0.9.9",
	)
	got := versionsStr(r)
	want := []string{
		"0.9.9",
		"1.0.0-a.9", "1.0.0-b.1", "1.0.0-b.2",
		"1.0.0",
		"2.0.0",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Versions order = %v, want %v", got, want)
	}

	if err := r.Tag("1.0.0"); reasonOf(err) != ErrAlreadyTagged {
		t.Fatalf("duplicate tag reason = %v", err)
	}
	if err := r.Tag("bad"); reasonOf(err) != ErrInvalidVersion {
		t.Fatalf("bad tag reason = %v", err)
	}
}
