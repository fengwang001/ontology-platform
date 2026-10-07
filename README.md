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

## 聚合视图子系统（`ontology/aggview`）

跨对象类型的增量聚合视图：以一种对象类型为分组，被聚合实例经由声明的
链接类型归属分组，对同组实例的数值属性求和，并维护参与实例计数。

- 多分组归属策略必须显式声明：`PolicyFull`（每组贡献完整值）或
  `PolicyEvenShare`（在当前归属组间均分）。
- 属性写入、链接增删、实例删除的聚合调整在同一处理单元内完成，失败整体回滚。
- 单次归属改变只触及常数份聚合结果（原分组一份、新分组一份）。
- 全部写操作可串行化；归属迁移用单调版本做乐观并发控制，冲突返回
  `KindConflict` 且无副作用。
- “属性不存在”与“取值为零”严格区分（前者贡献为零且不计数）。
- 数值使用 `math/big.Rat` 精确计算。

完整设计、关键取舍、被放弃方案与测试映射见
[`docs/aggview-design.md`](docs/aggview-design.md)。

```bash
# 功能 + 随机对拍 + 并发（竞态检测）
go test -race -v ./ontology/aggview/
```
