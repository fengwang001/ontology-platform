# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 承运商运价合同：运费计算与结算系统

位于 `freight/`，按合同线路、服务等级与左闭右开生效区间管理运价，
按运单揽收时刻选择合同，给出含体积重量、累进阶梯、最低收费、燃油/偏远/超限附加费的
可复现明细，并在结算时固化金额。

- 设计说明（取舍、放弃方案、复杂度论证、并发与重放）：`docs/design.md`
- 使用文档与错误码：`docs/freight.md`
- 可运行演示：`go run ./freight/example`
- 测试（边界、朴素模型随机差分、重放、并发、复杂度证明）：`freight/freighttest/`、`freight/store/`

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
