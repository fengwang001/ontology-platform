package emrtest_test

import (
	"errors"
	"testing"

	"ontology/record"
	"ontology/seal"
	"ontology/sign"
)

// svc 把三个包组装到同一存储上。
type svc struct {
	r *record.Record
	sn *sign.Sign
	sl *seal.Seal
}

func newSvc(T, U int64) *svc {
	r := record.New(T, U)
	return &svc{r: r, sn: sign.Wrap(r), sl: seal.Wrap(r)}
}

func (v *svc) user(name, dept string, level int, roles int) error {
	return v.r.AddUser(name, dept, level, roles)
}

func (v *svc) enc(enc string) error { return v.r.OpenEnc(enc) }

func (v *svc) dis(now int64, enc string) error { return v.r.Discharge(now, enc) }

func (v *svc) create(now int64, user, enc, doc, content string) error {
	return v.r.Create(now, user, enc, doc, []byte(content))
}

func (v *svc) edit(now int64, user, doc, content string) error {
	return v.r.Edit(now, user, doc, []byte(content))
}

func (v *svc) sign(now int64, user, doc string) error {
	return v.sn.Sign(now, user, doc)
}

func (v *svc) cosign(now int64, user, doc string) error {
	return v.sn.Cosign(now, user, doc)
}

func (v *svc) ret(now int64, user, doc string) error {
	return v.sn.Return(now, user, doc)
}

func (v *svc) seal(now int64, user, doc string) error {
	return v.sl.Seal(now, user, doc)
}

func (v *svc) amend(now int64, user, doc, content string) error {
	return v.sl.Amend(now, user, doc, []byte(content))
}

func (v *svc) unseal(now int64, a, b, doc string) error {
	return v.sl.Unseal(now, a, b, doc)
}

func (v *svc) defects() []string { return v.sl.Defects() }

func (v *svc) verify(doc string) (seal.VerifyResult, error) {
	return v.sl.Verify(doc)
}

func (v *svc) inspect(doc string) record.Doc {
	d, err := v.r.Inspect(doc)
	if err != nil {
		panic(err)
	}
	return d
}

func (v *svc) hashes() int { return v.r.HashCount() }

func errIs(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func mustOK(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		 t.Fatalf("%s: unexpected error %v", label, err)
	}
}

func mustErr(t *testing.T, label string, got, want error) {
	t.Helper()
	if !errIs(got, want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
}

func setupExample(t *testing.T) *svc {
	t.Helper()
	v := newSvc(4320, 60)
	mustOK(t, "add r", v.user("r", "内科", 1, 0))
	mustOK(t, "add s", v.user("s", "内科", 2, 0))
	mustOK(t, "add k", v.user("k", "外科", 3, 0))
	mustOK(t, "add enc", v.enc("e1"))
	mustOK(t, "discharge", v.dis(1000, "e1"))
	return v
}
