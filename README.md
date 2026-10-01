# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## IBLT 集合对账草图

`iblt.go` 实现了可逆布隆查找表（Invertible Bloom Lookup Table）集合对账草图：
两个副本各自把键集合写入草图，逐格相减后即可剥离出两侧各自独有的键。

- 草图含 `m` 个格子（`m` 必须为 3 的正整数倍），分为三段、每段 `m/3` 格。
- 每格保存三元组 `(count int64, keyXor uint64, checkXor uint64)`，键类型为 `uint64`。

### 位置与校验的计算

混合函数（乘法按 uint64 回绕）：

```text
mix(z):  z ^= z>>30; z *= 0xBF58476D1CE4E5B9;
         z ^= z>>27; z *= 0x94D049BB133111EB; z ^= z>>31
```

- 键 `x` 的第 `i`（`i=0,1,2`）个位置：
  `i·(m/3) + mix(x + (i+1)·0x9E3779B97F4A7C15) mod (m/3)`，即每段恰好一个位置。
- 校验值：`g(x) = mix(x ^ 0xD6E8FEB86659FD93)`。
- `Add(x)`：三个位置各 `count += 1`、`keyXor ^= x`、`checkXor ^= g(x)`。
- `Remove(x)`：同上但 `count -= 1`（不检查存在性，纯属带符号累计）。
- `Subtract(a, b)`：新草图逐格 `count` 相减、两个异或分量再次异或；两侧 `m`
  不同则返回 `ErrSizeMismatch` 且不产生结果。两侧均按加锁快照读取，同一草图
  自己减自己恒为全零草图。

### 纯格子与剥离次序

`Decode(limit)` 在草图的私有副本上工作（不修改草图），重复执行：

1. 从下标 0 起顺序扫描，取第一个**纯格子**：`count` 为 `+1` 或 `-1`，且
   `g(keyXor) == checkXor`。计数为 `0`（即使异或分量非零）或校验不匹配都不纯。
2. `count == +1` 的键记入「仅 a 侧」，`count == -1` 的键记入「仅 b 侧」。
3. 在副本上撤销该键：`+1` 键执行 `Remove` 的效果；`-1` 键执行 `Add` 的效果，
   然后回到下标 0 重新扫描。
4. 没有纯格子时停止。只有所有格子的三个分量全部为 0 才算成功，返回两个
   升序键列表；否则报不可解码。

### 错误类别与优先级

| 场景 | 错误 |
| --- | --- |
| 构造时 `m` 不是 3 的正整数倍 | `ErrInvalidSize` |
| `Subtract` 两侧 `m` 不同 | `ErrSizeMismatch` |
| `Decode` 的 `limit < 0` | `ErrNegativeLimit` |
| 剥离后仍有非零格子 | `ErrUndecodable` |
| 即将记入第 `limit+1` 个键 | `ErrLimitExceeded` |

Decode 的判定顺序：先查 `limit < 0`；之后按剥离过程中先发生者报告——
纯格子的键若已在本次 Decode 记录过（重复键），立即按不可解码处理，且该判定
**先于**超出上限；超出上限先于最终的残留非零格子不可解码。被拒绝的操作不会
改变草图。

### 并发语义

`Add` / `Remove` / `Subtract` / `Decode` 均可用互斥锁并发调用，结果等价于某个
串行顺序：同一批增删以任意顺序得到逐格相同的草图；`Subtract` 分别对两侧取
快照，自减合法且为全零，不会死锁；未写入过的键先 Add 再 Remove 恢复原状；
`Decode` 返回的列表为独立切片，不与内部状态别名。

### 本地验证

```bash
# 全量测试（对拍用例会在日志中打印每组输入、输出与判定依据，并汇总失败次数）
go test -v ./ontology

# 竞态检测
go test -race ./...

# 仅看 m=300 的 2000 组随机对拍汇总（成功必须与朴素集合差一致）
go test -run TestRandomDifferential -v ./ontology | grep SUMMARY

go vet ./...
gofmt -l .
```

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
