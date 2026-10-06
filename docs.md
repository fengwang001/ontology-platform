# 使用文档：学分替换与毕业审核引擎

包路径：根包 `audit`（module `ontology`）。

## 数据模型
- `Course{ID, Credits}`：课程主数据与标准学分。
- `PlanVersion`：`PlanID + Version` 唯一标识一个版本；包含 `Root` 要求树、`PassLine` 及格线、`MinTotalCredit` 总学分下限、`MinGPA` 加权均分下限、`TransferCap` 转入学分上限，以及 `SharedCredit` 允许重复计入的叶子对。
- `Requirement`：叶子（`ReqLeaf`）指定 `Courses`、`MinCredits`、`MinCourses` 与是否 `Required`；内部节点（`ReqInternal`）指定 `MinChildren`（子要求至少满足个数）。
- 学生 `Enroll` 时绑定方案版本；只有显式 `SwitchVersion` 才改变绑定，且目标版本不得早于当前版本。

## 操作（`*Engine` 方法）
| 方法 | 说明 |
| --- | --- |
| `AddCourse` / `AddPlan` | 注册课程与方案版本（方案校验 ID 唯一、阈值合法）。 |
| `Enroll(Student)` | 学生绑定入学时方案版本。 |
| `RegisterRecord(Record)` | 登记本校修读记录（课程、学期、学分、成绩）。 |
| `RevokeRecord` | 撤销记录；重复撤销返回 `ErrRecordRevoked`。 |
| `RegisterSubstitution` | 登记替换；须适用目标方案版本（目标课程须出现在该版本要求树），生效学期含等号。 |
| `RegisterTransfer` | 登记转入学分；按登记次序累计，超上限返回 `ErrTransferCap` 且不改状态。 |
| `SwitchVersion` | 显式转入新版本；目标更早返回 `ErrVersionTooOld`。 |
| `Audit(studentID)` | 返回 `AuditResult`；只读、可并发。 |

## 审核语义要点
- 成绩 `>= PassLine`（含等号）才可计入；同一课程只保留最高成绩一次，同分取最早学期，再同取记录 ID 最小。
- 替换后以目标课程身份归入，学分取原记录学分与目标课程标准学分的较小值；修读学期早于生效学期不得替换。
- 一门计入课程至多归入一个叶子；仅 `SharedCredit` 声明的叶子对允许同一课程重复计入。
- 根要求满足采用存在性判断（某一种归入方式即可）；不通过时 `Attrib` 给出所有方式都无法满足、编号最小的要求，及其对最有利归入方式的学分/门数缺口。
- 附加条件：总学分、加权均分（按计入学分加权，含等号）、无未结清不及格必修（有后续及格修读即结清）。

## 错误优先级（由高到低）
`ErrInvalidArgument` > `ErrNotFound` > `ErrRecordRevoked` >
`ErrSubNotApplicable` > `ErrTransferCap` > `ErrVersionTooOld`。
通过类型断言获取错误码：
```go
if opErr, ok := err.(*audit.OpError); ok {
    // opErr.Code
}
```

## 最小示例
```go
e := audit.NewEngine()
e.AddCourse(audit.Course{ID: "A", Credits: 4})
plan := &audit.PlanVersion{
    PlanID: "P", Version: 1,
    Root:         &audit.Requirement{ID: "L1", Kind: audit.ReqLeaf, Required: true,
        Courses: []string{"A"}, MinCredits: 4, MinCourses: 1},
    MinTotalCredit: 4, PassLine: 60, MinGPA: 60, TransferCap: 6,
}
e.AddPlan(plan)
e.Enroll(audit.Student{ID: "s1", PlanID: "P", PlanVersion: 1})
e.RegisterRecord(audit.Record{ID: "r1", Student: "s1", Course: "A",
    Semester: "2023-1", Credits: 4, Grade: 82})
res, _ := e.Audit("s1")
fmt.Println(res.Pass) // true
```
