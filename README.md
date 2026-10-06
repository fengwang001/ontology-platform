# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## vcs 包：版本库工作区三态状态引擎

`vcs` 对每个路径维护「已提交快照 / 暂存区 / 工作树」三份内容与合并冲突表：

- 十类互斥的普通状态分类 + 四种冲突细分（冲突优先）；忽略规则只作用于未跟踪路径。
- 批式 `Stage` / `Unstage` / `Discard`：全有或全无，支持目录前缀展开，统一错误优先级。
- `Commit` 固化暂存区为新快照，冲突未解决或（未带标志时）无变化则拒绝。
- 全部方法可并发调用，结果等价于某个串行顺序；整体状态列表按代缓存。
- 设计取舍见 [DESIGN.md](DESIGN.md)。

```go
ws := vcs.New(vcs.NewIgnoreSet("*.log", "build/"))
_ = ws.WriteFile("a/b.txt", []byte("hello"))
_ = ws.Stage([]string{"a"})          // 目录前缀展开
seq, _ := ws.Commit(false)           // 固化为新快照
list := ws.Status()                  // 非未变更路径分类（缓存）
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
