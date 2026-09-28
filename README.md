# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 组件

### `incjoin` — 两表内连接的增量差分维护

`incjoin` 包按批次对两张多重集表做等值内连接并输出差分（批后连接结果减批前）：

- 结果元组 `(key, 左值, 右值)` 的重数 = 两侧匹配行重数之积；
- 一批只输出差分非零的元组，按 key/左值/右值有序；下游顺序应用差分恒等于全量重算；
- 删除不存在的行（批后负重数）、变更符号为 0、空键/空值、结果元组数超限均以
  可区分的原因拒绝整批，被拒批不改变两张表与物化结果；
- `Apply` 支持并发（写锁串行化），并发读取只看到完整已提交批，同一序列输出确定。

详见 [`incjoin/README.md`](incjoin/README.md)。

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

# incjoin 脚本化演示（正常差分 + 各类拒绝 + 输入/输出/判定依据日志）
go run ./cmd/demo
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./incjoin
go test -run TestDiffAgreesWithFullRecompute ./incjoin

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
