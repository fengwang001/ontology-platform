# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 递归目录监视事件归一化

`watchnorm.New(W, P)` 创建归一化器：

- `W` 是监视名额上限。根目录 `""` 永远存在、永远被监视并固定占用 1 个名额。
- 新目录有名额时立即监视；没有名额时保留为已知但未监视目录。
- 删除、挂起重命名到期或 `Overflow` 释放名额后统一补位。补位反复选择已知且未挂起子树之外的字节序最小目录，输出 `Rescan(path)`，直到名额占满或没有可补目录。
- 挂起的 `MovedFrom` 子树会保留其中目录的监视状态并继续占用名额；窗口内 `MovedTo` 只输出 `Renamed(from,to)`，到期则在入口处理中输出 `Deleted(from)` 并丢弃整棵子树。
- 窗口判定为 `now-t >= P`，到期记录按 `(t,cookie)` 升序处理；`Overflow` 丢弃挂起记录但不补发删除事件。
- 无法配对的 `MovedTo` 按新建处理；目录在 `Created` 后额外输出 `Rescan(path)`，因为新目录内容未知。

每个操作严格按以下顺序执行：时钟回退检查（`ErrClock`）、参数检查（`ErrBadArg`）、入口到期和补位、操作自身状态检查与变更。入口到期产生的删除、释放和补位即使随后被状态类错误拒绝也会保留，并随错误一起返回。

公开方法包括 `Created`、`Deleted`、`MovedFrom`、`MovedTo`、`Overflow`、`Tick`。归一化事件为：

- `Created(path)`
- `Deleted(path)`
- `Renamed(from,to)`
- `Rescan(path)`，根重扫使用空路径 `""`

所有公开方法使用互斥锁串行化内部状态，因此可被并发调用；输出等价于某一种合法的调用串行顺序。

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

# 单个用例
go test -run TestSpecBudgetRenameExample .

# 2000 组随机事件序列与朴素实现对照；-v 打印每组输入、输出与状态判定
GOCACHE=/tmp/gocache go test -run TestRandomDifferentialAgainstNaiveModel -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
