package mesh_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/mesh"
)

// opLog records every test operation with inputs, actual output and the
// judgment basis. Entries print via t.Logf so `go test -v` shows the trail.
type opLog struct {
	mu  sync.Mutex
	t   *testing.T
	sb  strings.Builder
	ops int
}

func newOpLog(t *testing.T) *opLog {
	return &opLog{t: t}
}

func (l *opLog) record(op string, input, output, basis string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ops++
	entry := fmt.Sprintf("#%d OP=%s\n  INPUT:  %s\n  OUTPUT: %s\n  BASIS:  %s\n",
		l.ops, op, oneLine(input), oneLine(output), oneLine(basis))
	l.sb.WriteString(entry)
	l.t.Log(strings.TrimRight(entry, "\n"))
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	return s
}

func describeErr(err *mesh.Error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("Kind=%s rule=%d msg=%q", err.Kind, err.RuleIndex, err.Error())
}

func describeResult(r *mesh.RouteResult) string {
	if r == nil {
		return "<nil>"
	}
	p := func(v *int) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("%d", *v)
	}
	return fmt.Sprintf("subset=%s rule=%d target=%d fallback=%t policy{timeout=%s retries=%s perAttempt=%s}",
		r.Subset, r.RuleIndex, r.TargetIdx, r.FromFallback,
		p(r.Policy.Timeout), p(r.Policy.Retries), p(r.Policy.PerAttemptTime))
}

func ip(v int) *int { return &v }

func mustPublish(t *testing.T, l *opLog, s *mesh.Service, cfg *mesh.Config, base int) int {
	v, err := s.Publish(cfg, base)
	l.record("Publish", describeConfig(cfg), fmt.Sprintf("version=%d err=%s", v, describeErr(err)),
		"valid config; atomic replace, version increments by one")
	if err != nil {
		t.Fatalf("publish rejected: %v", err)
	}
	return v
}

func expectRoute(t *testing.T, l *opLog, s *mesh.Service, req mesh.Request, wantSubset string, wantRule int, basis string) {
	r, err := s.Route(req)
	l.record("Route", describeRequest(req),
		fmt.Sprintf("result=%s err=%s", describeResult(r), describeErr(err)), basis)
	if err != nil {
		t.Fatalf("route error: %v", err)
	}
	if r.Subset != wantSubset || r.RuleIndex != wantRule {
		t.Fatalf("got subset=%s rule=%d, want subset=%s rule=%d", r.Subset, r.RuleIndex, wantSubset, wantRule)
	}
}

func expectErrKind(t *testing.T, l *opLog, op string, input string, got *mesh.Error, want mesh.Kind, basis string) {
	l.record(op, input, describeErr(got), basis)
	if got == nil {
		t.Fatalf("expected error kind %s, got nil", want)
	}
	if got.Kind != want {
		t.Fatalf("error kind = %s, want %s", got.Kind, want)
	}
}

func describeConfig(c *mesh.Config) string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("rules=%d fallback=%v default=%s",
		len(c.Rules), c.Fallback, describePolicy(c.Default))
}

func describePolicy(p mesh.Policy) string {
	pp := func(v *int) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprintf("%d", *v)
	}
	return fmt.Sprintf("{t=%s r=%s pat=%s}", pp(p.Timeout), pp(p.Retries), pp(p.PerAttemptTime))
}

func describeRequest(r mesh.Request) string {
	return fmt.Sprintf("path=%q headers=%v bucket=%d", r.Path, r.Headers, r.Bucket)
}

func regReady(s *mesh.Service, name string, addrs ...string) {
	def := mesh.SubsetDef{Name: name}
	for _, a := range addrs {
		def.Endpoints = append(def.Endpoints, mesh.Endpoint{Addr: a, Ready: true})
	}
	_ = s.Registry.Register(def)
}
