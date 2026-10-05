package moderation_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/moderation"
)

// naive 是每次查询都全量重算的朴素模拟，用于与增量实现对照。
// 派生量（有效级别、计分、账号状态、结案状态）一律从原始记录现算。
type naive struct {
	p, a, tmax int64
	maxNow     int64
	contents   map[string]string
	decisions  map[string]*nDec
	appeals    map[string]*nApp
}

type nDec struct {
	content, creator, reviewer string
	level                      int
	now                        int64
	overturned, appealed       bool
}

type nApp struct {
	decID, by, origReviewer string
	submitted               int64
	votes                   []nVote
}

type nVote struct {
	reviewer string
	overturn bool
}

func newNaive(p, a, tmax int64) *naive {
	return &naive{
		p: p, a: a, tmax: tmax, maxNow: -1,
		contents:  make(map[string]string),
		decisions: make(map[string]*nDec),
		appeals:   make(map[string]*nApp),
	}
}

func (n *naive) clock(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return moderation.ErrInvalidParam
	}
	if now < n.maxNow {
		return moderation.ErrClockRegression
	}
	return nil
}

// scoreOf 全量扫描所有决定，累加此刻有效且未推翻的权重。
func (n *naive) scoreOf(creator string, now int64) int {
	sum := 0
	for _, d := range n.decisions {
		if d.creator == creator && !d.overturned && now >= d.now && now < d.now+n.p {
			sum += d.level - 1
		}
	}
	return sum
}

func stateOfScore(s int) moderation.State {
	switch {
	case s >= 5:
		return moderation.Banned
	case s >= 3:
		return moderation.Muted
	default:
		return moderation.Normal
	}
}

