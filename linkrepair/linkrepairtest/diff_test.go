package linkrepairtest

import (
	"bufio"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/linkrepair"
)

// 随机损坏快照生成器：覆盖题目要求的全部异常来源。
type generator struct {
	rng     *rand.Rand
	types   []TID
	typeMap map[TID]Type
	objs    []OID
	avail   map[OID]bool
}

func newGenerator(seed int64) *generator {
	rng := rand.New(rand.NewSource(seed))
	ntypes := 1 + rng.Intn(4)
	tms := []string{"alpha", "beta", "gamma", "delta"}
	typeMap := map[TID]Type{}
	types := []TID{}
	for i := 0; i < ntypes; i++ {
		id := TID(tms[i])
		var c Card
		switch rng.Intn(3) {
		case 0:
			c = Card{MaxFrom: 1, MaxTo: 1}
		case 1:
			c = Card{MaxFrom: 1 + rng.Intn(2), MaxTo: 1 + rng.Intn(3)}
		default:
			from := unbounded
			to := unbounded
			if rng.Intn(2) == 0 {
				from = 1 + rng.Intn(3)
			}
			if rng.Intn(2) == 0 {
				to = 1 + rng.Intn(3)
			}
			c = Card{MaxFrom: from, MaxTo: to}
		}
		typeMap[id] = Type{ID: id, Card: c}
		types = append(types, id)
	}

	nobj := 2 + rng.Intn(8)
	objs := make([]OID, 0, nobj)
	for i := 0; i < nobj; i++ {
		objs = append(objs, OID("o"+pad(i)))
	}
	avail := map[OID]bool{}
	for _, o := range objs {
		// 约 20% 对象在本次恢复中不可用。
		if rng.Intn(5) != 0 {
			avail[o] = true
		}
	}
	return &generator{rng: rng, types: types, typeMap: typeMap, objs: objs, avail: avail}
}

func pad(i int) string {
	if i < 10 {
		return "0" + strconv.Itoa(i)
	}
	return strconv.Itoa(i)
}

func (g *generator) snapshot(n int) Input {
	in := Input{Types: g.typeMap, Available: g.avail, Records: make([]Raw, 0, n)}
	for i := 0; i < n; i++ {
		r := Raw{Offset: i, Type: g.types[g.rng.Intn(len(g.types))],
			From: g.objs[g.rng.Intn(len(g.objs))],
			To:   g.objs[g.rng.Intn(len(g.objs))]}
		switch g.rng.Intn(10) {
		case 0:
			r.From = "" // 残留：From 丢失
		case 1:
			r.To = "" // 残留：To 丢失
		case 2:
			r.Type = "" // 类型丢失
		case 3:
			r.Type = "ghost-type" // 未知类型
		case 4:
			// 引用不可用对象
			for _, o := range g.objs {
				if !g.avail[o] {
					if g.rng.Intn(2) == 0 {
						r.From = o
					} else {
						r.To = o
					}
					break
				}
			}
		}
		in.Records = append(in.Records, r)
	}
	return in
}

func toFormal(in Input) linkrepair.Snapshot {
	tm := map[linkrepair.LinkTypeID]linkrepair.LinkType{}
	for id, t := range in.Types {
		tm[linkrepair.LinkTypeID(id)] = linkrepair.LinkType{
			ID: linkrepair.LinkTypeID(id),
			Cardinality: linkrepair.Cardinality{
				MaxFrom: t.Card.MaxFrom, MaxTo: t.Card.MaxTo,
			},
		}
	}
	am := map[linkrepair.ObjectID]bool{}
	for o, ok := range in.Available {
		am[linkrepair.ObjectID(o)] = ok
	}
	rs := make([]linkrepair.RawRecord, len(in.Records))
	for i, r := range in.Records {
		rs[i] = linkrepair.RawRecord{
			Offset: r.Offset, Type: linkrepair.LinkTypeID(r.Type),
			From: linkrepair.ObjectID(r.From), To: linkrepair.ObjectID(r.To),
		}
	}
	return linkrepair.Snapshot{LinkTypes: tm, AvailableObjects: am, Records: rs}
}

// TestDifferentialAgainstNaive 在大量随机损坏集合上对照正式实现与朴素模型，
// 并把每次裁决的输入、输出与判定依据写入 testdata/adjudication-log.txt。
func TestDifferentialAgainstNaive(t *testing.T) {
	logPath := filepath.Join("testdata", "adjudication-log.txt")
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	const cases = 2000
	for c := 0; c < cases; c++ {
		seed := int64(1000 + c)
		g := newGenerator(seed)
		in := g.snapshot(1 + g.rng.Intn(40))

		naive := NaiveRepair(in)
		formal := linkrepair.Repair(toFormal(in))

		diff := compareAndLog(w, c, seed, in, naive, formal)
		if diff != "" {
			_ = w.Flush()
			t.Fatalf("case %d seed %d mismatch:\n%s", c, seed, diff)
		}
	}
}

