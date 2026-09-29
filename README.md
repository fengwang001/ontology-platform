# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `rollup/`：多级小计的增量维护与撤回。对带两个分组维度的行流按
  明细组、第一维小计、总计三层逐级维护计数与求和，输出带层级号的正负变更日志；
  支持空值分组、撤回归零删除、非法输入整体拒绝（失败不留痕）、并发查询/自检，
  下游按序重放日志与批量重算逐层一致。

 快速示例：

  ```go
  s := rollup.New(0) // <=0 使用默认明细组数上限
  d1, d2 := "A", "x"
  s.Apply(rollup.Increment{Op: rollup.OpAdd, Row: rollup.Row{ID: "r1", Dim1: &d1, Dim2: &d2, Value: 10}})
  s.Apply(rollup.Increment{Op: rollup.OpRemove, Row: rollup.Row{ID: "r1"}})
  _ = s.Total()                 // 总计
  _ = s.Subtotals()             // 第一维小计
  _ = s.DetailGroups()          // 明细组
  if err := s.Verify(); err != nil { /* 三层恒等式/日志重放自检 */ }
  // 下游：NewReplay() 后逐条 ApplyEntry(s.Log())，结果与 BatchRecompute(s.Rows()) 一致
  ```

  详细规则（三层定义、变更日志、空值语义、边界、错误类别、并发模型）
  见 `rollup/DESIGN.md`。

## 环境要求

- Go 1.26+（`go version` 确认）

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./rollup
go test -run TestIncrementalLifecycle -v ./rollup

# 多级小计：带竞态检测与每步输入/输出日志/判定依据
go test -race -v ./rollup

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
