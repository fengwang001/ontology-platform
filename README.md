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

## 链接基数约束与批量导入子系统

`ontology` 包实现链接类型两端基数上限（恰好一 / 至多一 / 具体正整数 /
无限制）的严格校验，以及全有或全无、尽力而为两种批量导入语义：

- 基数账本按「链接类型 + 侧 + 实例」做增量计数，单次校验固定访问两个
  计数器，开销不随链接总数增长（由探针测试在 10 与 1000 条规模下验证恒为 2）。
- 批内占用按列表顺序累积；全有或全无失败时逆序回滚，外部不可见中间态；
  尽力而为逐条返回顺序一致的接受/拒绝清单。
- 错误归一化为四类：参数非法、起点一侧超限、终点一侧超限、链接不存在，
  创建按固定次序只报第一个命中原因。
- 单一互斥临界区串行化创建/删除/批量，删除释放名额与删除同时刻对后续
  并发创建可见；随机序列与「每次全量重数」的朴素模型逐条差分对账。

关键取舍、被放弃方案与验证矩阵见 `docs/DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