func (n *naive) publish(now int64, content, creator string) error {
	if content == "" || creator == "" {
		return moderation.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if _, ok := n.contents[content]; ok {
		return moderation.ErrContentExists
	}
	if stateOfScore(n.scoreOf(creator, now)) != moderation.Normal {
		return moderation.ErrAccountRestricted
	}
	n.contents[content] = creator
	n.maxNow = now
	return nil
}

func (n *naive) decide(now int64, id, content string, level int, reviewer string) error {
	if id == "" || content == "" || reviewer == "" || level < 1 || level > 3 {
		return moderation.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if _, ok := n.decisions[id]; ok {
		return moderation.ErrDecisionExists
	}
	creator, ok := n.contents[content]
	if !ok {
		return moderation.ErrContentNotFound
	}
	n.decisions[id] = &nDec{
		content: content, creator: creator, reviewer: reviewer,
		level: level, now: now,
	}
	n.maxNow = now
	return nil
}

func (n *naive) state(creator string, now int64) (moderation.State, error) {
	if creator == "" {
		return moderation.Normal, moderation.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return moderation.Normal, err
	}
	return stateOfScore(n.scoreOf(creator, now)), nil
}

// level 全量扫描该内容的全部决定，取未推翻者的最大 level。
func (n *naive) level(content string) (int, error) {
	if content == "" {
		return 0, moderation.ErrInvalidParam
	}
	if _, ok := n.contents[content]; !ok {
		return 0, moderation.ErrContentNotFound
	}
	level := 0
	for _, d := range n.decisions {
		if d.content == content && !d.overturned && d.level > level {
			level = d.level
		}
	}
	return level, nil
}

func (n *naive) appeal(now int64, appealID, decisionID, by string) error {
	if appealID == "" || decisionID == "" || by == "" {
		return moderation.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if _, ok := n.appeals[appealID]; ok {
		return moderation.ErrAppealExists
	}
	d, ok := n.decisions[decisionID]
	if !ok {
		return moderation.ErrDecisionNotFound
	}
	if by != d.creator {
		return moderation.ErrForbidden
	}
	if d.appealed {
		return moderation.ErrAlreadyAppealed
	}
	if now > d.now+n.a {
		return moderation.ErrAppealExpired
	}
	n.appeals[appealID] = &nApp{
		decID: decisionID, by: by, origReviewer: d.reviewer, submitted: now,
	}
	d.appealed = true
	n.maxNow = now
	return nil
}

// resolution 从投票记录现算结案状态：先扫投票找 2 票同向，否则看超时。
func (n *naive) resolution(a *nApp, now int64) (closed, overturned bool) {
	up, down := 0, 0
	for _, v := range a.votes {
		if v.overturn {
			up++
		} else {
			down++
		}
		if up == 2 {
			return true, true
		}
		if down == 2 {
			return true, false
		}
	}
	if now >= a.submitted+n.tmax {
		return true, false
	}
	return false, false
}

func (n *naive) review(now int64, appealID, reviewer string, overturn bool) error {
	if appealID == "" || reviewer == "" {
		return moderation.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	a, ok := n.appeals[appealID]
	if !ok {
		return moderation.ErrAppealNotFound
	}
	if closed, _ := n.resolution(a, now); closed {
		return moderation.ErrAppealClosed
	}
	if reviewer == a.origReviewer {
		return moderation.ErrRecusal
	}
	for _, v := range a.votes {
		if v.reviewer == reviewer {
			return moderation.ErrDuplicateVote
		}
	}
	a.votes = append(a.votes, nVote{reviewer: reviewer, overturn: overturn})
	if closed, overturned := n.resolution(a, now); closed && overturned {
		n.decisions[a.decID].overturned = true
	}
	n.maxNow = now
	return nil
}

func (n *naive) appealStatus(appealID string, now int64) (bool, bool, error) {
	if appealID == "" {
		return false, false, moderation.ErrInvalidParam
	}
	if err := n.clock(now); err != nil {
		return false, false, err
	}
	a, ok := n.appeals[appealID]
	if !ok {
		return false, false, moderation.ErrAppealNotFound
	}
	closed, overturned := n.resolution(a, now)
	return closed, overturned, nil
}

// errCode 把错误归约为可比较的判定码（判定依据）。
func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, moderation.ErrInvalidParam):
		return "参数非法"
	case errors.Is(err, moderation.ErrClockRegression):
		return "时钟回退"
	case errors.Is(err, moderation.ErrContentExists):
		return "内容已存在"
	case errors.Is(err, moderation.ErrAccountRestricted):
		return "账号受限"
	case errors.Is(err, moderation.ErrDecisionExists):
		return "决定已存在"
	case errors.Is(err, moderation.ErrContentNotFound):
		return "内容不存在"
	case errors.Is(err, moderation.ErrAppealExists):
		return "申诉已存在"
	case errors.Is(err, moderation.ErrDecisionNotFound):
		return "决定不存在"
	case errors.Is(err, moderation.ErrForbidden):
		return "无权"
	case errors.Is(err, moderation.ErrAlreadyAppealed):
		return "已申诉过"
	case errors.Is(err, moderation.ErrAppealExpired):
		return "超过期限"
	case errors.Is(err, moderation.ErrAppealNotFound):
		return "申诉不存在"
	case errors.Is(err, moderation.ErrAppealClosed):
		return "已结案"
	case errors.Is(err, moderation.ErrRecusal):
		return "须回避"
	case errors.Is(err, moderation.ErrDuplicateVote):
		return "重复投票"
	default:
		return "未知:" + err.Error()
	}
}

// TestRandomizedVsNaive 用 1500 组随机操作序列对照增量实现与全量重算朴素模拟，
// 逐操作比较错误判定，并在每个操作后比较全部可观察查询（状态/级别/结案）。
func TestRandomizedVsNaive(t *testing.T) {
	creators := []string{"u0", "u1", "u2"}
	reviewers := []string{"r0", "r1", "r2", "r3", "r4"}
	contents := []string{"c0", "c1", "c2", "c3"}
	decIDs := []string{"d0", "d1", "d2", "d3", "d4", "d5"}
	appIDs := []string{"a0", "a1", "a2", "a3"}

	for seed := int64(0); seed < 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		p := 1 + rng.Int63n(60)
		a := 1 + rng.Int63n(40)
		tmax := 1 + rng.Int63n(40)
		sys, err := moderation.New(p, a, tmax)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		nv := newNaive(p, a, tmax)
		now := int64(0)
		maxNow := int64(-1)

		pick := func(pool []string) string { return pool[rng.Intn(len(pool))] }

		for i := 0; i < 40; i++ {
			// 时钟：大概率前进，小概率回退（触发时钟回退拒绝）。
			if rng.Intn(20) == 0 && now > 0 {
				now -= 1 + rng.Int63n(5) // 小幅回退，触发拒绝后能快速恢复
				if now < 0 {
					now = 0
				}
			} else {
				now += rng.Int63n(8)
			}

			var desc string
			var gotErr, wantErr error
			switch rng.Intn(13) {
			case 0, 1:
				content, creator := pick(contents), pick(creators)
				if rng.Intn(15) == 0 {
					content = "" // 参数非法
				}
				desc = fmt.Sprintf("Publish(now=%d, %s, %s)", now, content, creator)
				gotErr = sys.Publish(now, content, creator)
				wantErr = nv.publish(now, content, creator)
			case 2, 3, 4, 5:
				id, content, level, rv := pick(decIDs), pick(contents), 1+rng.Intn(3), pick(reviewers)
				if rng.Intn(15) == 0 {
					level = rng.Intn(5) // 可能非法 level
				}
				desc = fmt.Sprintf("Decide(now=%d, %s, %s, lv=%d, %s)", now, id, content, level, rv)
				gotErr = sys.Decide(now, id, content, level, rv)
				wantErr = nv.decide(now, id, content, level, rv)
			case 6, 7, 8:
				appID, decID, by := pick(appIDs), pick(decIDs), pick(creators)
				// 偏向用真实创作者申诉，提高受理率。
				if d, ok := nv.decisions[decID]; ok && rng.Intn(4) > 0 {
					by = d.creator
				}
				desc = fmt.Sprintf("Appeal(now=%d, %s, %s, %s)", now, appID, decID, by)
				gotErr = sys.Appeal(now, appID, decID, by)
				wantErr = nv.appeal(now, appID, decID, by)
			default:
				appID, rv, ov := pick(appIDs), pick(reviewers), rng.Intn(2) == 0
				if ap, ok := nv.appeals[appID]; ok {
					switch r := rng.Intn(10); {
					case r == 0:
						rv = ap.origReviewer // 须回避
					case r < 3 && len(ap.votes) > 0:
						rv = ap.votes[rng.Intn(len(ap.votes))].reviewer // 重复投票
					}
				}
				desc = fmt.Sprintf("Review(now=%d, %s, %s, overturn=%v)", now, appID, rv, ov)
				gotErr = sys.Review(now, appID, rv, ov)
				wantErr = nv.review(now, appID, rv, ov)
			}

			if errCode(gotErr) != errCode(wantErr) {
				t.Fatalf("seed=%d op=%02d in=%s\n增量实现=%s\n朴素模拟=%s",
					seed, i, desc, errCode(gotErr), errCode(wantErr))
			}
			if gotErr == nil && now > maxNow {
				maxNow = now
			}
			t.Logf("seed=%d op=%02d 输入=%s 输出=%s 判定依据=%s", seed, i, desc, errCode(gotErr), errCode(gotErr))

			// 每个操作后比较全部可观察查询（查询时刻取已接受的最大时钟）。
			qnow := now
			if maxNow > qnow {
				qnow = maxNow
			}
			for _, c := range creators {
				gs, ge := sys.State(c, qnow)
				ws, we := nv.state(c, qnow)
				if gs != ws || errCode(ge) != errCode(we) {
					t.Fatalf("seed=%d op=%02d State(%s,%d): 增量=(%v,%s) 朴素=(%v,%s)",
						seed, i, c, qnow, gs, errCode(ge), ws, errCode(we))
				}
			}
			for _, c := range contents {
				gl, ge := sys.Level(c)
				wl, we := nv.level(c)
				if gl != wl || errCode(ge) != errCode(we) {
					t.Fatalf("seed=%d op=%02d Level(%s): 增量=(%d,%s) 朴素=(%d,%s)",
						seed, i, c, gl, errCode(ge), wl, errCode(we))
				}
			}
			for _, id := range appIDs {
				gc, go_, ge := sys.AppealStatus(id, qnow)
				wc, wo, we := nv.appealStatus(id, qnow)
				if gc != wc || go_ != wo || errCode(ge) != errCode(we) {
					t.Fatalf("seed=%d op=%02d AppealStatus(%s,%d): 增量=(%v,%v,%s) 朴素=(%v,%v,%s)",
						seed, i, id, qnow, gc, go_, errCode(ge), wc, wo, errCode(we))
				}
			}
		}
	}
}

