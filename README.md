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

## kitchen：商家出餐节奏与压单控制

`kitchen/` 是一个与主服务独立的 Go 包，实现并行制作上限、即时/预约单开工
推定、压单滞回状态机、爆单拒单、商家暂停与可复现的开工次序。

- 设计与取舍：见 `kitchen/DESIGN.md`
- 对外 API：`New(Config)`、`Admit`、`Complete`、`Cancel`、`Pause`、
  `Resume`、`Tick`、`PressureEvents`、`Order`、`Counters`
- 错误用 `errors.As` 取 `*kitchen.Error`，按 `Code`（参数非法 / 时钟回退 /
  订单不存在或已存在 / 未开工 / 已开工 / 已完成 / 商家暂停 / 预约过近 / 爆单）区分
- 随机对照：`go test -run TestDifferentialAgainstNaive ./kitchen/`
  （独立逐秒朴素模型；`KITCHEN_DIFF_VERBOSE=1` 打印每步输入、输出与判定依据）

## 代码检查

```bash
gofmt -l .
go vet ./...
```
