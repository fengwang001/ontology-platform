# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## slots：机场起降时刻协调与历史优先权

`slots/` 是一个自包含的 Go 包，实现单机场单航季的时刻系列申请、截止时
一次性三段式分配（历史优先权 → 新进入者保留额 → 其余申请）、等候名单、
返还补位、系列交换、周执行登记与航季末历史资格结算。规则、取舍与性能
论证见 `slots/DESIGN.md`；`reference.go` 是独立朴素模型，供随机差分对照。

```bash
go test ./slots/                     # 全场景 + 200 组随机差分对照
go test -race ./slots/               # 并发安全
go test -run=NONE -bench=. ./slots/  # O(1) 单元格判定 / O(周数) 使用率
```

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