// 相同操作序列重放结果相同。
func TestReplayDeterministic(t *testing.T) {
	run := func() []string {
		s := newSys(t, 1000, 100, 500)
		out := []string{}
		out = append(out, errCode(s.Publish(0, "c", "u")))
		out = append(out, errCode(s.Decide(10, "d1", "c", 3, "r1")))
		out = append(out, errCode(s.Appeal(20, "a1", "d1", "u")))
		out = append(out, errCode(s.Review(30, "a1", "r2", true)))
		out = append(out, errCode(s.Review(30, "a1", "r3", true)))
		st, _ := s.State("u", 40)
		lv, _ := s.Level("c")
		cl, ov, _ := s.AppealStatus("a1", 40)
		out = append(out, st.String(), fmt.Sprint(lv), fmt.Sprint(cl, ov))
		return out
	}
	first := run()
	for i := 0; i < 20; i++ {
		got := run()
		if fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("重放不一致: %v vs %v", got, first)
		}
	}
}

// 并发调用等价于某个串行顺序：全部操作同一时刻发起，
// 最终不变量（级别=最大生效 level、计分=权重和）必须成立。
func TestConcurrentEquivalentSerial(t *testing.T) {
	s := newSys(t, 1000, 100, 500)
	const contents = 8
	for i := 0; i < contents; i++ {
		wantOK(t, "Publish", s.Publish(0, fmt.Sprintf("c%d", i), fmt.Sprintf("u%d", i%3)))
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("d%d", i)
			content := fmt.Sprintf("c%d", i%contents)
			level := 1 + i%3
			if err := s.Decide(0, id, content, level, fmt.Sprintf("r%d", i%5)); err != nil {
				t.Errorf("Decide %s: %v", id, err)
			}
			if _, err := s.State(fmt.Sprintf("u%d", i%3), 0); err != nil {
				t.Errorf("State: %v", err)
			}
			if _, err := s.Level(content); err != nil {
				t.Errorf("Level: %v", err)
			}
		}(i)
	}
	wg.Wait()
	// 每个内容的决定集合是确定的（d0..d63 各一次），校验最终不变量。
	maxLevel := make([]int, contents)
	score := make([]int, 3)
	for i := 0; i < 64; i++ {
		level := 1 + i%3
		c := i % contents
		if level > maxLevel[c] {
			maxLevel[c] = level
		}
		score[(i%contents)%3] += level - 1
	}
	for c := 0; c < contents; c++ {
		wantLevel(t, s, fmt.Sprintf("c%d", c), maxLevel[c])
	}
	for u := 0; u < 3; u++ {
		wantState(t, s, fmt.Sprintf("u%d", u), 0, stateOfScore(score[u]))
	}
}
