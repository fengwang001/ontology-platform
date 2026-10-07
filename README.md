# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 本体链接图分页遍历器

`ontology` 包实现按「最大深度 × 单节点最大扇出」两个独立上限、带快照续读与
三态截断标记的邻域分页遍历。设计取舍、被放弃方案与验证方法见 [`DESIGN.md`](./DESIGN.md)。

```go
s := ontology.NewStore()
acl := ontology.NewACL(s)
it := ontology.NewIterator(s, acl)

page, err := it.Traverse(startID, 3, 20, "", ontology.Actor{ID: "alice"})
// 翻页：把 page.NextToken 原样传回；同一标记可安全重复使用，
// 始终锚定首次请求时的图版本，直到 page.Done == true。
```

保证要点：

- 扇出截断优先于深度截断；被扇出剪掉的对象不会经任何路径或续读标记复活。
- 权限过滤先于扇出计数：无存在性权限的对象、无遍历权限的链接不占扇出名额。
- 续读标记为 HMAC 签名的自描述快照位置；图变更后续读不报错、不混入新数据；
  仅当锚定起点对象在当前状态已不存在时返回 `ErrTokenObsolete`。
- 拒绝次序：参数非法 > 起点无权限（`ErrForbidden`）> 标记不可追溯（`ErrTokenObsolete`）。
- `Page.Metrics()` 提供单次请求实际访问对象/链接/权限判定数的内部可验证度量。

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
