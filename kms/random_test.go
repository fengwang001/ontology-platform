package kms

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// kmsAPI is the common surface of Manager and naiveManager.
type kmsAPI interface {
	Create(id string, p, now int64) (int64, error)
	Encrypt(id string, now int64) (Credential, error)
	Decrypt(cred Credential, now int64) (bool, error)
	ReEncrypt(cred Credential, now int64) (Credential, bool, error)
	Disable(id string, now int64) error
	Enable(id string, now int64) error
	ScheduleDeletion(id string, w, now int64) error
	CancelDeletion(id string, now int64) error
	Describe(id string, now int64) (KeyView, error)
}

type rndOp struct {
	kind string
	id   string
	p    int64
	w    int64
	now  int64
	cred Credential
}

func (op rndOp) String() string {
	switch op.kind {
	case "create":
		return fmt.Sprintf("Create(%q,%d,%d)", op.id, op.p, op.now)
	case "encrypt":
		return fmt.Sprintf("Encrypt(%q,%d)", op.id, op.now)
	case "decrypt":
		return fmt.Sprintf("Decrypt(%+v,%d)", op.cred, op.now)
	case "reencrypt":
		return fmt.Sprintf("ReEncrypt(%+v,%d)", op.cred, op.now)
	case "disable":
		return fmt.Sprintf("Disable(%q,%d)", op.id, op.now)
	case "enable":
		return fmt.Sprintf("Enable(%q,%d)", op.id, op.now)
	case "schedule":
		return fmt.Sprintf("ScheduleDeletion(%q,%d,%d)", op.id, op.w, op.now)
	case "cancel":
		return fmt.Sprintf("CancelDeletion(%q,%d)", op.id, op.now)
	case "describe":
		return fmt.Sprintf("Describe(%q,%d)", op.id, op.now)
	}
	return "?"
}

func fmtErr(err error) string {
	if err == nil {
		return "OK"
	}
	var ke *Error
	if errors.As(err, &ke) {
		return fmt.Sprintf("ERR{%s|%s|%s}", ke.Code, ke.Reason, ke.State)
	}
	return "ERR{foreign:" + err.Error() + "}"
}

// applyOp runs one operation and returns its canonical result string plus,
// for successful encrypt/re-encrypt, the produced credential.
func applyOp(api kmsAPI, op rndOp) (string, *Credential) {
	switch op.kind {
	case "create":
		gen, err := api.Create(op.id, op.p, op.now)
		return fmt.Sprintf("gen=%d %s", gen, fmtErr(err)), nil
	case "encrypt":
		c, err := api.Encrypt(op.id, op.now)
		if err != nil {
			return fmt.Sprintf("%s", fmtErr(err)), nil
		}
		return fmt.Sprintf("cred=%+v OK", c), &c
	case "decrypt":
		ok, err := api.Decrypt(op.cred, op.now)
		return fmt.Sprintf("isMax=%v %s", ok, fmtErr(err)), nil
	case "reencrypt":
		c, changed, err := api.ReEncrypt(op.cred, op.now)
		if err != nil {
			return fmt.Sprintf("%s", fmtErr(err)), nil
		}
		return fmt.Sprintf("cred=%+v changed=%v OK", c, changed), &c
	case "disable":
		return fmtErr(api.Disable(op.id, op.now)), nil
	case "enable":
		return fmtErr(api.Enable(op.id, op.now)), nil
	case "schedule":
		return fmtErr(api.ScheduleDeletion(op.id, op.w, op.now)), nil
	case "cancel":
		return fmtErr(api.CancelDeletion(op.id, op.now)), nil
	case "describe":
		v, err := api.Describe(op.id, op.now)
		return fmt.Sprintf("view=%+v %s", v, fmtErr(err)), nil
	}
	panic("unknown op kind")
}

