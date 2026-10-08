package authz

// An independent, deliberately naive reference model of the evaluation
// semantics (linear scan over every policy, no indexing) plus a randomized
// differential test that steps the real Store and the model through
// identical operation sequences and compares results after every step.
// Every operation's input, actual output and judgment are logged (run
// with -v to see them).

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// --- naive model -----------------------------------------------------------

type naiveModel struct {
	root     string
	version  uint64
	policies []Policy
}

func (m *naiveModel) replace(policies []Policy) uint64 {
	m.version++
	m.policies = append([]Policy(nil), policies...)
	return m.version
}

func naiveElem(elem, value string) bool {
	switch {
	case elem == "*":
		return true
	case strings.HasPrefix(elem, "*"):
		return strings.HasSuffix(value, elem[1:])
	case strings.HasSuffix(elem, "*"):
		return strings.HasPrefix(value, elem[:len(elem)-1])
	default:
		return elem == value
	}
}

func naiveAnyPattern(set []string, value string) bool {
	for _, e := range set {
		if naiveElem(e, value) {
			return true
		}
	}
	return false
}

func naiveAnyExact(set []string, value string) bool {
	for _, e := range set {
		if e == value {
			return true
		}
	}
	return false
}

func naiveAnyPort(set []int, port int) bool {
	for _, p := range set {
		if p == port {
			return true
		}
	}
	return false
}

func naiveRuleMatch(r *Rule, req *Request) bool {
	if s := r.Source; s != nil {
		if len(s.NotIdentities) > 0 && naiveAnyPattern(s.NotIdentities, req.SourceIdentity) {
			return false
		}
		if len(s.Identities) > 0 && !naiveAnyPattern(s.Identities, req.SourceIdentity) {
			return false
		}
		if len(s.NotNamespaces) > 0 && naiveAnyPattern(s.NotNamespaces, req.SourceNamespace) {
			return false
		}
		if len(s.Namespaces) > 0 && !naiveAnyPattern(s.Namespaces, req.SourceNamespace) {
			return false
		}
	}
	if o := r.Operation; o != nil {
		if len(o.NotMethods) > 0 && naiveAnyExact(o.NotMethods, req.Method) {
			return false
		}
		if len(o.Methods) > 0 && !naiveAnyExact(o.Methods, req.Method) {
			return false
		}
		if len(o.NotPaths) > 0 && naiveAnyPattern(o.NotPaths, req.Path) {
			return false
		}
		if len(o.Paths) > 0 && !naiveAnyPattern(o.Paths, req.Path) {
			return false
		}
		if len(o.NotPorts) > 0 && naiveAnyPort(o.NotPorts, req.Port) {
			return false
		}
		if len(o.Ports) > 0 && !naiveAnyPort(o.Ports, req.Port) {
			return false
		}
	}
	for _, c := range r.Conditions {
		var values []string
		for name, vs := range req.Headers {
			if strings.EqualFold(name, c.Header) {
				values = vs
				break
			}
		}
		if len(values) == 0 {
			if len(c.Values) > 0 {
				return false
			}
			continue
		}
		if len(c.Values) > 0 {
			ok := false
			for _, v := range values {
				if naiveAnyExact(c.Values, v) {
					ok = true
				}
			}
			if !ok {
				return false
			}
		}
		for _, v := range values {
			if naiveAnyExact(c.NotValues, v) {
				return false
			}
		}
	}
	return true
}

func naiveHit(p *Policy, req *Request) bool {
	for i := range p.Rules {
		if naiveRuleMatch(&p.Rules[i], req) {
			return true
		}
	}
	return false
}

func naiveApplicable(p *Policy, req *Request, root string) bool {
	if p.Namespace != req.TargetNamespace && p.Namespace != root {
		return false
	}
	for k, v := range p.Selector {
		if req.TargetLabels[k] != v {
			return false
		}
	}
	return true
}

func naiveSort(refs []PolicyRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Namespace < refs[j].Namespace
	})
}

