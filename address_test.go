package ontology

import "testing"

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		normalized string
		domain     string
		ok         bool
	}{
		{name: "canonical example", input: "a.b+x@Gmail.com", normalized: "ab@gmail.com", domain: "gmail.com", ok: true},
		{name: "googlemail alias", input: "AB@googlemail.com", normalized: "ab@gmail.com", domain: "gmail.com", ok: true},
		{name: "googlemail dots after alias", input: "a.b+x@Googlemail.com", normalized: "ab@gmail.com", domain: "gmail.com", ok: true},
		{name: "plus truncation empty", input: "+x@gmail.com", ok: false},
		{name: "gmail dots empty after plus", input: ".+x@gmail.com", ok: false},
		{name: "non gmail keeps dots", input: "A.B+x@Example.com", normalized: "a.b@example.com", domain: "example.com", ok: true},
		{name: "missing at", input: "aexample.com", ok: false},
		{name: "multiple at", input: "a@b@example.com", ok: false},
		{name: "empty local", input: "@example.com", ok: false},
		{name: "empty domain", input: "a@", ok: false},
		{name: "domain without dot", input: "a@example", ok: false},
		{name: "empty domain label", input: "a@example..com", ok: false},
		{name: "control byte", input: "a\t@example.com", ok: false},
		{name: "delete byte", input: "a\x7f@example.com", ok: false},
		{name: "too long", input: string(make([]byte, 255)), ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized, domain, _, ok := normalizeAddress(tt.input)
			if ok != tt.ok || normalized != tt.normalized || domain != tt.domain {
				t.Fatalf("normalizeAddress(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.input, normalized, domain, ok, tt.normalized, tt.domain, tt.ok)
			}
		})
	}
}

func TestNewManagerValidatesParameters(t *testing.T) {
	valid := []int64{1, 1, 1, 1, 1, 1, 1}
	for i := range valid {
		for _, value := range []int64{0, -1} {
			params := append([]int64(nil), valid...)
			params[i] = value
			if _, err := NewManager(params[0], params[1], params[2], params[3], params[4], params[5], params[6]); err != ErrInvalidParameter {
				t.Fatalf("parameter %d = %d: error = %v, want %v", i, value, err, ErrInvalidParameter)
			}
		}
	}

	if _, err := NewManager(17, 1, 1, 1, 1, 1, 1); err != ErrInvalidParameter {
		t.Fatalf("soft threshold upper bound: %v", err)
	}
	if _, err := NewManager(1, 1, 1, 1, 1001, 1, 1); err != ErrInvalidParameter {
		t.Fatalf("domain threshold upper bound: %v", err)
	}
}
