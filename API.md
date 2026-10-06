# API 与规则速查

```go
engine, _ := NewEngine(cfg, levels)
engine.EnterInitialScore(at, teacher, key, score)
engine.ApplyReview(at, studentActor, key)
engine.RejectApplication(at, teacher, key, reason)
engine.ProposeChange(at, teacher, key, proposedScore)
engine.DecideProposal(at, approver, key, approve, reason)
engine.LockSemester(at, locker, semester)
completed, _ := engine.SpecialConfirm(at, highLevelApprover, key, score)
snapshot, _ := engine.SnapshotAt(key, asOf, touchedAt)
avg, ok, _ := engine.SemesterAverageAt(student, semester, asOf, touchedAt)
entries := engine.AuditLog()
```

`Config` 中 `ReviewWindow`、`ApprovalLimit`、`SpecialConfirmTTL` 均为整数时长；终点取等。
`Teachers` 声明课程授课教师，`levels` 声明操作人权限级别。
`SpecialConfirm` 第一次返回 `(false,nil)`，第二人有效确认后返回 `(true,nil)`。
失败时使用 `*Error`，读取 `Code` 可区分八类固定优先级错误。

建议验证命令：

```bash
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go test -v ./...
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go test -race ./...
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go vet ./...
GOCACHE=/tmp/go-build PATH=/usr/local/go/bin:$PATH go test -bench=BenchmarkSnapshot -run '^$' .
```
