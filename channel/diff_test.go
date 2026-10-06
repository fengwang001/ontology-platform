package channel_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/channel"
)

// op 是随机生成的一条操作（两个模型接收完全相同的输入）。
type op struct {
	kind     string
	user     string
	target   string
	body     string
	mentions []string
	seq      int64
	upto     int64
	before   int64
	limit    int64
	now      int64
}

func genConfig(rng *rand.Rand) (int64, int64, int) {
	pickWindow := func() int64 {
		switch rng.Intn(4) {
		case 0:
			return int64(1 + rng.Intn(5))
		case 1:
			return int64(1 + rng.Intn(50))
		default:
			return int64(1 + rng.Intn(86400))
		}
	}
	return pickWindow(), pickWindow(), rng.Intn(4)
}

func pickUser(rng *rand.Rand, pool []string) string {
	if len(pool) == 0 {
		return "ghost"
	}
	if rng.Intn(7) == 0 {
		return fmt.Sprintf("x%d", rng.Intn(3))
	}
	return pool[rng.Intn(len(pool))]
}

func genMentions(rng *rand.Rand, pool []string, author string) []string {
	n := rng.Intn(5)
	out := []string{}
	used := map[string]bool{author: true}
	for i := 0; i < n && len(pool) > 0; i++ {
		u := pool[rng.Intn(len(pool))]
		if used[u] {
			continue
		}
		used[u] = true
		out = append(out, u)
	}
	switch rng.Intn(8) {
	case 0:
		if len(out) > 0 {
			out = append(out, out[0])
		}
	case 1:
		out = append(out, "ghost")
	case 2:
		out = append(out, author)
	}
	return out
}

func genBody(rng *rand.Rand) string {
	switch rng.Intn(10) {
	case 0:
		return ""
	case 1:
		return strings.Repeat("好", 4001)
	default:
		return fmt.Sprintf("b%d", rng.Intn(1000))
	}
}

func genOps(rng *rand.Rand, length int) []op {
	pool := []string{"alice", "bob", "carol", "dave", "erin"}
	var ops []op
	now := int64(0)
	present := []string{}
	isPresent := map[string]bool{}

	advance := func() int64 {
		if rng.Intn(8) == 0 {
			now -= int64(rng.Intn(3))
			if now < 0 {
				now = 0
			}
		} else {
			now += int64(rng.Intn(4))
		}
		return now
	}

	for i := 0; i < length; i++ {
		t := advance()
		var o op
		forceJoin := len(present) < 2 && i < 4
		if forceJoin {
			cand := pool[0]
			for _, p := range pool {
				if !isPresent[p] {
					cand = p
					break
				}
			}
			o = op{kind: "join", user: cand, now: t}
		} else {
			switch rng.Intn(12) {
			case 0:
				var cand string
				for _, p := range pool {
					if !isPresent[p] {
						cand = p
						break
					}
				}
				if cand == "" {
					cand = pickUser(rng, present)
				}
				o = op{kind: "join", user: cand, now: t}
			case 1:
				o = op{kind: "leave", user: pickUser(rng, present), now: t}
			case 2:
				o = op{kind: "promote", user: pickUser(rng, present), target: pickUser(rng, present), now: t}
			case 3, 4, 5:
				u := pickUser(rng, present)
				o = op{kind: "send", user: u, body: genBody(rng), mentions: genMentions(rng, present, u), now: t}
			case 6:
				u := pickUser(rng, present)
				o = op{kind: "edit", user: u, seq: int64(1 + rng.Intn(int(length)+3)),
					body: genBody(rng), mentions: genMentions(rng, present, u), now: t}
			case 7:
				o = op{kind: "recall", user: pickUser(rng, present),
					seq: int64(1 + rng.Intn(int(length)+3)), now: t}
			case 8:
				o = op{kind: "markread", user: pickUser(rng, present),
					upto: int64(rng.Intn(int(length) + 4)), now: t}
			case 9:
				o = op{kind: "unread", user: pickUser(rng, present), now: t}
			case 10:
				o = op{kind: "unreadmentions", user: pickUser(rng, present), now: t}
			default:
				o = op{kind: "fetch", user: pickUser(rng, present),
					before: int64(rng.Intn(int(length) + 4)),
					limit:  int64(1 + rng.Intn(110)), now: t}
			}
		}
		ops = append(ops, o)

		switch o.kind {
		case "join":
			if !isPresent[o.user] && o.user != "" {
				isPresent[o.user] = true
				present = append(present, o.user)
			}
		case "leave":
			if isPresent[o.user] {
				isPresent[o.user] = false
				next := present[:0]
				for _, p := range present {
					if p != o.user {
						next = append(next, p)
					}
				}
				present = next
			}
		}
	}
	return ops
}

func errCode(err error) string {
	if err == nil {
		return nOK
	}
	return err.Error()
}

func describeOp(o op) string {
	body := o.body
	if len(body) > 16 {
		body = body[:16] + fmt.Sprintf("…(%d runes)", len([]rune(o.body)))
	}
	return fmt.Sprintf(
		"{kind:%s user:%s target:%s seq:%d body:%q mentions:%v upto:%d before:%d limit:%d now:%d}",
		o.kind, o.user, o.target, o.seq, body, o.mentions, o.upto, o.before, o.limit, o.now)
}

