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

## 修订表达式解析与消歧服务（ontology 包）

把用户文本（完整标识、标识前缀缩写、引用短名、带相对导航后缀的表达式
或其组合）唯一解析为对象，并为任一对象计算当前库中唯一的最短缩写。

- 表达式文法：`base ("^"N? | "~"N? | "@{"N"}" | "^{}" | "^{tree}")*`，
  含义依次为取第 N 父、向上 N 代、引用日志第 N 次更早的值、剥标签、取树。
- 消歧次序：全名精确命中 > 命名空间次序补全（与有效标识前缀并存时报
  「引用与标识歧义」，全名豁免）> 标识前缀匹配（前缀歧义/不存在）。
- 并发：读写可任意并发，单次解析等价于看到某一瞬间的完整状态。
- 设计与取舍见 [DESIGN.md](DESIGN.md)。

```go
svc := ontology.NewService(ontology.Config{MinAbbrev: 4,
    Namespaces: []string{"refs/heads/", "refs/tags/"}})
svc.Put(ontology.Object{ID: id, Type: ontology.TypeCommit, Tree: tree})
svc.SetRef("refs/heads/main", id)
res, err := svc.Resolve("main~2^{tree}", ontology.Query{})
abbr, _ := svc.ShortestAbbrev(id)
```
