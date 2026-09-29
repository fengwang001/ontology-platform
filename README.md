# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 部分列更新批内合并（`merger` 包）

把同一批内同一主键的多条插入/更新事件合并成至多一条输出，合并后落表结果与逐条应用原始事件一致且可复现。

- 列值三态严格区分：缺席（`Absent()`）、显式空值（`Null()`）、字符串（`String("")` 与 `Null` 不同）。
- 插入必须给出全部列；更新只含变更列，且变更前镜像的列集合必须与变更列相同。
- 更新接更新：列取并集、同列取后到值、镜像取该列批内首次被更新时的值；插入接更新结果仍为插入且不剔除。
- 更新合并结果中，最终值与批前原值相同的列被剔除；全部剔空则该键不产生写入。
- 每键至多一条输出，顺序等于键在批内首次出现的顺序。
- 非法输入（未知列、缺列插入、未知类型、空键、空更新、镜像列不符、键不存在、键已存在、镜像值不符）整批拒绝，
  九种错误码互不相同，任何拒绝都不改变表。
- `Get` / `Snapshot` / `SelfCheck` 使用读锁，可多执行体并发并与 `Commit` 并发。
- 详细规则、边界与错误类别见 [`merger/DESIGN.md`](merger/DESIGN.md)。

本地验证：

```bash
go test ./...
go test -race -v ./merger
go vet ./...
gofmt -l .
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