func runReal(c *channel.Channel, o op) (string, string, []channel.Item) {
	switch o.kind {
	case "join":
		return "", errCode(c.Join(o.user, o.now)), nil
	case "leave":
		return "", errCode(c.Leave(o.user, o.now)), nil
	case "promote":
		return "", errCode(c.Promote(o.user, o.target, o.now)), nil
	case "send":
		seq, err := c.Send(o.user, o.body, o.mentions, o.now)
		return fmt.Sprintf("seq=%d", seq), errCode(err), nil
	case "edit":
		return "", errCode(c.Edit(o.user, o.seq, o.body, o.mentions, o.now)), nil
	case "recall":
		return "", errCode(c.Recall(o.user, o.seq, o.now)), nil
	case "markread":
		return "", errCode(c.MarkRead(o.user, o.upto, o.now)), nil
	case "unread":
		v, err := c.Unread(o.user)
		return fmt.Sprintf("v=%d", v), errCode(err), nil
	case "unreadmentions":
		v, err := c.UnreadMentions(o.user)
		return fmt.Sprintf("v=%d", v), errCode(err), nil
	case "fetch":
		items, err := c.Fetch(o.user, o.before, o.limit, o.now)
		return fmt.Sprintf("n=%d", len(items)), errCode(err), items
	}
	return "", "unknown op", nil
}

func runNaive(n *naiveChannel, o op) (string, string, []naiveItem) {
	switch o.kind {
	case "join":
		return "", n.Join(o.user, o.now), nil
	case "leave":
		return "", n.Leave(o.user, o.now), nil
	case "promote":
		return "", n.Promote(o.user, o.target, o.now), nil
	case "send":
		seq, e := n.Send(o.user, o.body, o.mentions, o.now)
		return fmt.Sprintf("seq=%d", seq), e, nil
	case "edit":
		return "", n.Edit(o.user, o.seq, o.body, o.mentions, o.now), nil
	case "recall":
		return "", n.Recall(o.user, o.seq, o.now), nil
	case "markread":
		return "", n.MarkRead(o.user, o.upto, o.now), nil
	case "unread":
		v, e := n.unread(o.user)
		return fmt.Sprintf("v=%d", v), e, nil
	case "unreadmentions":
		v, e := n.unreadMentions(o.user)
		return fmt.Sprintf("v=%d", v), e, nil
	case "fetch":
		items, e := n.fetch(o.user, o.before, o.limit, o.now)
		return fmt.Sprintf("n=%d", len(items)), e, items
	}
	return "", "unknown op", nil
}

func itemsEqual(real []channel.Item, naive []naiveItem) string {
	if len(real) != len(naive) {
		return fmt.Sprintf("len real=%d naive=%d", len(real), len(naive))
	}
	for i := range real {
		r, n := real[i], naive[i]
		if r.Seq != n.seq {
			return fmt.Sprintf("item[%d].seq %d vs %d", i, r.Seq, n.seq)
		}
		if (r.Placeholder != nil) != n.recalled {
			return fmt.Sprintf("item[%d].recalled mismatch", i)
		}
		if n.recalled {
			if r.Placeholder.RecalledBy != n.recalledBy || r.Placeholder.RecalledAt != n.recalledAt {
				return fmt.Sprintf("item[%d] placeholder trace mismatch", i)
			}
			continue
		}
		if r.Message.Author != n.author || r.Message.Body != n.body ||
			r.Message.CreatedAt != n.createdAt || r.Message.EditedAt != n.editedAt ||
			r.Message.EditCount != n.editCount {
			return fmt.Sprintf("item[%d] message fields mismatch", i)
		}
		if strings.Join(r.Message.Mentions, ",") != strings.Join(n.mentions, ",") {
			return fmt.Sprintf("item[%d] mentions %v vs %v", i, r.Message.Mentions, n.mentions)
		}
	}
	return ""
}

// TestRandomDifferential 对至少 1500 组随机操作序列做生产实现与朴素模型的逐步
// 对照。每组使用确定性种子；失败时打印完整输入/输出/判定依据日志以便复现。
// 需要查看全部判定日志时：go test -run TestRandomDifferential -v ./channel
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential test in -short mode")
	}
	const sequences = 1500
	const opsPerSeq = 60

	for s := 0; s < sequences; s++ {
		seed := int64(1_000_000 + s*7919)
		rng := rand.New(rand.NewSource(seed))
		e, r, k := genConfig(rng)
		ops := genOps(rng, opsPerSeq)

		real, err := channel.New(channel.Config{EditWindow: e, RecallWindow: r, MaxEdits: k})
		if err != nil {
			t.Fatalf("seq %d: New: %v", s, err)
		}
		naive := newNaive(e, r, k)

		var logb strings.Builder
		fmt.Fprintf(&logb, "seed=%d config={E:%d R:%d K:%d}\n", seed, e, r, k)

		for idx, o := range ops {
			rv, re, rItems := runReal(real, o)
			nv, ne, nItems := runNaive(naive, o)
			reason := "agree"
			mismatch := re != ne || rv != nv
			if !mismatch {
				if diff := itemsEqual(rItems, nItems); diff != "" {
					reason = "FETCH MISMATCH: " + diff
					mismatch = true
				}
			} else {
				reason = "RESULT MISMATCH"
			}
			fmt.Fprintf(&logb, "  [%02d] %-14s in=%s -> real=(%s,%s) naive=(%s,%s) [%s]\n",
				idx, o.kind, describeOp(o), rv, re, nv, ne, reason)

			if mismatch {
				t.Fatalf("sequence %d seed %d op %d:\nreal=(%s,%s)\nnaive=(%s,%s)\nreason=%s\n\n%s",
					s, seed, idx, rv, re, nv, ne, reason, logb.String())
			}
		}
		if testing.Verbose() {
			t.Logf("sequence %d/%d seed=%d config={E:%d R:%d K:%d}: %d ops agree\n%s",
				s+1, sequences, seed, e, r, k, len(ops), logb.String())
		}
	}
	t.Logf("differential test: %d sequences agree", sequences)
}
