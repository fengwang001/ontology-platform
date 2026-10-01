# FAT12 簇链表管理器

`package fat12` 实现一个以 12 位打包字节映像记录簇链的 FAT12 管理器，
分配策略为「先找连续空闲段、找不到再循环下一个适应」，并支持把文件链
整理到最小簇号（`Defrag`）。全部方法均可并发调用，内部用互斥锁串行化，
结果等价于某个串行顺序；相同操作序列重放得到完全相同的映像、句柄与 rover。

## 12 位项的字节布局

- 数据簇数 `C`：`1..4078`（含两端），否则构造整体拒绝。
- 数据簇编号 `2..C+1`，FAT 共 `C+2` 项，项 0、项 1 保留。
- 字节映像长度：`⌈(C+2)×3/2⌉`。
- 项 `n` 的字节偏移 `o = n + ⌊n/2⌋`：
  - `n` 为偶数：低 8 位在字节 `o`，高 4 位在字节 `o+1` 的**低**半字节；
  - `n` 为奇数：低 4 位在字节 `o` 的**高**半字节，高 8 位在字节 `o+1`。
- 写一项只改该项的 12 位，相邻项的 4 位保持不变。
- 值含义：`0` 空闲；`0xFF7` 坏簇；`0xFF8` 及以上为链尾（写入统一用
  `0xFFF`）；其余为下一簇号。构造后项 0=`0xFF8`、项 1=`0xFFF`，其余为 0。

## 分配规则与 rover

`Create(n)` 与 `Extend(h,n)` 的取簇规则相同，初始 `rover=2`：

1. **连续段优先（不环绕）**：在簇号不小于 `rover` 且不超过 `C+1` 的
   范围内，找起点最小的、由 `n` 个连续空闲簇组成的段，找到即整体取走。
2. **循环下一适应（回退）**：连续段找不到时，从 `rover` 起按簇号升序
   循环扫描（走到 `C+1` 之后回到 `2`，每个簇至多扫一次），依次取空闲簇。
   坏簇与已占用簇都不是空闲簇。
3. 取到的簇按取得次序串成链；`Extend` 时旧尾项指向第一个新簇，最后一个
   新簇为链尾（`0xFFF`）。
4. 成功后 `rover = 最后取得簇 + 1`；超过 `C+1` 时回到 `2`。

释放与整理对 rover 的影响：

- `Truncate`/`Delete` 只要释放了簇，`rover = min(rover, 被释放的最小簇号)`；
  没有簇被释放时 `rover` 不变。`MarkBad` 不移动 `rover`。
- `Defrag` 有簇被释放时同样回退；新旧簇集合相同而仅次序不同时 `rover`
  不变；分配到的新簇不会让 `rover` 前进。

## Defrag 规则

设链长为 `L`，`S = 全体空闲簇 ∪ 该文件链上的全部簇`（坏簇不在其中）。
取 `S` 中最小的 `L` 个簇，按升序串成新链，尾项写 `0xFFF`：

- 新链序列与旧链完全相同：不改任何内容，返回原句柄。
- 否则旧链中不在新链里的簇置 0，写入新链；文件句柄改为新链首簇号并返回，
  旧句柄失效（新旧首簇相同则句柄值不变）。`Defrag` 不改变 `Free()`。

## 拒绝顺序（`errors.Is` 可区分）

被拒绝的操作不改变映像、rover 与文件表，按下列顺序只报第一个：

1. 参数非法（`n`/`k < 1`；`MarkBad` 的 `c` 越界）—— `ErrInvalid`
2. 文件不存在（`h` 不是现存文件首簇号，`Defrag` 同）—— `ErrNotFound`
3. 操作自有原因：
   - `Create`/`Extend`：空闲簇数小于 `n` —— `ErrNoSpace`
     （空间不足时连续段与循环两条路径都不尝试）
   - `Truncate`：`k` 大于链长 —— `ErrOutOfRange`
   - `MarkBad`：目标簇非空闲 —— `ErrNotFree`

## API 一览

| 方法 | 说明 |
| --- | --- |
| `New(c int) (*FAT12, error)` | 构造管理器 |
| `Create(n int) (handle int, err error)` | 新建含 n 簇的文件，返回首簇号 |
| `Extend(handle, n int) error` | 在链尾追加 n 簇 |
| `Truncate(handle, k int) error` | 保留链前 k 簇，第 k 簇改链尾 |
| `Delete(handle int) error` | 释放整条链并删除文件 |
| `MarkBad(c int) error` | 把空闲簇标记为坏簇 |
| `Defrag(handle int) (newHandle int, err error)` | 整理到最小簇号 |
| `Image() []byte` | 字节映像副本 |
| `Chain(handle int) ([]int, error)` | 链簇号序列副本 |
| `Free() int` | 空闲簇数（不含坏簇） |
| `Rover() int` | 当前分配游标 |

## 本地验证

需要 Go 1.26+。若 `go` 不在 PATH，或默认构建缓存只读，可显式设置：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache

go test -v ./fat12
go test -race ./...
go vet ./...
gofmt -l .
```

`TestRandomDifferential2000` 重放 2000 组随机操作序列（含 `C=1`、`C=4078`
边界），将实现与按题目规则逐步写成的逐项 `uint16` 朴素模型
（见 `model_test.go`）逐步比对字节映像、句柄、链、`Free` 与 `Rover`，
并校验链互不相交、无环、以链尾结束等不变式。`-v` 日志会打印每步的
输入、输出与判定依据。
