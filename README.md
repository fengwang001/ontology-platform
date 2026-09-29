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

## 分层合并键值写入路径（`lsm` 包）

`lsm` 包实现“先写内存缓存 → 冻结为不可变段 → 按层合并”的键值路径。

### 写入与冻结

- `Put` / `Delete` 只向内存缓存追加一条带全局单调 `seq` 的记录（`Delete` 追加墓碑）。
- 缓存记录数达到 `MemtableSize` 时，本次写入在发布前把缓存冻结成一个不可变段进入第零层，同键以 `seq` 最大者为准。
- 所有变更（写入、冻结、级联合并、外部加载）都在写锁内基于上一份状态拷贝计算，
  最后通过一次原子指针替换发布；读路径只读取不可变快照，**不持锁**。

### 层合并规则

- 某层段数达到扇出阈值 `Fanout` 时，把该层**最旧的 `Fanout` 个段**合并为一个段进入上一层，并从第零层向上级联。
- 合并段的段号取参与段的**最大段号**（冻结/合并代次单调，层内段号严格升序）。
- 同一键在多个段出现时，**一键取段号最大的段中的那条**；墓碑与普通记录同等参与，不做特殊遮蔽回退。
- 达到 `MaxLevel` 后再触发合并时，合并结果**仍留在最大层**（段号相同，置于剩余段之前），继续滚动合并。

### 墓碑保留规则

- 墓碑只在一种情况下允许被合并丢弃：**该键在全系统不存在任何更早写入**（即在本次合并集之外，
  不存在段号更小的同键记录；外部加载的 floor 基线在语义上永远更早）。
- 只要更早写入还存在（哪怕它在更上层尚未被卷入本次合并，或来自外部加载段），墓碑就必须实体保留，
  避免合并后旧值“复活”。
- 被删除键从未写入过时，其墓碑没有需要遮蔽的对象，合并时安全丢弃；读语义仍然是“不存在”。

### 并发一致性

- 读（`Get` / `GetWithSource`）与自检（`Verify`）可被任意并发调用，且可在写入触发合并期间并发。
- 每次发布的单位是整棵状态（内存缓存 + 所有层 + floor），因此任一读者要么看到某次合并前的完整状态，
  要么看到合并后的完整状态，**不会读到两代记录混合的中间态**。
- `View()` 返回某一时刻的只读快照句柄，可在写入继续推进时反复读取同一个完整状态（快照隔离）。

### 段格式、损坏与截断

段为自描述字节：`magic(4) | id(8) | [ keyLen(4) valueLen(4) flags(1) seq(8) key value crc32c(4) ]*`。

- 非法参数（`MemtableSize/Fanout <= 0`、`MaxLevel < 0`）→ `ErrInvalidArgument`。
- 空键读写 → `ErrEmptyKey`；nil 值 → `ErrInvalidArgument`。原因均可通过 `errors.Is` 区分。
- 尾部不完整记录（固定头残缺或声明长度超出剩余字节）→ 严格解码返回 `ErrSegmentTruncated`，
  `RecoverTruncated` 可安全截断到最后一条完整记录边界，已提交数据不受影响。
- 魔数错误、标志位非法、键非严格升序、CRC 不匹配 → `ErrSegmentCorrupt`，不做猜测性恢复。
- `LoadSegment` 严格解码：任何失败都在状态发布之前返回，**缓存、段与层分布保持不变**。

### 本地验证方法

```bash
# 竞态检测 + 逐步日志（写入、读取结果与每步判定依据）
go test -race -v ./lsm

# 反复压并发读
go test -race -count=10 -run TestConcurrent ./lsm
```

覆盖场景：

- `TestLevelCascade`：多层级联与最大层滚动合并，核对每层段数与最新值。
- `TestTombstoneKeptWhenOlderWriteExists`：外部基线存在更早写入时，墓碑在两次合并后仍实体保留。
- `TestTombstoneDroppedWithoutAnyOlderWrite`：从未写入的键，其墓碑可安全丢弃。
- `TestSegmentTruncationAndCorruption`：尾部截断恢复、魔数/CRC 损坏拒绝、失败不改变状态。
- `TestConcurrentReadsDuringMerges`：合并进行中并发读与自检，快照内读取稳定。
- `TestReplayByTimeOrder`：**按时间顺序重放核对**——导出全部段后，按逻辑年代从旧到新
  （外部 floor 基线 → 最大层到第零层、层内段号升序 → 内存缓存）解码重放，
  同键以 `seq` 最大者为准，重放得到的每个键结果必须与在线 `Get` 一致。
  注意 floor 段号属于外部编号空间，不能只按段号数值排序。