func (m *naiveModel) eval(req *Request) Result {
	var denyHits, allowHits, auditHits []PolicyRef
	allowApplicable := false
	for i := range m.policies {
		p := &m.policies[i]
		if !naiveApplicable(p, req, m.root) {
			continue
		}
		switch p.Action {
		case ActionDeny:
			if naiveHit(p, req) {
				denyHits = append(denyHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		case ActionAllow:
			allowApplicable = true
			if naiveHit(p, req) {
				allowHits = append(allowHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		case ActionAudit:
			if naiveHit(p, req) {
				auditHits = append(auditHits, PolicyRef{p.Name, p.Namespace, p.Action})
			}
		}
	}
	res := Result{Version: m.version}
	naiveSort(auditHits)
	res.AuditPolicies = auditHits
	switch {
	case len(denyHits) > 0:
		res.Decision = DecisionDeny
		naiveSort(denyHits)
		res.DecisionPolicies = denyHits
	case !allowApplicable:
		res.Decision = DecisionAllow
	case len(allowHits) > 0:
		res.Decision = DecisionAllow
		naiveSort(allowHits)
		res.DecisionPolicies = allowHits
	default:
		res.Decision = DecisionDeny
	}
	return res
}

// --- random generators -------------------------------------------------------

var (
	randNamespaces  = []string{"prod", "default", "staging", testRoot}
	randIdentities  = []string{"cluster.local/ns/default/sa/web", "cluster.local/ns/prod/sa/api", "cluster.local/ns/staging/sa/job"}
	randMethods     = []string{"GET", "POST", "DELETE"}
	randPaths       = []string{"/v1/items", "/v1/users", "/healthz", "/v2/items"}
	randHeaderNames = []string{"x-tenant", "x-env"}
	randHeaderVals  = []string{"a", "b", "debug"}
	randLabelKeys   = []string{"app", "tier"}
	randLabelVals   = []string{"api", "web", "backend"}
)

// randPattern builds a pattern that matches value with probability ~1/2.
func randPattern(r *rand.Rand, value string) string {
	switch r.Intn(6) {
	case 0:
		return "*"
	case 1:
		return value
	case 2:
		if len(value) > 2 {
			return value[:len(value)/2] + "*"
		}
		return value
	case 3:
		if len(value) > 2 {
			return "*" + value[len(value)/2:]
		}
		return value
	case 4:
		return value + "-nope"
	default:
		return "no-match-" + value
	}
}

func randPatternSet(r *rand.Rand, values []string) []string {
	n := r.Intn(3)
	if n == 0 {
		return nil
	}
	set := make([]string, 0, n)
	for i := 0; i < n; i++ {
		set = append(set, randPattern(r, values[r.Intn(len(values))]))
	}
	return set
}

func randExactSet(r *rand.Rand, values []string) []string {
	n := r.Intn(3)
	if n == 0 {
		return nil
	}
	set := make([]string, 0, n)
	for i := 0; i < n; i++ {
		set = append(set, values[r.Intn(len(values))])
	}
	return set
}

func randPortSet(r *rand.Rand) []int {
	n := r.Intn(3)
	if n == 0 {
		return nil
	}
	pool := []int{80, 443, 8080, 9090}
	set := make([]int, 0, n)
	for i := 0; i < n; i++ {
		set = append(set, pool[r.Intn(len(pool))])
	}
	return set
}

func randRule(r *rand.Rand) Rule {
	var rule Rule
	if r.Intn(4) != 0 {
		rule.Source = &SourceSpec{
			Identities:    randPatternSet(r, randIdentities),
			NotIdentities: randPatternSet(r, randIdentities),
			Namespaces:    randPatternSet(r, randNamespaces),
			NotNamespaces: randPatternSet(r, randNamespaces),
		}
	}
	if r.Intn(4) != 0 {
		rule.Operation = &OperationSpec{
			Methods:  randExactSet(r, randMethods),
			Paths:    randPatternSet(r, randPaths),
			Ports:    randPortSet(r),
			NotPorts: randPortSet(r),
		}
	}
	if r.Intn(4) != 0 {
		n := 1 + r.Intn(2)
		for i := 0; i < n; i++ {
			rule.Conditions = append(rule.Conditions, Condition{
				Header:    randHeaderNames[r.Intn(len(randHeaderNames))],
				Values:    randExactSet(r, randHeaderVals),
				NotValues: randExactSet(r, randHeaderVals),
			})
		}
	}
	return rule
}

func randPolicy(r *rand.Rand, idx int) Policy {
	p := Policy{
		Name:      fmt.Sprintf("pol-%d", idx),
		Namespace: randNamespaces[r.Intn(len(randNamespaces))],
		Action:    Action(r.Intn(3)),
	}
	if r.Intn(2) == 0 {
		p.Selector = map[string]string{
			randLabelKeys[r.Intn(len(randLabelKeys))]: randLabelVals[r.Intn(len(randLabelVals))],
		}
	}
	n := r.Intn(4)
	for i := 0; i < n; i++ {
		p.Rules = append(p.Rules, randRule(r))
	}
	return p
}

func randRequest(r *rand.Rand) Request {
	req := Request{
		SourceIdentity:  randIdentities[r.Intn(len(randIdentities))],
		SourceNamespace: randNamespaces[r.Intn(len(randNamespaces))],
		TargetNamespace: randNamespaces[r.Intn(len(randNamespaces))],
		TargetLabels: map[string]string{
			"app":  randLabelVals[r.Intn(len(randLabelVals))],
			"tier": randLabelVals[r.Intn(len(randLabelVals))],
		},
		Method: randMethods[r.Intn(len(randMethods))],
		Path:   randPaths[r.Intn(len(randPaths))],
		Port:   []int{80, 443, 8080, 9090}[r.Intn(4)],
	}
	if r.Intn(2) == 0 {
		req.Headers = map[string][]string{
			randHeaderNames[r.Intn(len(randHeaderNames))]: {randHeaderVals[r.Intn(len(randHeaderVals))]},
		}
	}
	return req
}

// --- differential test -------------------------------------------------------

func resultsEqual(a, b Result) bool {
	if a.Decision != b.Decision || a.Version != b.Version {
		return false
	}
	if len(a.DecisionPolicies) != len(b.DecisionPolicies) || len(a.AuditPolicies) != len(b.AuditPolicies) {
		return false
	}
	for i := range a.DecisionPolicies {
		if a.DecisionPolicies[i] != b.DecisionPolicies[i] {
			return false
		}
	}
	for i := range a.AuditPolicies {
		if a.AuditPolicies[i] != b.AuditPolicies[i] {
			return false
		}
	}
	return true
}

func TestRandomizedDifferentialAgainstNaiveModel(t *testing.T) {
	const steps = 2000
	r := rand.New(rand.NewSource(20261007))
	store := newStore(t)
	model := &naiveModel{root: testRoot}

	for step := 0; step < steps; step++ {
		if r.Intn(3) == 0 {
			// Replace the whole set with a random legal one.
			n := r.Intn(12)
			set := make([]Policy, 0, n)
			used := map[string]bool{}
			for i := 0; i < n; i++ {
				p := randPolicy(r, i)
				key := p.Namespace + "/" + p.Name
				if used[key] {
					continue
				}
				used[key] = true
				set = append(set, p)
			}
			v, err := store.ReplaceAll(set)
			if err != nil {
				t.Fatalf("step %d: unexpected rejection of generated set: %v", step, err)
			}
			mv := model.replace(set)
			ok := v == mv
			t.Logf("step %d REPLACE policies=%d -> store.version=%d model.version=%d match=%v",
				step, len(set), v, mv, ok)
			if !ok {
				t.Fatalf("step %d: version mismatch store=%d model=%d", step, v, mv)
			}
			continue
		}
		req := randRequest(r)
		got, err := store.Evaluate(req)
		if err != nil {
			t.Fatalf("step %d: unexpected evaluate error: %v", step, err)
		}
		want := model.eval(&req)
		ok := resultsEqual(got, want)
		t.Logf("step %d EVAL src=%q sns=%s tns=%s labels=%v %s %s :%d headers=%v -> got=%s v=%d dec=%v aud=%v want=%s dec=%v aud=%v match=%v",
			step, req.SourceIdentity, req.SourceNamespace, req.TargetNamespace, req.TargetLabels,
			req.Method, req.Path, req.Port, req.Headers,
			got.Decision, got.Version, refNames(got.DecisionPolicies), refNames(got.AuditPolicies),
			want.Decision, refNames(want.DecisionPolicies), refNames(want.AuditPolicies), ok)
		if !ok {
			t.Fatalf("step %d: mismatch\ngot:  %+v\nwant: %+v", step, got, want)
		}
	}
}
