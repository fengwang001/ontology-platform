# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 批量导入校验钩子可见性子系统

`batchimport/` 包实现有序批量导入时校验钩子的批内可见性控制：
钩子只能看到「全局已提交状态 + 列表中排在当前记录之前且已通过前置校验的
记录」，支持全有或全无 / 尽力而为两种语义、批次级后置钩子、三类可区分
错误的优先级报告、任意内部并发度下的确定性，以及与批次长度无关的可见性
解析开销。

入口 API：

```go
reg := batchimport.NewRegistry()
reg.RegisterObjectType(&batchimport.ObjectType{
    Name:       "Widget",
    FieldTypes: map[string]string{"amount": "int64"},
    PreHooks:   []batchimport.PreHook{...},
    PostHooks:  []batchimport.PostHook{...},
})

// concurrency 只控制与顺序无关的参数校验阶段并行度（<=1 串行）。
rep := reg.Batch(records, batchimport.AllOrNothing, 4)
// rep.Committed / rep.Err（归一化三类错误）/ rep.Records[i]（逐条清单）
// rep.Records[i].VisibilitySteps 为该记录可见性解析实际访问的已暂存记录数
```

关键设计、被放弃的方案与本地验证步骤见 `docs/design.md`。
测试含顺序可见性、两语义差异、后置钩子整批撤销、并发度一致性、
400 轮随机批量与朴素模型逐条对照，以及可见性开销证明；
`-v` 输出会打印每批输入、实际输出与判定依据。
