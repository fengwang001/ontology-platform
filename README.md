# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 群聊频道消息服务

频道消息序号、编辑/撤回与已读水位服务位于 `channel/` 包：连续无洞序号、
撤回占位、按当前提及集合归属未读、单调读水位、成员进出下的精确可见性。
设计取舍与本地验证方法见 [DESIGN.md](DESIGN.md)。

```go
c, _ := channel.New(channel.Config{EditWindow: 300, RecallWindow: 120, MaxEdits: 5})
c.Join("alice", 0)                 // 首个加入者为管理员
seq, _ := c.Send("alice", "hi", []string{"bob"}, 1)
c.UnreadMentions("bob")            // 索引区间查询，不随历史线性增长
```

核心复杂度：`Unread`/`UnreadMentions` 为 Fenwick 区间查询 `O(log N)`，
`MarkRead` 为 `O(1)`，`Send` 的成员相关循环只与提及人数有关。
测试含边界用例、与独立朴素模型的 1500 组随机差分对照、`-race` 并发测试
与确定性重放。

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
