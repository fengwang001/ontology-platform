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

## 串行安全网认证器

`Authenticator` 在快照隔离的读集/写集之上实现提交时串行安全网（SSN）认证。

- 构造：`NewAuthenticator(K, H)`，要求 `1 <= K <= 64`、`2 <= H <= 8`；每键初始版本为 `(cs=0, value=0, ps=0, ss=+∞)`，全局提交号 `n=0`。
- 快照：`Begin()` 返回事务号并记录 `s=n`；`Read(k)` 优先返回写缓冲，其次返回事务首次读到的值；否则选择 `cs <= s` 的最新版本并持有该版本对象。
- 前驱高水位：提交候选号为 `c=n+1`，`η` 取读集版本的 `cs`，以及每个写集键当前最新版本的 `cs/ps` 的最大值；读写同一键时使用读集持有的同一版本。
- 后继低水位：`π=min(c, 读集各版本在提交此刻的 ss)`；不能使用读发生时缓存的 `ss`。
- 排除窗口：先检查写集键最新版本 `cs > s` 的写写冲突；随后当且仅当 `π <= η` 时以 SSN 中止，因此差 1（`π=η+1`）通过，相等中止。
- 提交更新：通过后先设置 `n=c`，再把每个读集版本更新为 `ps=max(ps,c)`，把每个写集键原最新版本设置为 `ss=π`（不是 `c`），最后追加 `(cs=c, ps=0, ss=+∞)`；每键仅保留最近 `H` 个链上版本，但读集对象仍存在并继续参与认证。
- 原子性：写写冲突、SSN 中止以及任何被拒绝的 `Read/Write/Commit/Abort` 都不修改提交号、版本链或水位；中止事务不消耗提交号。只读事务也完整提交并消耗提交号。
- 并发：所有公开调用由同一互斥保护，结果等价于某个串行顺序；`Commit` 对每个读集/写集键只获取一次当前版本对象。

本地验证：

```bash
go test -v ./...
go test -race ./...
go test -run TestNaiveRandomSequences -v ./...
```

随机测试使用固定种子重放 2000 组调用序列，逐步对照朴素规则模型的返回值、提交号、版本链、读集持有的已截断版本水位，并对已提交事务的写读、写写、读写依赖图执行暴力无环检测。`-v` 日志包含每步输入、输出、`η/π` 或拒绝原因。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
