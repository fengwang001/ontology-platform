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

## 理赔反欺诈工作流（`claims` 包）

指标规则库评分、阈值分流（自动通过/单人复核/双人复核）、复核分配
（FIFO、利益冲突回避、级别与同人校验）、双人一致性判定与仲裁、
空位时限自动通过与撤回终态。设计取舍见 `claims/DESIGN.md`。

```bash
go test ./claims/ -race -v                      # 单测 + 朴素模型差分对照
go test ./claims -bench RequestAssign -benchmem -run '^$'  # 分配性能证明
```
