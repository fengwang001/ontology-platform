// demo 逐项演练两阶段提交协调者的九条语义，每步打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"ontology/coord"
	"ontology/participant"
	"ontology/vote"
)

var failed int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	}
	fmt.Printf("%s %-22s %s\n", verdict, name, detail)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func started(ids ...string) (*coord.Coordinator, *clock) {
	clk := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	c := coord.New(clk.now)
	for _, id := range ids {
		_ = c.Register(id)
	}
	_ = c.Begin(time.Minute)
	return c, clk
}

func main() {
	// 1. 全票提交
	c1, _ := started("a", "b")
	_ = c1.CastVote("a", vote.Agree)
	_ = c1.CastVote("b", vote.Agree)
	v1, _ := c1.Drive()
	s1 := c1.Query()
	check("全票提交", v1.Decision == vote.Commit &&
		s1.Participants["a"] == participant.Committed &&
		s1.Participants["b"] == participant.Committed, v1.String())

	// 2. 一票否决全体中止（已同意者回滚）
	c2, _ := started("a", "b")
	_ = c2.CastVote("a", vote.Agree)
	_ = c2.CastVote("b", vote.Reject)
	v2, _ := c2.Drive()
	s2 := c2.Query()
	check("一票否决全体中止", v2.Decision == vote.Abort && v2.Cause == vote.Rejected &&
		s2.Participants["a"] == participant.Aborted, v2.String())

	// 3. 超时导致中止且原因可区分（now == deadline 即超时）
	c3, clk3 := started("a", "b")
	_ = c3.CastVote("a", vote.Agree)
	clk3.advance(time.Minute)
	v3, _ := c3.Drive()
	check("超时中止原因可区分", v3.Decision == vote.Abort && v3.Cause == vote.TimedOut &&
		v3.Culprit == "b", v3.String())

	// 4. 决议不可改写（迟到的同意票 + 重复驱动）
	_ = c3.CastVote("b", vote.Agree)
	v3b, _ := c3.Drive()
	check("决议不可改写", v3b == v3 && c3.Query().Verdict == v3, v3b.String())

	// 5. 重复指令幂等（提交计数不增）
	_ = c1.SendCommand("a", vote.Commit)
	_ = c1.SendCommand("a", vote.Commit)
	check("重复指令幂等", c1.Query().CommitCounts["a"] == 1, "commit count = 1")

	// 6. 非法转移可判定（三种错误彼此可区分且状态不变）
	p := participant.New("p")
	err1 := p.Commit()
	_ = p.Abort()
	err2 := p.Commit()
	q := participant.New("q")
	_ = q.Prepare()
	_ = q.Commit()
	err3 := q.Abort()
	check("非法转移可判定", errors.Is(err1, participant.ErrNotPrepared) &&
		errors.Is(err2, participant.ErrCommitAfterAbort) &&
		errors.Is(err3, participant.ErrAbortAfterCommit) &&
		p.State() == participant.Aborted && q.State() == participant.Committed,
		"3 种错误可区分且状态不变")

	// 7. 空集合决议（空集上全票同意 vacuously 成立 => Commit）
	c7, _ := started()
	v7, _ := c7.Drive()
	check("空集合决议", v7.Decision == vote.Commit, v7.String())

	// 8. 未裁决查询为零值
	c8, _ := started("a", "b")
	_ = c8.CastVote("a", vote.Agree)
	st8 := c8.Query()
	check("未裁决查询零值", st8.Phase == coord.PhaseVoting && st8.Verdict == (vote.Verdict{}),
		st8.Verdict.String())

	// 9. 恢复重放后一致（状态与提交计数均不变）
	before := c1.Query()
	errReplay := c1.Replay()
	after := c1.Query()
	check("恢复重放后一致", errReplay == nil && reflect.DeepEqual(before, after) &&
		after.CommitCounts["a"] == 1, "状态与计数不变")

	// 10. 并发下决议唯一
	c10, _ := started("x", "y", "z")
	var wg sync.WaitGroup
	seen := make(chan vote.Verdict, 30)
	for _, id := range []string{"x", "y", "z"} {
		wg.Add(1)
		go func(id string) { defer wg.Done(); _ = c10.CastVote(id, vote.Agree) }(id)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, _ := c10.Drive(); v.Decision != vote.None {
				seen <- v
			}
		}()
	}
	wg.Wait()
	close(seen)
	unique := true
	for v := range seen {
		if v.Decision != vote.Commit {
			unique = false
		}
	}
	final, _ := c10.Drive()
	check("并发下决议唯一", unique && final.Decision == vote.Commit, final.String())

	fmt.Printf("TOTAL %d checks, %d failed\n", 10, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
