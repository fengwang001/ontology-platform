# grading — 阅卷任务分配与双评仲裁引擎

详见 `DESIGN.md`。

## 快速开始

```go
eng := grading.NewEngine(grading.Config{Threshold: 10, Step: 2})
eng.AddGrader(grading.Grader{ID: "a", Group: "A", DailyQuota: 80})
eng.AddGrader(grading.Grader{ID: "b", Group: "B", DailyQuota: 80})
eng.AddGrader(grading.Grader{ID: "c", Group: "C", DailyQuota: 80})
eng.AddPaper(grading.Paper{ID: "p1", Question: "q", MaxScore: 100, Student: "s1"})
for _, t := range eng.Tasks("p1") {
    _ = eng.Submit(t.ID, 60)
}
fv, ok := eng.FinalScore("p1")
```

## API 概览

- `NewEngine(Config)`：配置阈值（恰等算一致）与最小步长。
- `AddGrader(Grader)` / `AddPaper(Paper)`：录入；新增评卷人会领走待分配答卷。
- `Submit(taskID, score)`：提交；分差超阈值自动生成第三组仲裁任务。
- `Withdraw(taskID)`：提交前撤回并立即重分配，原评卷人永久排除。
- `Deactivate(graderID)`：停用；待提交任务视同撤回，已提交评分保留。
- `FinalScore(paperID)` / `Tasks(paperID)` / `Pending()` / `Load(graderID)`：只读查询。
- `Audit()`：按发生次序返回审计（被拒操作不入审计）。

## 测试

```bash
go test -race -count=1 ./...
go test -run TestDifferentialRandom -v ./grading/
```

`TestDifferentialRandom` 与独立写成的朴素模型对照随机操作序列，
日志逐行打印每步输入、输出与判定依据。
