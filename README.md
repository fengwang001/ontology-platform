# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `cardinality/`：链接类型基数上限运行期下调后的级联处理与孤儿清理。
  确定性超额标记（`(登记序号, 链接ID)` 全序）、待处理生命周期与显式
  处置（删除 / 保留并提升有效上限）、上调时按标记逆序整体恢复、
  派生状态的暂时不可信/恢复/清理级联、对象撤销强制删除优先路径、
  并发操作全序串行化，以及完整的审计记录。设计与取舍见
  `cardinality/DESIGN.md`。

快速上手：

```go
e := cardinality.NewEngine(nil)
r1 := e.CreateLink("owns", "obj-1", "target-1", "link-1") // Accepted
e.SetLimit(cardinality.BucketKey{LinkType: "owns", Direction: cardinality.Outgoing, SourceID: "obj-1"}, 1)
// 超额链接进入 pending；可 e.Finalize(id, cardinality.DispositionDelete/DispositionRetain)
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
