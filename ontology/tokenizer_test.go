package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		Domains: map[string]Domain{
			"email":  {Name: "email", TokenLimit: 4},
			"phone":  {Name: "phone", TokenLimit: 4},
			"idcard": {Name: "idcard", TokenLimit: 4},
		},
		Columns: map[ColumnRef]string{
			{Table: "users", Column: "email"}:      "email",
			{Table: "accounts", Column: "contact"}: "email",
			{Table: "users", Column: "phone"}:      "phone",
			{Table: "accounts", Column: "phone"}:   "phone",
			{Table: "users", Column: "idcard"}:     "idcard",
		},
	}
}

func newTestTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	tk, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tk
}

func TestInvalidConfig(t *testing.T) {
	base := testConfig()
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"no domains", func(c *Config) { c.Domains = nil }},
		{"empty domain name", func(c *Config) {
			c.Domains[""] = Domain{Name: "", TokenLimit: 1}
			c.Columns[ColumnRef{Table: "t", Column: "c"}] = "x"
		}},
		{"domain key/name mismatch", func(c *Config) {
			c.Domains["email"] = Domain{Name: "mail"}
		}},
		{"negative limit", func(c *Config) {
			d := c.Domains["email"]
			d.TokenLimit = -1
			c.Domains["email"] = d
		}},
		{"empty table in binding", func(c *Config) {
			c.Columns[ColumnRef{Table: "", Column: "c"}] = "email"
		}},
		{"empty column in binding", func(c *Config) {
			c.Columns[ColumnRef{Table: "users", Column: ""}] = "email"
		}},
		{"binding to undefined domain", func(c *Config) {
			c.Columns[ColumnRef{Table: "users", Column: "phone"}] = "ghost"
		}},
		{"no columns", func(c *Config) { c.Columns = nil }},
	}
	details := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cloneConfig(base)
			tc.mutate(&cfg)
			_, err := New(cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
			details[err.Error()] = true
		})
	}
	if len(details) != len(cases) {
		t.Fatalf("reject details are not distinct: %v", details)
	}
}

func cloneConfig(c Config) Config {
	cp := Config{Domains: map[string]Domain{}, Columns: map[ColumnRef]string{}}
	for k, v := range c.Domains {
		cp.Domains[k] = v
	}
	for k, v := range c.Columns {
		cp.Columns[k] = v
	}
	return cp
}

func TestRejectCategoriesAreDistinct(t *testing.T) {
	tk := newTestTokenizer(t)

	categories := []struct {
		name string
		want error
		ev   *Event
	}{
		{"unknown table", ErrUnknownTable, &Event{Table: "ghost_table", Before: Image{"c": "x"}}},
		{"nil event", ErrInvalidEvent, nil},
		{"unsupported value", ErrInvalidEvent, &Event{Table: "users", Before: Image{"email": 1 + 2i}}},
		{"empty table name", ErrInvalidEvent, &Event{Table: "", After: Image{"email": "x"}}},
		{"empty column name", ErrInvalidEvent, &Event{Table: "users", After: Image{"": "x"}}},
	}
	seen := map[string]bool{}
	for _, c := range categories {
		t.Run(c.name, func(t *testing.T) {
			_, err := tk.Tokenize(c.ev)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			seen[err.Error()] = true
		})
	}
	if len(seen) != len(categories) {
		t.Fatalf("error messages not distinguishable: %v", seen)
	}
	for _, d := range []string{"email", "phone", "idcard"} {
		if tk.TokenCount(d) != 0 {
			t.Fatalf("state changed after rejects in domain %s: %d", d, tk.TokenCount(d))
		}
	}
}

