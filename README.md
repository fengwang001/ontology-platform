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

## FAT12 簇链表管理器（`ontology` 包）

`ontology/fat12.go` 实现了一个并发安全的 FAT12 簇链表管理器。构造参数为数据簇数
`C`（`1 <= C <= 4078`，否则整体拒绝）。数据簇编号为 `2..C+1`，FAT 共 `C+2` 项
（项 0、项 1 保留），字节映像长度为 `ceil((C+2)*3/2)`。

### 12 位项的字节布局

每个 FAT 项占 12 位，两项打包成 3 个字节。项 `n` 的偏移为 `o = n + floor(n/2)`：

- `n` 为偶数：低 8 位在字节 `o`，高 4 位在字节 `o+1` 的低半字节（该字节高半字节
  属于相邻奇数项，写偶数项时不得改动）。
- `n` 为奇数：低 4 位在字节 `o` 的高半字节（该字节低半字节属于相邻偶数项），
  高 8 位在字节 `o+1`。

项值含义：`0x000` 空闲；`0xFF7` 坏簇；`0xFF8..0xFFF` 为链尾（本实现统一写入
`0xFFF`）；其余值为下一簇号。构造后项 0 为 `0xFF8`、项 1 为 `0xFFF`，数据项全为
`0`，分配游标 `rover` 初始为 `2`。

### 分配：连续段优先，找不到再循环下一个适应

`Create(n)` 与 `Extend(h, n)` 取簇规则相同：

1. 先在簇号不小于 `rover`、不大于 `C+1` 的范围内（**不环绕**）找起点最小的、由
   `n` 个连续空闲簇组成的段，找到即取该段。因此起点恰等于 `rover` 的段优先于终点
   恰在 `rover` 前一簇的段；连续段不会跨过 `C+1` 环绕。
2. 找不到时退回循环下一个适应：从 `rover` 起按簇号升序循环扫描（走到 `C+1` 之后
   回到 `2`，每个簇至多扫一次）依次取空闲簇。坏簇与已占用簇都不算空闲。

取得的簇按取得次序串成链；`Extend` 的旧尾指向第一个新簇，最后一个新簇写链尾。
成功后 `rover` 为最后取得簇的下一簇号（超过 `C+1` 则回到 `2`）。

### 释放、坏簇与 Defrag

- `Truncate(h, k)` 保留前 `k` 个簇（第 `k` 个改链尾），其余置 `0`；`Delete(h)`
  释放整链并删除文件。只要释放了簇，`rover = min(rover, 被释放簇中最小簇号)`；
  没有簇被释放时 `rover` 不变。
- `MarkBad(c)` 只能把空闲簇置为 `0xFF7`，`rover` 不变。
- `Defrag(h)`：设链长为 `L`，`S` 为全体空闲簇与该文件链上簇的并集（坏簇不在
  其中），取 `S` 中最小的 `L` 个簇按升序组成新链。新链与旧链完全相同则不改动并
  返回原句柄；否则旧链中不在新链的簇置 `0`、写入新链，文件句柄改为新链首簇并
  返回（旧句柄失效；新旧首簇相同则句柄不变）。有簇被释放时
  `rover = min(rover, 最小被释放簇)`；仅次序变化（集合相同）时 `rover` 不变；
  分配新簇不会让 `rover` 前进。`Defrag` 不改变空闲簇数。

### 查询与可复现性

- `Image()` 返回字节映像副本；`Chain(h)` 返回链的簇号序列；`Free()` 返回空闲簇数
  （不含坏簇）；`Rover()` 返回游标。
- 所有方法可并发调用，内部用读写互斥保证等价于某个串行顺序。
- `Image()` 与按逐项 `uint16` 数组朴素编码得到的字节逐字节相同；相同的操作序列
  重放得到完全相同的映像、句柄与 `rover`。

### 拒绝顺序（只报第一个，可用 `errors.Is` 区分）

被拒绝的操作不改变映像、`rover` 与文件表。判定顺序为：

1. 参数非法（`Create`/`Extend` 的 `n < 1`、`Truncate` 的 `k < 1`、`MarkBad` 的
   `c` 不在 `2..C+1`）——`ErrInvalidArgument`；
2. 文件不存在（句柄不是现存文件的首簇号，`Defrag` 同）——`ErrFileNotFound`；
3. 操作自身原因：`Create`/`Extend` 空闲簇数小于 `n` 为 `ErrNoSpace`（此时连续段
   与循环两条路径都不尝试）；`Truncate` 的 `k` 大于链长为 `ErrOutOfRange`；
   `MarkBad` 的簇非空闲为 `ErrClusterNotFree`。

### 本地验证

```bash
# 全部测试（含 2000 组随机操作与独立朴素字节映像模型的逐步对照）
go test ./...

# 查看随机对照的输入 / 输出 / 判定依据日志
go test ./ontology/ -run TestRandomAgainstNaiveModel -v

# 竞态检测
go test -race ./ontology/

# 格式化与静态检查
gofmt -l .
go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
