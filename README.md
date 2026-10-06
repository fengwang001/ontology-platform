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

## 电表换表与读数衔接引擎（`meterengine`）

处理供电点生命周期内多只电表的挂接、换表衔接、翻转判定、估算占位/替代与乱序插入，
支持跨表用电量查询。详见 `meterengine/DESIGN.md`。

```bash
# 全量测试（含 2000 组随机操作与朴素模型差分、竞态检测）
go test -race -count=1 ./meterengine

# 打印随机差分的每条输入、输出与判定依据
go test -v -run TestDifferentialRandom ./meterengine

# 复杂度基准（1k/10k/100k 读数）
go test -bench=. -run=^$ ./meterengine
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
