package trim

import (
	"testing"

	"ontology/hoplex"
)

func TestDbgTrim(t *testing.T) {
	tr := New(Config{16, 8})
	if err := tr.Trust("10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	t.Logf("prefixes=%v size=%d", tr.prefixes, tr.trustSize)
	hops, herr := hoplex.ParseChain("1.2.3.4, 10.0.0.1, 10.0.0.2")
	t.Logf("herr=%v", herr)
	for i, h := range hops {
		t.Logf("hop%d addr=%v is4=%v in6=%v", i, h.Addr, h.Addr.Is4(), h.Addr.Is4In6())
	}
	r, err := tr.Trim("1.2.3.4, 10.0.0.1, 10.0.0.2", "", "10.0.0.2")
	t.Logf("r=%+v err=%v calls=%d", r, err, tr.matchCalls)
}