func TestCrossTableSameDomain(t *testing.T) {
	tk := newTestTokenizer(t)

	out1, err := tk.Tokenize(&Event{Table: "users", After: Image{
		"email": "a@example.com",
		"phone": "111",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out1.After["email"] != "email-1" || out1.After["phone"] != "phone-1" {
		t.Fatalf("unexpected first tokens: %v", out1.After)
	}

	out2, err := tk.Tokenize(&Event{Table: "accounts", After: Image{
		"contact": "a@example.com",
		"phone":   "111",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out2.After["contact"] != "email-1" {
		t.Fatalf("same value cross-table must share token, got %v", out2.After["contact"])
	}
	if out2.After["phone"] != "phone-1" {
		t.Fatalf("phone token mismatch: %v", out2.After["phone"])
	}

	out3, err := tk.Tokenize(&Event{Table: "users", Before: Image{"email": "b@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if out3.Before["email"] != "email-2" {
		t.Fatalf("new value must take next contiguous number, got %v", out3.Before["email"])
	}
	if tk.TokenCount("email") != 2 || tk.NextNumber("email") != 3 {
		t.Fatalf("non-contiguous counters: count=%d next=%d", tk.TokenCount("email"), tk.NextNumber("email"))
	}
}

func TestOccurrenceOrdering(t *testing.T) {
	tk := newTestTokenizer(t)

	ev := &Event{
		Table: "users",
		Before: Image{
			"phone":  "p1",
			"idcard": "i1",
			"email":  "e1",
		},
		After: Image{
			"email":  "e1",
			"phone":  "p2",
			"idcard": "i1",
		},
	}
	out, err := tk.Tokenize(ev)
	if err != nil {
		t.Fatal(err)
	}

	wantBefore := Image{"email": "email-1", "idcard": "idcard-1", "phone": "phone-1"}
	for col, tok := range wantBefore {
		if out.Before[col] != tok {
			t.Fatalf("before.%s: want %s, got %v", col, tok, out.Before[col])
		}
	}
	if out.After["email"] != "email-1" || out.After["idcard"] != "idcard-1" {
		t.Fatalf("after must reuse before tokens, got %v", out.After)
	}
	if out.After["phone"] != "phone-2" {
		t.Fatalf("after-image new value must take phone-2, got %v", out.After["phone"])
	}

	// 列名字节序直接决定同一镜像内多列新值的编号。
	cfg := Config{
		Domains: map[string]Domain{"d": {Name: "d"}},
		Columns: map[ColumnRef]string{
			{Table: "t", Column: "zeta"}:  "d",
			{Table: "t", Column: "alpha"}: "d",
			{Table: "t", Column: "mid"}:   "d",
		},
	}
	tk2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := tk2.Tokenize(&Event{Table: "t", After: Image{
		"zeta":  "z",
		"mid":   "m",
		"alpha": "a",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out2.After["alpha"] != "d-1" || out2.After["mid"] != "d-2" || out2.After["zeta"] != "d-3" {
		t.Fatalf("column byte order violated: %v", out2.After)
	}
}

var _ = bytes.Equal

func TestConcurrentTokenization(t *testing.T) {
	cfg := Config{
		Domains: map[string]Domain{"d": {Name: "d", TokenLimit: 100000}},
		Columns: map[ColumnRef]string{
			{Table: "users", Column: "email"}:      "d",
			{Table: "accounts", Column: "contact"}: "d",
		},
	}
	tk, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 20
	const perG = 100
	const distinct = 20

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	var mu sync.Mutex
	valueToken := map[string]string{}
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			table := []string{"users", "accounts"}[g%2]
			col := map[bool]string{true: "email", false: "contact"}[table == "users"]
			for i := 0; i < perG; i++ {
				val := "v" + itoa((g+i)%distinct)
				ev := &Event{Table: table, After: Image{col: val}}
				out, err := tk.Tokenize(ev)
				if err != nil {
					errs <- err
					return
				}
				tok, ok := out.After[col].(string)
				if !ok {
					errs <- fmt.Errorf("g=%d i=%d: token is not string: %T", g, i, out.After[col])
					return
				}
				mu.Lock()
				if prev, exists := valueToken[val]; exists && prev != tok {
					mu.Unlock()
					errs <- fmt.Errorf("g=%d i=%d: value %s mapped to %s and %s", g, i, val, prev, tok)
					return
				}
				valueToken[val] = tok
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// 编号 1..20 必须连续无空洞、无重复。
	if tk.TokenCount("d") != distinct || tk.NextNumber("d") != distinct+1 {
		t.Fatalf("contiguity broken: count=%d next=%d", tk.TokenCount("d"), tk.NextNumber("d"))
	}
	seen := map[string]bool{} // token -> 是否已出现
	for n := 1; n <= distinct; n++ {
		tok := makeToken("d", n)
		if seen[tok] {
			t.Fatalf("duplicate token %s", tok)
		}
		seen[tok] = true
	}
	used := map[string]bool{}
	for _, tok := range valueToken {
		if !seen[tok] {
			t.Fatalf("token outside contiguous range: %s", tok)
		}
		if used[tok] {
			t.Fatalf("two distinct values share token %s", tok)
		}
		used[tok] = true
	}
	if len(valueToken) != distinct || len(used) != distinct {
		t.Fatalf("bijection broken: values=%d tokens=%d", len(valueToken), len(used))
	}
}

func TestLoggingRecordsInputsTokensAndBasis(t *testing.T) {
	var buf bytes.Buffer
	cfg := cloneConfig(testConfig())
	e := cfg.Domains["email"]
	e.TokenLimit = 5
	cfg.Domains["email"] = e
	tk, err := New(cfg, WithLogger(&buf))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tk.Tokenize(&Event{
		Table:  "users",
		Before: Image{"email": "a@example.com"},
		After:  Image{"email": "a@example.com", "nickname": "ali"},
	})
	if err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		`begin event table="users"`,
		`assign domain="email" key=str("a@example.com") token=email-1 basis=first-occurrence`,
		`reuse domain="email" key=str("a@example.com") token=email-1 basis=same-event`,
		`pass-through table="users" after.nickname value=str("ali")`,
		`commit event table="users" new-assignments=1`,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\nfull log:\n%s", want, log)
		}
	}

	// 拒绝路径必须打印回滚且不留痕。
	buf.Reset()
	for i, s := range []string{"x1", "x2", "x3"} {
		if _, err := tk.Tokenize(&Event{Table: "users", After: Image{"email": s}}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	buf.Reset()
	_, err = tk.Tokenize(&Event{Before: Image{"email": "y4"}, After: Image{"email": "y5"}, Table: "users"})
	if !errors.Is(err, ErrTokenLimitExceeded) {
		t.Fatalf("want limit error, got %v", err)
	}
	rejectLog := buf.String()
	if !strings.Contains(rejectLog, "rollback: revoked=1") {
		t.Fatalf("rollback not logged:\n%s", rejectLog)
	}
	if !strings.Contains(rejectLog, "state-unchanged=true") {
		t.Fatalf("state-unchanged marker missing:\n%s", rejectLog)
	}
}

// naiveRef 是规格的朴素参照：单线程、无回滚优化（按事件先试运行再提交），
// Tokenizer 的结果必须与其逐值一致。
type naiveRef struct {
	columns map[ColumnRef]string
	tokens  map[string]map[valueKey]int
	next    map[string]int
}

func newNaiveRef(cfg Config) *naiveRef {
	r := &naiveRef{
		columns: map[ColumnRef]string{},
		tokens:  map[string]map[valueKey]int{},
		next:    map[string]int{},
	}
	for ref, d := range cfg.Columns {
		r.columns[ref] = d
	}
	for d := range cfg.Domains {
		r.tokens[d] = map[valueKey]int{}
		r.next[d] = 1
	}
	return r
}

func (r *naiveRef) run(ev *Event) *Event {
	local := map[string]map[valueKey]int{}
	out := &Event{Table: ev.Table}
	proc := func(img Image) Image {
		if img == nil {
			return nil
		}
		cols := make([]string, 0, len(img))
		for c := range img {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		res := Image{}
		for _, c := range cols {
			v := img[c]
			domain, ok := r.columns[ColumnRef{Table: ev.Table, Column: c}]
			if !ok {
				res[c] = v
				continue
			}
			if v == nil {
				res[c] = nil
				continue
			}
			k, _ := makeValueKey(v)
			if n, ok := r.tokens[domain][k]; ok {
				res[c] = makeToken(domain, n)
				continue
			}
			if n, ok := local[domain][k]; ok {
				res[c] = makeToken(domain, n)
				continue
			}
			n := r.next[domain]
			r.tokens[domain][k] = n
			r.next[domain] = n + 1
			if local[domain] == nil {
				local[domain] = map[valueKey]int{}
			}
			local[domain][k] = n
			res[c] = makeToken(domain, n)
		}
		return res
	}
	out.Before = proc(ev.Before)
	out.After = proc(ev.After)
	return out
}

func TestMatchesNaiveReference(t *testing.T) {
	cfg := Config{
		Domains: map[string]Domain{"d": {Name: "d", TokenLimit: 100000}},
		Columns: map[ColumnRef]string{
			{Table: "users", Column: "email"}:      "d",
			{Table: "accounts", Column: "contact"}: "d",
		},
	}
	tk, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ref := newNaiveRef(cfg)

	tables := []string{"users", "accounts"}
	cols := [][]string{{"email"}, {"contact"}}
	for i := 0; i < 500; i++ {
		table := tables[i%2]
		col := cols[i%2][0]
		val := "v" + itoa(i%37) // 少量不同值，制造大量复用
		var ev *Event
		if i%3 == 0 {
			ev = &Event{Table: table, Before: Image{col: "v" + itoa((i+5)%37)}, After: Image{col: val}}
		} else if i%3 == 1 {
			ev = &Event{Table: table, Before: Image{col: val}}
		} else {
			ev = &Event{Table: table, After: Image{col: val}}
		}
		got, gerr := tk.Tokenize(ev)
		if gerr != nil {
			t.Fatalf("event %d: %v", i, gerr)
		}
		want := ref.run(ev)
		if !eventsEqual(got, want) {
			t.Fatalf("event %d mismatch:\n got=%#v\nwant=%#v", i, got, want)
		}
	}
	if tk.TokenCount("d") != 37 {
		t.Fatalf("distinct-value count mismatch: %d", tk.TokenCount("d"))
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

func TestNullVsEmptyString(t *testing.T) {
	tk := newTestTokenizer(t)

	out, err := tk.Tokenize(&Event{Table: "users", After: Image{
		"email": nil,
		"phone": "",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if out.After["email"] != nil {
		t.Fatalf("NULL must stay NULL, got %v", out.After["email"])
	}
	if out.After["phone"] != "phone-1" {
		t.Fatalf("empty string is a regular value, got %v", out.After["phone"])
	}
	if tk.TokenCount("email") != 0 {
		t.Fatalf("NULL must consume no token, count=%d", tk.TokenCount("email"))
	}
	if tk.TokenCount("phone") != 1 {
		t.Fatalf("empty string must consume one token, count=%d", tk.TokenCount("phone"))
	}

	out2, err := tk.Tokenize(&Event{Table: "accounts", After: Image{"phone": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if out2.After["phone"] != "phone-1" {
		t.Fatalf("empty string must map to stable token, got %v", out2.After["phone"])
	}
	if tk.TokenCount("phone") != 1 {
		t.Fatalf("empty string reused but count changed: %d", tk.TokenCount("phone"))
	}
}

func TestLimitExceededRollsBackWholeEvent(t *testing.T) {
	tk := newTestTokenizer(t) // 各域上限 4

	for i, s := range []string{"s1", "s2", "s3"} {
		if _, err := tk.Tokenize(&Event{Table: "users", After: Image{"email": s}}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	// before 先占 email-4，after 再要 email-5 时超限，整事件（含 email-4）必须撤销。
	ev := &Event{
		Table:  "users",
		Before: Image{"email": "s4"},
		After:  Image{"email": "s5"},
	}
	_, err := tk.Tokenize(ev)
	if !errors.Is(err, ErrTokenLimitExceeded) {
		t.Fatalf("want ErrTokenLimitExceeded, got %v", err)
	}
	if tk.TokenCount("email") != 3 || tk.NextNumber("email") != 4 {
		t.Fatalf("state changed after over-limit reject: count=%d next=%d", tk.TokenCount("email"), tk.NextNumber("email"))
	}

	// 只占一个新编号的重试必须成功并拿到 email-4：撤销后无空洞。
	out, err := tk.Tokenize(&Event{Table: "users", After: Image{"email": "s4"}})
	if err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
	if out.After["email"] != "email-4" {
		t.Fatalf("reused number should be email-4, got %v", out.After["email"])
	}

	_, err = tk.Tokenize(&Event{Table: "accounts", After: Image{"contact": "s6"}})
	if !errors.Is(err, ErrTokenLimitExceeded) {
		t.Fatalf("want ErrTokenLimitExceeded, got %v", err)
	}
	if tk.TokenCount("email") != 4 {
		t.Fatalf("count changed after second over-limit: %d", tk.TokenCount("email"))
	}
}

func TestEmptyImages(t *testing.T) {
	tk := newTestTokenizer(t)
	out, err := tk.Tokenize(&Event{Table: "users"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Table != "users" || out.Before != nil || out.After != nil {
		t.Fatalf("unexpected: %#v", out)
	}
	out2, err := tk.Tokenize(&Event{Table: "users", Before: Image{}, After: Image{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out2.Before) != 0 || len(out2.After) != 0 {
		t.Fatalf("empty images must stay empty: %#v", out2)
	}
}

func TestPassthroughAndInputNotMutated(t *testing.T) {
	tk := newTestTokenizer(t)
	ev := &Event{
		Table: "users",
		After: Image{
			"email":    "a@example.com",
			"nickname": "alice",
			"score":    int64(42),
			"raw":      []byte("bin"),
			"flags":    true,
			"ratio":    1.5,
			"deleted":  nil,
		},
	}
	before := snapshotEvent(ev)

	out, err := tk.Tokenize(ev)
	if err != nil {
		t.Fatal(err)
	}
	want := Image{
		"email":    "email-1",
		"nickname": "alice",
		"score":    int64(42),
		"raw":      []byte("bin"),
		"flags":    true,
		"ratio":    1.5,
		"deleted":  nil,
	}
	if !imagesEqual(out.After, want) {
		t.Fatalf("unexpected output: %#v", out.After)
	}
	if !eventsEqual(ev, before) {
		t.Fatalf("caller event was mutated: %#v", ev.After)
	}
	if out == ev {
		t.Fatal("output must be a new event, not the same pointer")
	}
}

func snapshotEvent(ev *Event) *Event {
	cpImg := func(img Image) Image {
		if img == nil {
			return nil
		}
		c := Image{}
		for k, v := range img {
			c[k] = v
		}
		return c
	}
	return &Event{Table: ev.Table, Before: cpImg(ev.Before), After: cpImg(ev.After)}
}

func eventsEqual(a, b *Event) bool {
	return a.Table == b.Table && imagesEqual(a.Before, b.Before) && imagesEqual(a.After, b.After)
}

func imagesEqual(a, b Image) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok {
			return false
		}
		x, xok := v.([]byte)
		y, yok := w.([]byte)
		if xok || yok {
			if !xok || !yok || !bytes.Equal(x, y) {
				return false
			}
			continue
		}
		if v != w {
			return false
		}
	}
	return true
}
