package main

import (
	"fmt"
	"time"

	"ontology/review"
)

func main() {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	svc := review.NewService(t0)
	at := t0
	step := func(d time.Duration) time.Time { at = at.Add(d); return at }

	for i := int64(1); i <= 7; i++ {
		must(svc.AddExpert(step(time.Minute), i, fmt.Sprintf("单位%c", 'A'+i%3), map[int64]string{1: "计算机", 2: "计算机", 3: "机械", 4: "机械", 5: "材料", 6: "材料", 7: "材料"}[i]))
	}
	must(svc.AddApplicant(step(time.Minute), 100, "单位X"))

	rid, panel, err := svc.CreateReview(step(time.Minute), 100, 5, map[string]int{"计算机": 1, "机械": 1})
	must(err)
	fmt.Printf("评审 %d 抽取评委组: %v\n", rid, panel)

	must(svc.Vote(step(time.Minute), rid, panel[0], 1, review.ChoiceApprove))
	must(svc.Vote(step(time.Minute), rid, panel[1], 1, review.ChoiceApprove))
	must(svc.Vote(step(time.Minute), rid, panel[2], 1, review.ChoiceApprove))
	must(svc.Vote(step(time.Minute), rid, panel[3], 1, review.ChoiceApprove))
	must(svc.Vote(step(time.Minute), rid, panel[4], 1, review.ChoiceAbstain))

	view, _ := svc.ReviewView(rid)
	fmt.Printf("第一轮后状态: %s, 结果: %v, 公示截止: %v\n", view.Status, view.Result, view.PublicityEnd)

	must(svc.AddExpert(step(time.Minute), 8, "单位Y", "材料"))
	must(svc.AddRelation(step(time.Minute), 100, panel[0]))
	view, _ = svc.ReviewView(rid)
	fmt.Printf("回避替补后状态: %s, 现任评委: %v, 版本数: %d\n", view.Status, view.Members, len(view.Versions))

	must(svc.Vote(step(time.Minute), rid, view.Members[4], 1, review.ChoiceApprove))
	view, _ = svc.ReviewView(rid)
	fmt.Printf("替补补投后状态: %s, 结果: %v\n", view.Status, view.Result)

	must(svc.FileObjection(step(time.Hour), rid, "程序异议"))
	must(svc.RuleObjection(step(time.Hour), rid, false))
	view, _ = svc.ReviewView(rid)
	fmt.Printf("异议不成立, 状态: %s (公示期满生效)\n", view.Status)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
