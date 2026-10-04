package offline_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/errs"
	"ontology/offline"
	"ontology/playback"
)

// ---- naive model: everything recomputed from raw facts on every op ----

type naiveRec struct {
	rentalEnd int64
	firstPlay int64
	played    bool
}

type naiveCooldown struct {
	until int64
	dev   string
}

type naiveModel struct {
	p        offline.Params
	lastNow  int64
	accounts map[string]bool
	titles   map[string]int64
	devices  map[string]map[string]bool // acct -> dev set
	cools    map[string][]naiveCooldown
	lics     map[string]map[string]map[string]*naiveRec // acct -> dev -> title
}

func newNaive(p offline.Params) *naiveModel {
	return &naiveModel{
		p:        p,
		accounts: map[string]bool{},
		titles:   map[string]int64{},
		devices:  map[string]map[string]bool{},
		cools:    map[string][]naiveCooldown{},
		lics:     map[string]map[string]map[string]*naiveRec{},
	}
}

func validStr(s string) bool { return s != "" }

func (n *naiveModel) checkClock(now int64) error {
	if now < n.lastNow {
		return errs.ErrClockRewind
	}
	return nil
}

func (n *naiveModel) addAccount(now int64, acct string) error {
	if now < 0 || now > 1e12 || !validStr(acct) {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if n.accounts[acct] {
		return errs.ErrAccountExists
	}
	n.accounts[acct] = true
	n.devices[acct] = map[string]bool{}
	n.lics[acct] = map[string]map[string]*naiveRec{}
	n.lastNow = now
	return nil
}

func (n *naiveModel) addTitle(now int64, title string, end int64) error {
	if now < 0 || now > 1e12 || !validStr(title) || end <= now {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.titles[title]; ok {
		return errs.ErrTitleExists
	}
	n.titles[title] = end
	n.lastNow = now
	return nil
}

func (n *naiveModel) setTitleEnd(now int64, title string, end int64) error {
	if now < 0 || now > 1e12 || !validStr(title) || end <= now {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if _, ok := n.titles[title]; !ok {
		return errs.ErrNoTitle
	}
	n.titles[title] = end
	n.lastNow = now
	return nil
}

// activeCools recomputes the set of still-occupied cooldown slots at now by a
// full scan of raw deregistration facts.
func (n *naiveModel) activeCools(acct string, now int64) []naiveCooldown {
	out := []naiveCooldown{}
	for _, c := range n.cools[acct] {
		if c.until > now {
			out = append(out, c)
		}
	}
	return out
}

func (n *naiveModel) register(now int64, acct, dev string) error {
	if now < 0 || now > 1e12 || !validStr(acct) || !validStr(dev) {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if !n.accounts[acct] {
		return errs.ErrNoAccount
	}
	if n.devices[acct][dev] {
		return errs.ErrDeviceExists
	}
	// physically discard every cooldown slot released at or before now
	kept := make([]naiveCooldown, 0, len(n.cools[acct]))
	for _, raw := range n.cools[acct] {
		if raw.until > now {
			kept = append(kept, raw)
		}
	}
	n.cools[acct] = kept
	active := n.activeCools(acct, now)
	for i, c := range active {
		if c.dev == dev { // reuse own un-released slot
			// remove that single fact: rebuild raw list dropping it
			filtered := make([]naiveCooldown, 0, len(n.cools[acct]))
			dropped := false
			for _, raw := range n.cools[acct] {
				if !dropped && raw.until > now && raw.dev == dev {
					dropped = true
					continue
				}
				filtered = append(filtered, raw)
			}
			n.cools[acct] = filtered
			n.devices[acct][dev] = true
			n.lastNow = now
			return nil
		}
		_ = i
	}
	if len(n.devices[acct])+len(kept) >= n.p.Dmax {
		return errs.ErrDeviceFull
	}
	n.devices[acct][dev] = true
	n.lastNow = now
	return nil
}

func (n *naiveModel) deregister(now int64, acct, dev string) error {
	if now < 0 || now > 1e12 || !validStr(acct) || !validStr(dev) {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if !n.accounts[acct] {
		return errs.ErrNoAccount
	}
	if !n.devices[acct][dev] {
		return errs.ErrNoDevice
	}
	delete(n.devices[acct], dev)
	n.lics[acct][dev] = map[string]*naiveRec{} // licenses destroyed
	// discard cooldown slots released at or before now (equality releases)
	kept := make([]naiveCooldown, 0, len(n.cools[acct]))
	for _, raw := range n.cools[acct] {
		if raw.until > now {
			kept = append(kept, raw)
		}
	}
	n.cools[acct] = kept
	n.cools[acct] = append(n.cools[acct], naiveCooldown{until: now + n.p.Cool, dev: dev})
	n.lastNow = now
	return nil
}

func (n *naiveModel) exp(r *naiveRec, title string) int64 {
	end := n.titles[title]
	if r.played {
		e := r.firstPlay + n.p.Lp
		if e > end {
			return end
		}
		return e
	}
	if r.rentalEnd > end {
		return end
	}
	return r.rentalEnd
}

func (n *naiveModel) expiredErr(r *naiveRec, title string, now int64) *errs.ExpiredError {
	exp := n.exp(r, title)
	switch {
	case now >= n.titles[title]:
		return &errs.ExpiredError{Reason: errs.ReasonTitleEnded, Exp: exp}
	case r.played && now >= r.firstPlay+n.p.Lp:
		return &errs.ExpiredError{Reason: errs.ReasonPlayEnded, Exp: exp}
	default:
		return &errs.ExpiredError{Reason: errs.ReasonRentalEnded, Exp: exp}
	}
}

func (n *naiveModel) download(now int64, acct, dev, title string) error {
	if now < 0 || now > 1e12 || !validStr(acct) || !validStr(dev) || !validStr(title) {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if !n.accounts[acct] {
		return errs.ErrNoAccount
	}
	if _, ok := n.titles[title]; !ok {
		return errs.ErrNoTitle
	}
	if !n.devices[acct][dev] {
		return errs.ErrNoDevice
	}
	if now >= n.titles[title] {
		return errs.ErrTitleEnded
	}
	r := n.lics[acct][dev][title]
	if r != nil {
		valid := now < n.exp(r, title)
		switch {
		case valid && r.played:
			return errs.ErrAlreadyPlayed
		case valid:
			r.rentalEnd = now + n.p.Lr
			n.lastNow = now
			return nil
		}
	}
	// full recompute of the account's valid license count
	validCount := 0
	for _, tm := range n.lics[acct] {
		for tn, rr := range tm {
			if now < n.exp(rr, tn) {
				validCount++
			}
		}
	}
	if validCount >= n.p.Omax {
		return errs.ErrLicenseFull
	}
	if n.lics[acct][dev] == nil {
		n.lics[acct][dev] = map[string]*naiveRec{}
	}
	n.lics[acct][dev][title] = &naiveRec{rentalEnd: now + n.p.Lr}
	n.lastNow = now
	return nil
}

func (n *naiveModel) play(now int64, acct, dev, title string) error {
	if now < 0 || now > 1e12 || !validStr(acct) || !validStr(dev) || !validStr(title) {
		return errs.ErrInvalidParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if !n.accounts[acct] {
		return errs.ErrNoAccount
	}
	if _, ok := n.titles[title]; !ok {
		return errs.ErrNoTitle
	}
	if !n.devices[acct][dev] {
		return errs.ErrNoDevice
	}
	r := n.lics[acct][dev][title]
	if r == nil {
		return errs.ErrNoLicense
	}
	if now >= n.exp(r, title) {
		return n.expiredErr(r, title, now)
	}
	if !r.played {
		r.played = true
		r.firstPlay = now
	}
	n.lastNow = now
	return nil
}

func (n *naiveModel) status(acct, dev, title string, now int64) (playback.Result, bool) {
	if now < 0 || now > 1e12 || !validStr(acct) || !validStr(dev) || !validStr(title) {
		return playback.Result{}, false
	}
	r := n.lics[acct][dev][title]
	if r == nil {
		return playback.Result{}, false
	}
	exp := n.exp(r, title)
	if now >= exp {
		return playback.Result{Status: playback.StatusExpired, Exp: exp, Reason: n.expiredErr(r, title, now).Reason}, true
	}
	if r.played {
		return playback.Result{Status: playback.StatusPlaying, Exp: exp}, true
	}
	return playback.Result{Status: playback.StatusUnplayed, Exp: exp}, true
}

// ---- random operation sequence replay against both models ----

type opKind int

const (
	opAddAccount opKind = iota
	opAddTitle
	opSetTitleEnd
	opRegister
	opDeregister
	opDownload
	opPlay
	opStatus
)

type op struct {
	kind             opKind
	now              int64
	acct, dev, title string
	end              int64
}

func errToken(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := errs.IsExpired(err); ok {
		return "expired:" + e.Reason + fmt.Sprintf("@%d", e.Exp)
	}
	return err.Error()
}

func applyReal(m *offline.Manager, o op) error {
	switch o.kind {
	case opAddAccount:
		return m.AddAccount(o.now, o.acct)
	case opAddTitle:
		return m.AddTitle(o.now, o.title, o.end)
	case opSetTitleEnd:
		return m.SetTitleEnd(o.now, o.title, o.end)
	case opRegister:
		return m.Register(o.now, o.acct, o.dev)
	case opDeregister:
		return m.Deregister(o.now, o.acct, o.dev)
	case opDownload:
		return m.Download(o.now, o.acct, o.dev, o.title)
	case opPlay:
		return m.Play(o.now, o.acct, o.dev, o.title)
	}
	return nil
}

func applyNaive(n *naiveModel, o op) error {
	switch o.kind {
	case opAddAccount:
		return n.addAccount(o.now, o.acct)
	case opAddTitle:
		return n.addTitle(o.now, o.title, o.end)
	case opSetTitleEnd:
		return n.setTitleEnd(o.now, o.title, o.end)
	case opRegister:
		return n.register(o.now, o.acct, o.dev)
	case opDeregister:
		return n.deregister(o.now, o.acct, o.dev)
	case opDownload:
		return n.download(o.now, o.acct, o.dev, o.title)
	case opPlay:
		return n.play(o.now, o.acct, o.dev, o.title)
	}
	return nil
}

func opName(k opKind) string {
	return [...]string{"AddAccount", "AddTitle", "SetTitleEnd", "Register", "Deregister", "Download", "Play", "Status"}[k]
}

// stateSummary extracts comparable state from the real manager via debug accessors.
func stateSummary(m *offline.Manager) string {
	return m.DebugSummary()
}

func naiveSummary(n *naiveModel) string {
	var b strings.Builder
	accts := make([]string, 0, len(n.accounts))
	for a := range n.accounts {
		accts = append(accts, a)
	}
	sort.Strings(accts)
	for _, a := range accts {
		devs := append([]string{}, keysMapBool(n.devices[a])...)
		sort.Strings(devs)
		fmt.Fprintf(&b, "A %s D %v\n", a, devs)
		cs := append([]naiveCooldown{}, n.cools[a]...)
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].until != cs[j].until {
				return cs[i].until < cs[j].until
			}
			return cs[i].dev < cs[j].dev
		})
		for _, c := range cs {
			fmt.Fprintf(&b, "  C %s@%d\n", c.dev, c.until)
		}
		type flat struct {
			dev, title string
			r          *naiveRec
		}
		var fr []flat
		for d, tm := range n.lics[a] {
			for tn, r := range tm {
				fr = append(fr, flat{d, tn, r})
			}
		}
		sort.Slice(fr, func(i, j int) bool {
			if fr[i].dev != fr[j].dev {
				return fr[i].dev < fr[j].dev
			}
			return fr[i].title < fr[j].title
		})
		for _, f := range fr {
			fmt.Fprintf(&b, "  L %s %s rental=%d played=%v fp=%d\n",
				f.dev, f.title, f.r.rentalEnd, f.r.played, f.r.firstPlay)
		}
	}
	titles := make([]string, 0, len(n.titles))
	for tn := range n.titles {
		titles = append(titles, tn)
	}
	sort.Strings(titles)
	for _, tn := range titles {
		fmt.Fprintf(&b, "T %s end=%d\n", tn, n.titles[tn])
	}
	return b.String()
}

func keysMapBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func genOps(rng *rand.Rand, seq int) (offline.Params, []op) {
	p := offline.Params{
		Dmax: 1 + rng.Intn(4),
		Cool: int64(1 + rng.Intn(300)),
		Lr:   int64(1 + rng.Intn(500)),
		Lp:   int64(1 + rng.Intn(100)),
		Omax: 1 + rng.Intn(3),
	}
	nOps := 60 + rng.Intn(240)
	accts := []string{"a", "b"}
	titles := []string{"T1", "T2", "T3", "T4"}
	devs := []string{"d1", "d2", "d3", "d4", "d5"}
	var now int64
	ops := make([]op, 0, nOps)
	for i := 0; i < nOps; i++ {
		// mostly non-decreasing now; occasionally an illegal rewind
		if rng.Intn(8) != 0 {
			now += int64(rng.Intn(120))
		}
		k := opKind(rng.Intn(int(opStatus) + 1))
		o := op{kind: k, now: now, acct: accts[rng.Intn(len(accts))],
			dev: devs[rng.Intn(len(devs))], title: titles[rng.Intn(len(titles))]}
		switch k {
		case opAddTitle, opSetTitleEnd:
			// 1/8 chance of illegal end <= now
			if rng.Intn(8) == 0 {
				o.end = now
			} else {
				o.end = now + 1 + int64(rng.Intn(2000))
			}
		}
		// occasional invalid empty ids or out-of-range now
		if rng.Intn(20) == 0 {
			o.acct = ""
		}
		if rng.Intn(40) == 0 {
			o.now = -1
		}
		ops = append(ops, o)
	}
	return p, ops
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(42))
	for seq := 0; seq < 1500; seq++ {
		p, ops := genOps(rng, seq)
		m, err := offline.New(p)
		if err != nil {
			t.Fatalf("seq %d: %v", seq, err)
		}
		n := newNaive(p)
		var log strings.Builder
		fmt.Fprintf(&log, "--- seq %d params=%+v ---\n", seq, p)
		for i, o := range ops {
			realErr := applyReal(m, o)
			naiveErr := applyNaive(n, o)
			var realStatus, naiveStatus string
			if o.kind == opStatus {
				rs, rok := m.Status(o.acct, o.dev, o.title, o.now)
				ns, nok := n.status(o.acct, o.dev, o.title, o.now)
				realStatus = statusToken(rs, rok)
				naiveStatus = statusToken(ns, nok)
			}
			basis := ""
			if e, ok := errs.IsExpired(realErr); ok {
				basis = " reason=" + e.Reason
			}
			fmt.Fprintf(&log, "#%d %s now=%d acct=%q dev=%q title=%q end=%d -> %s%s\n",
				i, opName(o.kind), o.now, o.acct, o.dev, o.title, o.end, errToken(realErr), basis)
			if realStatus != "" {
				fmt.Fprintf(&log, "    status real=%s naive=%s\n", realStatus, naiveStatus)
			}
			if errToken(realErr) != errToken(naiveErr) {
				t.Fatalf("seq %d op %d mismatch:\n%sreal=%s naive=%s",
					seq, i, log.String(), errToken(realErr), errToken(naiveErr))
			}
			if realStatus != naiveStatus {
				t.Fatalf("seq %d op %d status mismatch:\n%sreal=%s naive=%s",
					seq, i, log.String(), realStatus, naiveStatus)
			}
			if stateSummary(m) != naiveSummary(n) {
				t.Fatalf("seq %d op %d STATE mismatch:\n%s\nREAL:\n%s\nNAIVE:\n%s",
					seq, i, log.String(), stateSummary(m), naiveSummary(n))
			}
		}
		if seq < 3 {
			t.Logf("\n%s", log.String())
		}
	}
}

func statusToken(r playback.Result, ok bool) string {
	if !ok {
		return "absent"
	}
	return fmt.Sprintf("%s@%d/%s", r.Status, r.Exp, r.Reason)
}

func TestConcurrentSerializability(t *testing.T) {
	p := offline.Params{Dmax: 10, Cool: 100, Lr: 1000, Lp: 500, Omax: 10}
	m, err := offline.New(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AddAccount(0, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTitle(0, "T", 1_000_000); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dev := fmt.Sprintf("dev%d", g)
			_ = m.Register(int64(g), "a", dev)
			for i := 0; i < 50; i++ {
				now := int64(g + i) // each goroutine is non-decreasing
				_ = m.Download(now, "a", dev, "T")
				_ = m.Play(now+1, "a", dev, "T")
				_, _ = m.Status("a", dev, "T", now+2)
			}
		}(g)
	}
	wg.Wait()
}
