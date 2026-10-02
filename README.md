# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 链式 MAC 注记令牌（macaroon）服务

实现在 `ontology` 包（`ontology/macaroon.go`、`ontology/caveat.go`、
`ontology/errors.go`、`ontology/counters.go`）。

### 配置与构造

`New(Config{K, Mac, Cm, Lc, Rm})`，任一非法即整体拒绝：

- `K`：非空根密钥字节串。
- `Mac`：确定性可注入 MAC，`mac(key, data) []byte`，输出必须非空。
- `Cm`：注记个数上限，1–32。
- `Lc`：单条注记字节上限，1–256。
- `Rm`：撤销记录上限，1–10^6。

### 签名链

令牌 = `ID`（1–64 字节）、有序注记列表（每条非空且不超过 `Lc`，总数不超过
`Cm`）与 `Sig`（非空）。签名链：

```
s0 = mac(K, ID)
sj = mac(s(j-1), 第 j 条注记)        (j = 1..n)
Sig = sn
```

`sj` 称为第 j 层签名（j 从 0 起）。注记追加在末尾会改变后续所有签名，因此
追加次序不可交换。

### 操作

- `Mint(id, caveats, now)`：铸造；所有注记必须是已知前缀且合文法。
- `Attenuate(cfg, token, caveat)`：不依赖服务状态的纯函数，不校验旧令牌真伪，
  追加注记并令 `Sig' = mac(Sig, caveat)`；注记数达到 `Cm` 报超限。次序为先
  参数非法后超限。
- `Verify(token, request, now)`：按下面的固定次序只报第一个失败。
- `Revoke(token, k, now)`：撤销长度为 k（0–n）的前缀，记录 `(ID, k, sk)`。

### 注记文法与满足条件

冒号前为前缀；前缀不在四类中为**未知**，前缀已知但取值不合文法为**畸形**。

- `exp:N`：N 为 0–10^15 的无前导零十进制整数；当且仅当 `now < N` 满足
  （now 恰等于 N **不**满足）。
- `ops:a,b,c`：一个或多个逗号分隔项，项为非空、仅含小写字母/数字/连字符；
  请求 `Op` 等于某项即满足。
- `res:P`：P 以 `/` 开头；除恰为 `/` 外不以 `/` 结尾且不含 `//`。请求
  `Res` 等于 P 或以 `P + "/"` 开头才满足，故 `res:/a` 满足 `/a` 与 `/a/b`
  但不满足 `/ab`；`res:/` 对所有以 `/` 开头的路径满足。
- `amt:N`：N 为 0–10^12 的无前导零十进制整数；请求 `Amount <= N` 满足
  （恰等于上限满足）。

请求字段：`Op` 非空、`Res` 以 `/` 开头、`Amount` 在 0–10^12；`now` 在
0–10^15。

### 校验与撤销次序

`Verify` 只报第一个：参数非法 → 时钟回退 → 签名不符（重算 s0..sn 与 Sig
比较）→ 已撤销（对 j=0..n 依次查表 `(ID, j, sj)`，报命中的最小 j；较短前缀
优先于较长前缀）→ 注记不满足（按序号 1..n，第一条未知/畸形/不满足即报，
带序号与三类之一）。

`Revoke`：参数非法（含 k 越界）→ 时钟回退 → 签名不符（须先证明令牌真实）
→ 撤销记录超限 → 再生效。

### 前缀撤销的范围与保留期界

- 撤销长度 k 的前缀影响所有以该前缀开头的后代令牌（它们的 sk 相同），不
  影响祖先与兄弟分支。
- 记录 `(ID, k, sk)` 的保留期界 b = 前 k 条注记中**文法合法** `exp` 的最小
  N；没有合法 exp 则为无穷。只统计合法 exp，畸形 exp 被跳过。
- `b <= now` 时该撤销不入表，只返回已过期标记；幂等重复入表返回成功。
- 已到期（`b <= 校验时刻`）的记录在判定上视同不存在（于是 exp 已过的令牌
  报「注记不满足」而非「已撤销」），与其是否已被物理回收无关。

### 全局时钟与回收

所有带 `now` 的操作共用一个全局单调时钟；`now` 小于此前被接受操作见过的
最大 now 即时钟回退。每个**被接受**的 `Mint`、`Revoke`、`Verify`（Verify
通过参数与时钟两关即被接受，之后的签名/撤销/注记判定不改状态）都把时钟置
为 now 并物理回收全部 `b <= now` 的记录（按 b 的最小堆弹顶）。被拒绝的操作
既不推进时钟也不回收。

超限只在记录将被新入表（`b > now` 且表中无同一记录）且回收后的有效记录数
已等于 `Rm` 时报告；被拒绝时不回收、不推进时钟。

### 可区分拒绝与计数器

拒绝类别见 `ontology/errors.go`：参数非法、时钟回退、签名不符、已撤销
（带前缀长度）、注记不满足（带 1 起序号与未知/畸形/不满足三类）、超限。
非导出计数器可经 `Service.Snapshot()` 读取（MAC 调用数、撤销表查询数、回收
堆顶弹出数、有效记录数、时钟）：

- 通过参数与时钟两关的 `Mint`/`Verify`/`Revoke` 恰调用 MAC `n+1` 次；
  `Attenuate` 恰 1 次；参数非法或时钟回退为 0 次。
- Verify 签名相符时表查询不超过 n+1 次（命中即停，命中 j=0 时仅 1 次）；
  签名不符时为 0 次。
- 每次回收的弹出次数不超过本次回收记录数加一（最小堆逐顶弹出）。
- 所有操作在互斥锁下串行化，等价于某个串行顺序；相同序列重放结果完全确定。

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

# 随机对照（默认包含 2000 组）并打印第 0 组的输入/输出/判定依据日志
go test -run TestDifferentialRandom -v ./ontology
```

`TestDifferentialRandom` 用 2000 组随机铸造/追加/撤销/校验序列，把实现与
朴素模拟（`ontology/naive_test.go`：每次从零重算签名链、线性扫描撤销表）
逐步对照令牌字节、判定类别与定位、MAC 调用数、表查询数、堆弹出数、有效记录
数与时钟；`TestDeterministicReplay` 验证同序列重放结果完全一致；
`TestConcurrentSafety` 在 `-race` 下并发施压并校验堆不变量。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