func compareAndLog(w *bufio.Writer, caseNo int, seed int64, in Input,
	naive NaiveResult, formal linkrepair.Result) string {
	fmtline := func(s string) { _, _ = w.WriteString(s) }

	fmtline("==== case " + strconv.Itoa(caseNo) + " seed=" + strconv.FormatInt(seed, 10) + "\n")
	typeSpecs := make([]string, 0, len(in.Types))
	for _, ty := range in.Types {
		typeSpecs = append(typeSpecs, string(ty.ID)+
			"(maxFrom="+capName(ty.Card.MaxFrom)+",maxTo="+capName(ty.Card.MaxTo)+")")
	}
	sort.Strings(typeSpecs)
	fmtline("types: " + strings.Join(typeSpecs, ", ") + "\n")

	avail := make([]string, 0, len(in.Available))
	for o, ok := range in.Available {
		if ok {
			avail = append(avail, string(o))
		}
	}
	sort.Strings(avail)
	fmtline("available: " + strings.Join(avail, ",") + "\n")

	formalKept := map[L]bool{}
	for _, l := range formal.Kept {
		formalKept[L{TID(l.Type), OID(l.From), OID(l.To)}] = true
	}

	var diffs strings.Builder
	formalReason := map[int]string{}
	for _, d := range formal.Dropped {
		formalReason[d.Record.Offset] = string(d.Reason)
	}

	recs := append([]Raw(nil), in.Records...)
	sort.Slice(recs, func(i, j int) bool { return recs[i].Offset < recs[j].Offset })
	for _, r := range recs {
		nr := naive.Reason[r.Offset]
		fr := formalReason[r.Offset]
		outcome := "KEPT"
		basis := "survives all four adjudication levels"
		if nr != "" {
			outcome = "DROPPED"
			basis = nr
		}
		fmtline("  [#" + strconv.Itoa(r.Offset) + "] " +
			recRepr(r) + " -> " + outcome + " (" + basis + ")\n")

		if nr == "" {
			nlink := L{r.Type, r.From, r.To}
			if nr == "" && !naive.Kept[nlink] {
				// 重复记录在朴素模型中不带 Kept，但有原因；此处无需处理。
			}
		}
		if nr != fr {
			diffs.WriteString("  reason offset " + strconv.Itoa(r.Offset) +
				": naive=" + blankDash(nr) + " formal=" + blankDash(fr) +
				" rec=" + recRepr(r) + "\n")
		}
	}

	if len(formalKept) != len(naive.Kept) {
		diffs.WriteString("kept set size differs\n")
	}
	for l := range naive.Kept {
		if !formalKept[l] {
			diffs.WriteString("naive kept but formal dropped: " + linkRepr(l) + "\n")
		}
	}
	for l := range formalKept {
		if !naive.Kept[l] {
			diffs.WriteString("formal kept but naive dropped: " + linkRepr(l) + "\n")
		}
	}

	keptList := make([]string, 0, len(formalKept))
	for l := range formalKept {
		keptList = append(keptList, linkRepr(l))
	}
	sort.Strings(keptList)
	fmtline("kept: " + strings.Join(keptList, ", ") + "\n")
	fmtline("stats: input=" + strconv.Itoa(formal.Stats.Input) +
		" kept=" + strconv.Itoa(formal.Stats.Kept) +
		" malformed=" + strconv.Itoa(formal.Stats.Malformed) +
		" refUnavailable=" + strconv.Itoa(formal.Stats.RefUnavailable) +
		" cardinality=" + strconv.Itoa(formal.Stats.Cardinality) +
		" duplicate=" + strconv.Itoa(formal.Stats.Duplicate) +
		" conflictGroups=" + strconv.Itoa(formal.Stats.ConflictGroups) +
		" conflictGroupWork=" + strconv.Itoa(formal.Stats.ConflictGroupWork) + "\n\n")

	return diffs.String()
}

func blankDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func capName(c int) string {
	if c == unbounded {
		return "*"
	}
	return strconv.Itoa(c)
}

func recRepr(r Raw) string {
	return string(r.Type) + ":" + string(orQ(r.From)) + "->" + string(orQ(r.To))
}

func linkRepr(l L) string { return string(l.Type) + ":" + string(l.From) + "->" + string(l.To) }

func orQ(o OID) OID {
	if o == "" {
		return "<missing>"
	}
	return o
}
