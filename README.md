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

## 信用卡账户引擎（creditcard 包）

`ontology/creditcard` 实现信用卡账户的出账、计息、还款分配与滞纳金：
三类余额（取现/分期/购物）独立计息、购物类免息资格判定、最低还款额、
到期判定、还款即时分配与溢缴款。设计取舍与验证方法见
[creditcard/DESIGN.md](creditcard/DESIGN.md)。

```bash
go test ./creditcard/                # 单元 + 朴素模型随机对照
go test -race ./creditcard/          # 并发
go test -run xxx -bench . ./creditcard/
```
