# 三态版本库工作区状态引擎

本包实现一个内存版本库工作区状态模型，对同一路径维护已提交快照、暂存区、工作树三份内容，并支持多阶段合并冲突、原子批次操作、提交和互斥状态分类。

## 模块说明

- `path.go`：路径规范化、目录前缀和祖先后代冲突。
- `model.go`：状态枚举、错误、三份内容和前缀引用索引。
- `engine.go`：外部写入、状态分类、单路径查询和列表缓存。
- `ops.go`：暂存、撤销、丢弃、合并冲突引入、冲突解决与提交。
- `DESIGN.md`：关键取舍、被放弃方案和性能论证。

## 基本用法

```go
engine, err := statusengine.NewEngine(statusengine.Snapshot{
    "README.md": []byte("hello\n"),
}, nil)
if err != nil {
    panic(err)
}

if err := engine.WriteWorktree("README.md", []byte("updated\n")); err != nil {
    panic(err)
}
entry, err := engine.Status("README.md")
if err != nil {
    panic(err)
}
fmt.Println(entry.Status) // 工作树修改

if err := engine.Stage("README.md"); err != nil {
    panic(err)
}
id, err := engine.Commit(false)
```

忽略规则通过第二个参数注入，只对未跟踪路径生效：

```go
engine, err := statusengine.NewEngine(nil, func(path string) bool {
    return path == "tmp/local.log"
})
```

合并冲突通过三阶段内容引入：

```go
err := engine.BeginMerge(map[string]statusengine.Stages{
    "file.txt": {
        Base:   []byte("base"),
        Ours:   []byte("ours"),
        Theirs: []byte("theirs"),
    },
})
```

对冲突路径调用 `Stage` 即视为解决；工作树不存在时解决为删除。

## 状态

普通状态共十类：未变更、已暂存新增、已暂存修改、已暂存删除、工作树修改、工作树删除、未跟踪、暂存后又修改、暂存删除后又重建、被忽略。

冲突优先于普通状态，细分为：内容冲突、本方删除对方修改、本方修改对方删除、双方新增。

## 批次与错误

`Stage`、`Unstage`、`Discard` 均接收一批路径，也可以传目录前缀。批内重复路径、非法路径或任一目标失败都会让整批调用完全不生效。

错误优先级固定为：

1. 参数非法
2. 路径不存在
3. 冲突中不可撤销
4. 未跟踪需强制
5. 存在未解决冲突
6. 无可提交内容

所有错误可用标准 `errors.Is` 匹配；`PathError` 会携带出错路径。

## 环境与验证

- Go 1.26+（`go version` 确认）

测试日志会打印每个判定的输入、实际输出和判定依据。

```bash
go test ./...

go test -race -v ./...
go test -bench . -benchmem
go vet ./...
gofmt -l .
```
