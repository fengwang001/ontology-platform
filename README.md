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

## booking：即时配送预约单排程

`booking/` 提供区域时段名额账本、预约生命周期、顺延下单、原子改期、
`start-DispatchLead` 时刻驱动的释放派单，以及单锁并发协调。

- 关键取舍与放弃方案见 `booking/DESIGN.md`。
- 所有拒绝原因由 `booking.Error.Code` 携带（参数非法、时钟回退、
  区域/时段/预约不存在、已取消/已送达/已释放/无需改期、过早/过晚/
  已过改期截止、时段已满），可程序化区分。

```bash
# 边界、超额回落、原子改期、并发不超上限
go test -race ./booking/

# 与全量扫描朴素模型的随机差分（打印每步输入/输出/判定依据）
go test -run TestRandomDifferential -v ./booking/

# 性能证据：顺延搜索/释放判定不随预约总数增长
go test -run xxx -bench . -benchtime 10000x ./booking/
```
