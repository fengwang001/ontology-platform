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

## cidpool：QUIC 式连接 ID 池管理器

`cidpool` 包（`cidpool/pool.go`）接收对端通告的连接 ID，按
`retire_prior_to` 批量退役，受活动数上限与重复冲突约束，并让多条路径各占
一个互不相同的活动连接 ID。所有操作经互斥锁串行化，并发调用等价于某个
串行顺序；相同调用序列重放得到完全相同的退役队列与路径分配。

### 重复与冲突的区分

- **重复（成功、零副作用）**：`seq` 已知且 `(cid, token)` 与记录完全相同，
  视为同一帧的重发，本次调用不改变任何状态（包括 `R`），成功返回。
- **冲突（`ErrViolation`）**：`seq` 已知但 `cid` 或 `token` 不同；或 `seq`
  未知而 `cid` 与任一已知条目（含已退役的墓碑条目）的 `cid` 相同。已退役
  条目永久保留，其 `cid` 不可被其他 `seq` 复用。

### retire_prior_to 的生效与推演次序

`OnNew(seq, rpt, cid, token)` 按序判定，只报第一个错误，被拒绝的调用不改
变条目表、`R`、退役队列、路径表中的任何一项：

1. **编码错误（`ErrEncoding`）**：`rpt > seq`，或 `cid` 长度不在 1..20。
2. **冲突**：见上（重复直接成功返回，冲突为 `ErrViolation`）。
3. **推演**：`R' = max(R, rpt)`；假想加入新条目后，凡 `seq < R'` 的条目都
   应退役，新条目自身若 `seq < R'` 也立即退役；若推演后活动数超过上限
   `L`，为 `ErrLimit`。

通过后提交：`R = R'`；本次新退役的条目按 `seq` 升序追加到退役队列尾。
注意 `rpt == seq` 合法且新条目保留，`rpt == seq + 1` 是编码错误。

### 迟到项即退役

`R` 只增不减。迟到帧（`seq < R`）即使内容全新，也在入表时立即退役、不计
入活动数，因此即使活动数已达上限也不会触发 `ErrLimit`；其 `seq` 仍按升序
规则进入退役队列。

### 路径重分配次序

- `NewPath(pid)` 分配「未被任何路径占用的活动项中 `seq` 最小者」，没有则
  报 `ErrNoCID` 且不创建路径。
- 任何退役（`OnNew` 批量退役或本端 `Retire`）发生后：占用已退役 `seq` 的
  路径先变为搁置，再对**所有**搁置路径按 `pathID` 升序依次分配最小空闲
  活动项，分完为止，分不到的保持搁置。
- `FreePath(pid)` 删除路径，其占用项回到空闲（不退役），随后同样按
  `pathID` 升序重分配剩余搁置路径。

### 本地验证方法

```bash
# 全部单元测试（含规范示例、边界与 2000 组随机序列对照）
go test ./cidpool/

# 查看每次调用的输入、输出与判定依据日志
go test -v -run TestDifferential ./cidpool/

# 竞态检测下的并发锤击测试
go test -race -run TestConcurrent ./cidpool/
```

`TestDifferential` 用固定种子生成 2000 组随机调用序列，逐条与按规则写成的
朴素模拟（`cidpool/fuzz_test.go` 中的 `model`）对照错误码、退役队列、路径
占用与活动数，并校验不变量：活动数不超过 `L`、每个 `seq` 至多入队一次、
没有两条路径占用同一个 `seq`。
