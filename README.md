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

## 动作事务审计溯源子系统

实现在 `ontology/` 包：每次动作执行（提交或整体回退）都在同对象类型内
一条单调、连续、无空洞且不可篡改的审计序列中留痕，仅凭该序列即可独立
重建任意历史时刻状态。

- 设计与取舍（含被放弃方案、复杂度论证）：`ontology/DESIGN.md`
- 包级用法示例：`ontology/doc.go`
- 朴素黄金模型与随机对照：`ontology/naive.go`、`ontology/random_diff_test.go`

```bash
# 全量测试（含竞态；会打印输入/输出/判定依据）
go test -race -v ./ontology

# 仅看复杂度证明（稳态扫描量 vs 朴素全量扫描）
go test -v ./ontology -run TestReplayComplexityBound
```
