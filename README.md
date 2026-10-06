# ontology-platform

Go 根包 `enrollment` 实现学期选课约束引擎，支持课程/教学班配置、先修/共修/互斥、学分上限、时间冲突、容量账目、批量选课、级联退课与原子换课。

## 快速开始

```go
engine := enrollment.NewEngine()
_ = engine.AddCourse(enrollment.Course{ID: "cs101", Credits: 3})
_ = engine.AddSection(enrollment.Section{
	ID: "cs101-a", CourseID: "cs101", Capacity: 40, Times: []int{1, 2},
})
_ = engine.SetCreditLimit("alice", 18)

err := engine.EnrollBatch("alice", []enrollment.Request{{
	CourseID: "cs101", SectionID: "cs101-a",
}})
```

错误统一为 `*enrollment.RuleError`，通过 `Code` 区分原因；批量失败时 `Index` 是首个不通过课程的提交下标。`ErrNotEnrolled` 是退课/换课的独立错误，不参与选课拒绝优先级链。

核心 API：

- `AddCourse`、`AddSection`、`SetCapacity`：课程、教学班与容量管理。
- `AddPrerequisite`、`AddCorequisite`、`AddMutualExclusion`：直接先修、对称共修、对称互斥。
- `SetCreditLimit`、`SetHistory`：学生学分上限与既往成绩；成绩等于及格线视为通过。
- `EnrollBatch`：按提交顺序判定、全有或全无提交。
- `DropCourse`：沿共修连通分量级联退课并释放容量。
- `SwitchSection`：以旧班已退出的临时视图判定，失败保留原选择。
- `HasTimeConflict`、`CapacityAvailable`：O(候选班时段数) 与 O(1) 查询。

## 本地验证

当前环境 Go 位于 `/usr/local/go/bin/go`，且默认构建缓存不可写，可使用：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/go-cache

go test ./...
go test -race ./...
go test -run TestRandomOperationsMatchNaiveModel -v
go test -run TestComplexityIndependenceIsObservable -bench . -benchtime=1000x
```

`TestRandomOperationsMatchNaiveModel` 会用固定随机种子驱动高效引擎和独立朴素模型，日志逐步打印输入、双方输出、失败下标与判定依据。

详细设计与被放弃方案见 `DESIGN.md`。
