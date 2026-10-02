# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## cidpool：QUIC 式连接 ID 池管理器

`cidpool` 包（`cidpool/pool.go`）接收对端通告的连接 ID，按
retire_prior_to 批量退役，受活动数上限与重复/冲突约束，并为每条路径
维护一个互不相同的活动连接 ID。全部操作以互斥锁串行化，可并发调用，
结果等价于某个串行顺序，相同调用序列重放结果完全一致。

### 判定次序（只报第一个）

`OnNew(seq, rpt, cid, token)` 严格按下列顺序判定：

1. **编码错误**（`ErrEncoding`）：`rpt > seq`，或 `cid` 长度不在
   1..20，或 `token` 长度不是 16。
2. **冲突/重复**（`ErrViolation` 或成功无变化）：
   - `seq` 已知且 `(cid, token)` 与记录完全相同 → **重复**：成功返回，
     不改变任何状态，**R（retire_prior_to）也不前进**，条目不入退役队列。
   - `seq` 已知但 `cid` 或 `token` 不同 → `ErrViolation`。
   - `seq` 未知但 `cid` 与任一已知条目（含已退役墓碑）相同 →
     `ErrViolation`。
3. **超限**（`ErrLimit`）：通过下面的推演后活动数大于 L 才报错。

被拒绝的调用不改变条目表、R、退役队列、路径表中的任何一项，也不留墓碑；
因此三类原因 `ErrEncoding` / `ErrViolation` / `ErrLimit` 始终可区分。

### retire_prior_to 的生效与推演次序

先推演 `R' = max(R, rpt)`，再加入新条目：凡 `seq < R'` 的条目（含新条目
自身）都应退役；只有在推演完成后活动数仍不超过 L 才提交（`R = R'`、
写入条目、入队、重分配路径）。超限回滚整个推演，R 不前进。

- `rpt == seq` 时新条目自身不退役（保留）；`rpt == seq+1` 属于
  `rpt > seq`，为 `ErrEncoding`。
- 本次新退役的条目按 **seq 升序**追加到退役队列尾；每个 seq 至多入队一次。

### 迟到项即退役

若新条目的 `seq < R'`（例如对端重发/迟到的旧帧），它加入后立即退役，
作为墓碑永久保留，**不计入活动数**，即使活动数已达 L 也不报 `ErrLimit`，
并按 seq 序进入退役队列。对同一迟到帧的再次重发属于重复，队列不变。

### 路径与重分配次序

- 每条路径占一个活动项，任意时刻没有两条路径占用同一 seq；无项可占时
  路径处于「搁置」状态。
- `OnNew`/`Retire` 导致退役时：占用已退役 seq 的路径**先全部搁置**，再对
  所有搁置路径按 **pathID 升序**，各分配「未被任何路径占用的活动项中
  seq 最小者」；没有可分配项则继续搁置。
- `NewPath` 在空闲项中取最小 seq，没有则 `ErrNoCID`（不创建路径）；
  `FreePath` 释放路径后按同样规则重分配剩余搁置路径。
- `IsReset` 仅当 token 等于某个**活动**项的令牌时为真，已退役项令牌无效。

### 本地验证

```bash
# 全部测试（规格示例 + 2000 组随机朴素模拟对照 + 并发）
go test ./cidpool/ -v

# 竞态检测
go test -race ./...

# 只跑随机对照（2000 组，失败时日志含逐步输入/输出/判定依据）
go test ./cidpool/ -run TestRandomDifferential -v

go vet ./...
gofmt -l .
```

随机对照测试（`cidpool/diff_test.go` 中的朴素模型与
`cidpool/random_diff_test.go`）对同一随机调用序列同时驱动 `Pool` 与按
规则逐条手写的朴素模型，每步之后比对 R、条目表（含墓碑）、活动数、退役
队列、路径分配与 `IsReset`；日志逐步打印输入操作、返回原因（ok /
ErrEncoding / ErrViolation / ErrLimit / ErrArg / ErrNoCID）。若默认
`GOCACHE` 所在目录只读，可指定可写缓存，例如
`GOCACHE=/tmp/gocache go test ./...`。

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
