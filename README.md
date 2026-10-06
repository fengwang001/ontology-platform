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

## 压实服务

分层压实任务选择与安装服务位于 `compaction/` 包，覆盖精确有理评分、零层/非零层起点、端点闭合、下一层纳入、在途改选、直接下移和原子安装。

详细设计、复杂度证明和本地验证命令见 `docs/compaction-design.md`。

```go
service, err := compaction.NewService(compaction.Config{
    Layers:              4,
    ZeroTrigger:         4,
    FirstNonZeroTarget:  64 << 20,
    TargetMultiplier:    10,
}, logger)
if err != nil {
    return err
}
if err := service.RegisterFile(compaction.File{
    ID:     1,
    Layer:  0,
    MinKey: []byte("a"),
    MaxKey: []byte("m"),
    Bytes:  128,
}); err != nil {
    return err
}
result := service.CreatePlan()
if result.Plan == nil {
    // 没有达标层是正常结果；result.RankedScores 和 result.Reason 可解释原因。
    return nil
}
return service.InstallPlan(result.Plan.ID, outputs)
```
