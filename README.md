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

## 差异记录重放校验组件（`replay` 包）

灾难恢复场景：给定灾难前的良好快照与一份差异记录（变化序列 + 声明的
目标状态），判定重放后能否确证与灾难发生时刻的状态等价。

```go
snap := replay.NewSnapshot(schema, objects, links) // 自动派生索引
log := &replay.DeltaLog{Changes: changes, TargetSchema: ts, TargetObjects: to, TargetLinks: tl}

v := replay.NewVerifier()
res := v.Verify(snap, log) // 不修改 snap 与 log，可并发调用
switch res.Verdict {
case replay.VerdictEquivalent:         // 重放结果与声明目标等价
case replay.VerdictInconsistentDelta:  // 差异记录自身不自洽（res.ChangeIndex 定位）
case replay.VerdictPreconditionFailed: // 良好快照不满足重放前提
case replay.VerdictNotEquivalent:      // 不等价（res.Mismatches 按 结构/对象/链接 分类）
}
```

设计说明（关键取舍、被放弃的方案、复杂度证明与本地验证方法）见
[replay/DESIGN.md](replay/DESIGN.md)。
