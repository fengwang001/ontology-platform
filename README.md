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

## metering：楼宇抄表记账与公摊分摊

`metering` 包提供楼宇总表与分户表的抄表记账与公摊分摊服务：

- 读数录入（实抄/估抄）、表计翻转与换表、估抄被实抄替代；
- 账期结算（左闭右开），跨边界用量按时刻线性归属、余数归较晚账期；
- 公摊按建筑面积分摊，部分在住按在住时长精确加权，空置分户全额参与；
- 已结算账期的更正重算（链式触发、只记差额、不改原账单）；
- 固定次序的八类业务错误；全局串行化保证并发等价于某串行顺序。

设计取舍与验证方法见 [metering/DESIGN.md](metering/DESIGN.md)。

```bash
go test ./metering/ -v          # 定向用例
go test -race ./metering/       # 并发与差分对照
```
