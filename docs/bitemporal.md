# 双时态链接历史审计 —— 使用文档

## 概念

- 有效时间 `validTime`：链接在业务世界成立/失效的时间。
- 记录时间 `recordTime`：事实被系统知晓并记入只追加日志的时间。
- 物理半：非对称链接为正向 `f` 与反向 `r`；对称链接为 `ab` 与 `ba`。两半在回放时必须互为镜像。

## 典型用法

```go
st := bitemporal.NewStore()

// 登记对象类型（及其诞生的记录时间）与链接类型 + 首版基数规则。
_ = st.RegisterObjectType("Person", 0)
_ = st.RegisterObjectType("Org", 0)
_ = st.RegisterLinkType(
    bitemporal.LinkType{ID: "employed_by", SourceType: "Person", TargetType: "Org"},
    bitemporal.Cardinality{
        Forward: bitemporal.Card{Min: 0, Max: 1}, // 每人至多任职一家组织
        Reverse: bitemporal.Card{Min: 0, Max: 0}, // 组织人数不限
    },
    0) // 规则从记录时间 0 起生效

// 创建/撤销：规范化记录一次即可，正反视角自动镜像。
_ = st.RecordCreate("employed_by", "alice", "acme", 10, 10)
_ = st.RecordRevoke("employed_by", "alice", "acme", 40, 55) // 40 起离职，55 才被知晓

// 规则可随时间出新版本；历史版本不被覆盖。
_ = st.PutRule("employed_by",
    bitemporal.Cardinality{Forward: bitemporal.Card{Max: 2}, Reverse: bitemporal.Card{}},
    100)

snap := st.CurrentSnapshot()

// 1) 任意双时态点回放（这里固定有效时间视角）。
res := snap.Replay("employed_by", 30, 20) // rt=30 时回看 vt=20 的世界
_ = res.Edges   // 当时实际存在的边
_ = res.Defects // 结构性镜像缺陷（如有）

// 2) 正向/反向镜像对账。
fwd, rev, defects := snap.CheckMirror("employed_by", 30, 20)

// 3) 历史区间审计（对角线：记录时刻即有效时刻）。
log := bitemporal.NewDecisionLog()
aud := bitemporal.NewAuditor(st, log)
segs, err := aud.Audit(context.Background(), bitemporal.AuditRequest{
    LinkType: "employed_by",
    RecordStart: 0, RecordEnd: 200,
})
```

返回的每个 `Segment` 自带：

- `RecordStart/RecordEnd`：该结论成立的记录时间半开区间；
- `ValidTime`：判定采用的有效时间（对角线分段为该段起点）；
- `Rule`：该段实际生效的基数约束版本；
- `Violations`：正/反向上超出基数的对象及当时出度。

规则版本在区间内调整时，结果**按版本分段**，不会合并成单一结论。

## 对称链接与历史数据缺陷

```go
_ = st.RegisterObjectType("Friend", 0)
_ = st.RegisterLinkType(
    bitemporal.LinkType{ID: "friend", SourceType: "Friend", TargetType: "Friend", Symmetric: true},
    bitemporal.Cardinality{Forward: bitemporal.Card{Max: 100}, Reverse: bitemporal.Card{Max: 100}},
    0)

// 任一端规范化记录一次，回放自动双向可见。
_ = st.RecordCreate("friend", "a", "b", 10, 10)

// 迁移遗留数据时，若只有一端的物理半事实：
_ = st.IngestLegacyHalf("friend", "a", "b", 10, 10, true, "ab")
// 回放/审计会显式给出 MirrorDefect（缺失 ba 半），而不会静默假装对称。
```

## 审计错误

`Audit` 返回的错误用 `errors.Is` 判断，优先级 `E3 > E2 > E1 > E4`：

- `ErrIntervalContradiction`：区间矛盾；
- `ErrObjectTypeMissing`：端点对象类型/规则在请求时刻尚不存在；
- `ErrRuleVersionSuperseded`：依据的规则版本在审计期间被作废；
- `ErrMirrorStructural`：结构性镜像缺失。

所有错误都不会改变链接历史。

## 判定留痕

`DecisionLog.Records()` 返回只追加的判定记录：请求、快照代次、依据规则版本起点
（`RuleBasis`）、错误或分段结论，供事后核查。

## 复杂度

- 任意 `(recordTime, validTime)` 回放：`O(log^2 F + k)`，不随累计事实量 `F` 线性增长。
- 对角线窗口审计：版本选择 `O(log R)`，违反/缺陷区间枚举 `O(log F + k)`。
- 探针见 `ProbeCounts`，对应验证见 `TestReplaySublinearProbe`、`TestAuditSegmentProbe`。
