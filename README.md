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

## 自适应跳过压缩分帧流（`ontology` 根包）

实现位于根包：`errors.go`（错误）、`rle.go`（校验和与游程编解码）、
`writer.go`（分块写出器）、`reader.go`（流式校验读取器）。

### 分帧格式

流以标识块开头，标识块允许在流中重复出现。每个块为
`1 字节类型 + 3 字节小端负载长度 + 负载`。

| 类型 | 含义 |
| --- | --- |
| `0xFF` | 标识块：长度必须为 6，负载必须为 ASCII `sNaPpY` |
| `0x00` | 数据块，游程压缩负载 |
| `0x01` | 数据块，原样负载 |
| `0x80..0xFE` | 可跳过块，读取端丢弃负载 |
| `0x02..0x7F` | 保留的不可跳过块，读取端报错 |

数据块负载为 `4 字节小端掩码校验和 + 数据`。未压缩数据每块至多
65536 字节，故负载至多 `65536+4=65540` 字节。

校验和对**未压缩**数据计算：

```
crc   = CRC-32C（Castagnoli）
masked = ((crc >> 15) | (crc << 17)) + 0xa282ead8   // uint32 回绕
```

### 游程压缩（RLE）

控制字节 `c`：

- `c < 128`：后接 `c+1` 个字面字节（1..128）；
- `c >= 128`：把后一个字节重复 `(c-128)+3` 次（3..130）。

压缩器从左扫描，取当前位置的极大相同字节段长度 `r`：

- `r >= 3`：先刷出待出字面缓冲（非空时），再写重复令牌；一个令牌
  至多 130 次，段内余下部分作为新段重新判定。例：131 个 `'a'` =>
  `FF 61 00 61`；133 个 => `FF 61 80 61`；70 个 => `C3 61`；
- `r < 3`：字节逐个放入字面缓冲，满 128 立即刷出；结束刷剩余。

### Writer：自适应跳过规则

`NewWriter(F, S)`，要求 `F >= 1 && S >= 1`，否则 `ErrParam`。
`Write` 累积字节，缓冲恰满 65536 立即成块；`Flush` 强制成块；
`Close` 先 `Flush`，空流只写标识块。输出只取决于字节与
`Flush/Close` 位置，与 `Write` 切分无关。`Bytes()` 返回副本。

对每块（`n` 字节）按序判定：

1. `skip > 0`：原样成块并 `skip--`；减到 0 时置 `probe=true`；
2. 否则 `n < 16`：原样成块，`f/skip/probe/curS` 均不变；
3. 否则压缩得 `comp`：
   - `len(comp) < n - n/8`（整数除法，**恰等不算收益**）：用
     `0x00`，且 `f=0、probe=false、curS=S`；
   - 否则用 `0x01`：
     - `probe=true`（探测失败）：`probe=false`、
       `curS=min(2*curS, 8*S)`、`skip=curS`、`f=0`；
     - `probe=false`：`f++`；`f == F` 时 `skip=curS、f=0`。

`curS` 初值为 `S`；探测成功（该块有压缩收益）立即令 `curS` 回到
`S`。例（F=3,S=4）：连续 3 个 100 字节不可压缩块后 `skip=4`，随后
四块原样，第八块为探测块；探测失败则直接 `skip=8`（不再等 F 次）。

### Reader：错误次序与偏移

`Feed(p)` 只返回本次调用内**完整且校验通过**的块所解出的数据，绝不
输出半块；本次调用出错时，同时返回出错块之前已解出的数据与该错误。
错误为带偏移的 `*FrameError`（可用 `errors.Is` 比较），偏移是**出错
块头第一个字节**在整个流中的位置。错误判定次序：

1. `ErrNoIdentifier`：首块类型不是 `0xFF`（优先于其他判定）；空流
   `Close` 也报它（偏移 0）；
2. `ErrBadIdentifier`：标识块长度不是 6 或负载不是 `sNaPpY`；
3. `ErrReserved`：出现 `0x02..0x7F`；
4. `ErrChunkLen`：数据块负载 `<4` 或 `>65540`，块头收齐即判，不等
   负载；
5. `ErrDecode`：压缩负载令牌截断，或解出超过 65536 字节（先于校验
   和）；
6. `ErrChecksum`：掩码校验和不符；
7. `ErrTruncated`：`Close` 时停在块头或负载中间（偏移为该块头；
   标识块收到一半时它优先于 `ErrNoIdentifier`）。

出错后读取端粘滞，之后 `Feed` 返回同一错误且无输出。可跳过块按
流式丢弃、不缓冲负载；读取端缓冲不超过 65544 字节，非导出计数器
`maxBuffered` 记录峰值。所有方法可并发调用，等价于某个串行顺序；
被拒绝的写入不改变状态；相同操作序列重放字节完全一致。

### 本地验证

```bash
# 需把 Go 加入 PATH（本机为 /usr/local/go/bin），构建缓存若只读请指向 /tmp
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache

go test ./...                       # 全量测试（含 2000 组朴素模型对照）
go test -race -count=1 ./...        # 竞态检测
go test -run TestNaiveModel2000 -v  # 查看输入/输出/判定依据日志
go vet ./... && gofmt -l .
```

测试覆盖：`n-n/8` 取等不压缩、n=15/16 界限、成功清零失败计数、
skip 期间不可压缩与 n<16 不消耗失败计数但消耗 skip、探测失败翻倍并
封顶 8S、探测成功回到 S、probe 期间 n<16 保持 probe、段长
130/131/132/133 切分、字面缓冲恰 128、Flush 改变输出而 Write 切分
不改变、65536 恰成块、空流与零长写入、输出逐字节翻转与逐截断点、
每种切分点分段 `Feed`、错误种类与偏移，以及 2000 组随机输入与独立
朴素模型的字节级对照。
