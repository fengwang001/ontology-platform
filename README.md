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

## 理赔反欺诈工作流模块

`ontology/` 包实现理赔反欺诈评分与人工复核工作流：指标规则库、案件评分
（受理时固化）、阈值分流（自动通过/单人复核/双人复核）、复核分配（利益冲突
回避、级别与同人约束）、双人一致性判定与仲裁、时限自动结论、撤回与拒绝次序。
设计与取舍见 [DESIGN.md](DESIGN.md)。

```bash
# 单元测试 + 朴素模型随机对照（-v 打印输入/输出/判定依据）
go test ./ontology/
go test -race ./ontology/
go test -run TestDifferentialRandom -v ./ontology/

# 性能证明：申请分配开销不随已分配/已终态案件数增长
go test -bench . -benchtime=100000x ./ontology/
```
