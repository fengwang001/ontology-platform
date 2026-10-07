# API 速览

```go
reg := ontology.NewRegistry().
	RegisterObjectType(ontology.ObjectType{
		Name: "Person",
		Validate: ontology.ValidationHook{Run: func(p *ontology.Instance, v ontology.BatchView) string {
			if p.Properties["ban"] != nil {
				return "banned"
			}
			return ""
		}},
	}).
	RegisterObjectType(ontology.ObjectType{Name: "Team"}).
	RegisterLinkType(ontology.LinkType{
		Name: "member", Source: "Person", Target: "Team",
		SrcMax: 2, // 每人最多属于 2 个团队；0 表示不限
	})

s := ontology.NewStore(reg)

// 新建：Create=true 时 Baseline 必须为 0
res, err := s.ApplyBatch(ontology.BatchInput{
	ClientID: "create-people",
	Items: []ontology.BatchItem{
		{ID: "alice", Type: "Person", Create: true, Properties: ontology.Property{"n": "Alice"}},
		{ID: "team-x", Type: "Team", Create: true, Properties: ontology.Property{"n": "X"}},
	},
})

// 更新：逐项声明各自当前版本；链接增删与属性写在同一批，基数按最终镜像判定
_, err = s.ApplyBatch(ontology.BatchInput{Items: []ontology.BatchItem{
	{
		ID: "alice", Type: "Person", Baseline: 1,
		Properties: ontology.Property{"n": "Alice!"},
		LinkDeltas: []ontology.EdgeDelta{
			{Edge: ontology.Edge{Link: "member", Source: "alice", Target: "team-x"}, Add: true},
		},
	},
}})

// 失败分类（互斥，固定顺序：重复声明 > 版本冲突 > 钩子拒绝 > 基数）
var be *ontology.BatchError
if errors.As(err, &be) {
	switch be.Kind {
	case ontology.FailureDuplicateDecl:      // 批次内重复写同一实例
	case ontology.FailureVersionConflict:    // 某项基线落后
	case ontology.FailureValidationRejected: // 结构/钩子拒绝
	case ontology.FailureCardinality:        // 最终镜像基数超限
	}
}

// 线性化读：多个实例在同一线性化点取快照，不会跨批次撕裂
snap := s.Snapshot([]ontology.InstanceID{"alice", "team-x"})

// 判定开销证据与重放日志
_ = res.Record.InstanceTouches // 判定过程中读取过的实例计数（只含本批工作集）
for _, rec := range s.Journal() {
	_ = rec.Baselines         // 逐项基线核对（声明值/观察值）
	_ = rec.Hooks             // 每个钩子的调用与拒绝原因
	_ = rec.CardinalityChecks // 每个端点的度、上限与结论
	_ = rec.Committed
}
```

关键类型：

- `BatchItem{ID, Type, Create, Baseline, Properties, LinkDeltas}`：同一批中
  `ID` 必须互不相同；不同 item 可声明不同 `Baseline`，系统逐项核对。
- `BatchResult{CommitSeq, Versions, Record}`：`CommitSeq` 是只随提交批次
  前进的逻辑时钟。
- `BatchView`：传给钩子的只读最终镜像，`Get`/`Instances` 返回深拷贝，
  `HasEdge` 查询最终边状态。