// TestRandomAgainstNaive replays 2000 random operation sequences against
// both the real Manager and the naive reference (plus a replayed Manager
// for determinism), comparing every output and error. Each step logs the
// input, the outputs and the match decision.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	ids := []string{"a", "b", "c"}
	periods := []int64{0, 60, 61, 90, 120}
	kinds := []string{
		"create", "create", "create",
		"encrypt", "encrypt", "encrypt", "encrypt",
		"decrypt", "decrypt", "decrypt",
		"reencrypt", "reencrypt",
		"disable", "disable",
		"enable", "enable",
		"schedule", "schedule",
		"cancel",
		"describe", "describe",
	}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 7))
		v := int64(1 + rng.Intn(4))
		wmin := int64(1 + rng.Intn(50))
		wmax := wmin + int64(rng.Intn(200))
		kmax := int64(1 + rng.Intn(3))
		real, err := NewManager(v, wmin, wmax, kmax)
		if err != nil {
			t.Fatalf("seq=%d: NewManager: %v", seq, err)
		}
		replay, err := NewManager(v, wmin, wmax, kmax)
		if err != nil {
			t.Fatalf("seq=%d: NewManager: %v", seq, err)
		}
		naive := newNaiveManager(v, wmin, wmax, kmax)

		var pool []Credential
		lastNow := int64(0)
		ops := 30 + rng.Intn(50)
		for i := 0; i < ops; i++ {
			// Clock: mostly non-decreasing, sometimes jumping backwards.
			if rng.Float64() < 0.75 {
				lastNow += int64(rng.Intn(150))
			} else {
				lastNow = int64(rng.Intn(2000))
			}
			op := rndOp{kind: kinds[rng.Intn(len(kinds))], now: lastNow}
			op.id = ids[rng.Intn(len(ids))]
			switch op.kind {
			case "create":
				if rng.Float64() < 0.1 {
					op.id = "ghost"
				}
				op.p = periods[rng.Intn(len(periods))]
			case "schedule":
				if rng.Float64() < 0.6 {
					op.w = wmin + rng.Int63n(wmax-wmin+1)
				} else {
					op.w = rng.Int63n(wmax + 50)
				}
			case "decrypt", "reencrypt":
				if len(pool) > 0 && rng.Float64() < 0.7 {
					op.cred = pool[rng.Intn(len(pool))]
				} else {
					op.cred = Credential{
						ID:      ids[rng.Intn(len(ids))],
						Gen:     1 + int64(rng.Intn(4)),
						Version: 1 + int64(rng.Intn(8)),
					}
					if rng.Float64() < 0.2 {
						op.cred.ID = "ghost"
					}
					if rng.Float64() < 0.1 {
						op.cred.Gen = 0
					}
					if rng.Float64() < 0.1 {
						op.cred.Version = 0
					}
				}
			}

			gotReal, cred := applyOp(real, op)
			gotNaive, _ := applyOp(naive, op)
			gotReplay, _ := applyOp(replay, op)
			decision := "match"
			if gotReal != gotNaive || gotReal != gotReplay {
				decision = "MISMATCH"
			}
			t.Logf("seq=%d op=%d in=%s | out=%s | naive=%s | replay=%s | decision=%s",
				seq, i, op, gotReal, gotNaive, gotReplay, decision)
			if decision != "match" {
				t.Fatalf("seq=%d op=%d in=%s\nreal:   %s\nnaive:  %s\nreplay: %s",
					seq, i, op, gotReal, gotNaive, gotReplay)
			}
			if cred != nil {
				pool = append(pool, *cred)
			}
		}
	}
}

// TestReplayDeterminism runs one fixed sequence twice and requires
// byte-identical results.
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		m, err := NewManager(3, 100, 1000, 2)
		if err != nil {
			t.Fatal(err)
		}
		ops := []rndOp{
			{kind: "create", id: "k", p: 60, now: 0},
			{kind: "encrypt", id: "k", now: 215},
			{kind: "disable", id: "k", now: 250},
			{kind: "enable", id: "k", now: 300},
			{kind: "schedule", id: "k", w: 100, now: 400},
			{kind: "cancel", id: "k", now: 499},
			{kind: "enable", id: "k", now: 600},
			{kind: "encrypt", id: "k", now: 700},
			{kind: "describe", id: "k", now: 700},
		}
		var out []string
		var cred *Credential
		for _, op := range ops {
			s, c := applyOp(m, op)
			out = append(out, s)
			if c != nil {
				cred = c
			}
		}
		if cred != nil {
			s, _ := applyOp(m, rndOp{kind: "decrypt", cred: *cred, now: 800})
			out = append(out, s)
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("length mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("op %d: %q != %q", i, first[i], second[i])
		}
	}
}
