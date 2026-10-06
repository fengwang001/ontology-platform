# 提交图行归属服务

`ontology` 包提供并发安全的提交载入、可版本化忽略名单和逐行归属查询。

## 快速使用

```go
svc := ontology.NewService()
err := svc.Load(ontology.CommitInput{
    ID:    "c1",
    Files: map[string]string{"main.go": "package main\n"},
})
version, err := svc.AddIgnoreList(nil)
rows, err := svc.Blame("c1", "main.go", version)
```

每行返回 `Attribution{CommitID, Path, Line, Ignored}`。用 `errors.Is` 区分：

- `ErrInvalidArgument`
- `ErrCommitNotFound`
- `ErrListVersionNotFound`
- `ErrPathNotFound`
- 载入还可能返回 `ErrDuplicateCommit`、`ErrParentNotFound`、`ErrInvalidRename`

名单版本从 0 开始，版本 0 是空名单；后续版本由 `AddIgnoreList` 返回。

## 本地验证

当前环境的 Go 位于 `/usr/local/go/bin`，默认构建缓存不可写时使用：

```bash
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache-ontology gofmt -w ontology/*.go
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache-ontology go test -v ./...
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache-ontology go test -race ./...
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache-ontology go vet ./...
```

测试覆盖线性历史、分叉合并、改名穿透、忽略穿透、错误次序、并发串行等价、
冷/热查询计数，以及随机 DAG 与独立朴素模型逐行比对。关键取舍见 `DESIGN.md`。
