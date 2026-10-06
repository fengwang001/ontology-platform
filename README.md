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

## 家庭医疗保单多层限额账引擎（limitbook）

`limitbook` 包实现保单与成员登记、保单年度划分、限额批改、理赔扣减与冲正，
在项目年度、个人年度、家庭共享年度与个人终身四层限额约束下给出唯一且可复现的赔付额。

```bash
go test ./limitbook/ -v          # 单元测试 + 朴素模型随机对照（打印判定依据）
go test -race ./limitbook/       # 并发正确性
go test -bench=BenchmarkSettle ./limitbook/  # 结算性能不随历史增长
```

设计取舍与验证方法见 [limitbook/DESIGN.md](limitbook/DESIGN.md)。
