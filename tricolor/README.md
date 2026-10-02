# tricolor — RFC 4115 风格双速率三色标记器

`tricolor` 实现带溢出耦合承诺桶/超额桶、红色惩罚期与在线改配置的
双速率颜色感知三色标记器。相同操作序列重放得到完全相同的输出与桶
状态；所有方法可并发调用，结果等价于某个串行顺序。

## 构造参数与内部状态

| 参数 | 含义 | 范围 |
| --- | --- | --- |
| `CIR` | 承诺速率（字节/毫秒） | [1, 1e6] |
| `CBS` | 承诺桶深（字节） | [1, 1e9] |
| `EBS` | 超额桶深（字节） | [0, 1e9] |
| `W` | 红色窗口（毫秒） | [1, 1e6] |
| `K` | 红色阈值 | [1, 1000] |
| `Pn` | 基础惩罚时长（毫秒） | [1, 1e6] |

内部状态：`Tc`（初值 CBS）、`Te`（初值 EBS）、`last`（初 0）、
`until`（初 0，仅当 `now<until` 处于惩罚期）、`lastUntil`（初 0，
表示无）、连击数 `s`（初 0）、红记录队列（初空）。时刻 `now` 取值
[0, 1e12] 毫秒；`(now-last)*CIR ≤ 1e18`，无 int64 溢出。

## 补充与溢出规则（Refill）

每次 `Mark`/`Reconfigure` 先结算：`add=(now-last)*CIR`，`Tc+=add`；
若 `Tc>CBS`，溢出 `o=Tc-CBS`，令 `Tc=CBS`、`Te=min(EBS, Te+o)`
（超出 EBS 的部分丢弃）；`last=now`。超额桶只接收承诺桶溢出，不直接
补充。惩罚期内同样结算。批量补充与逐毫秒各补 CIR、逐毫秒溢出的朴素
模拟逐步一致（见 `TestNaiveSimulationComparison`）。任意时刻恒有
`0≤Tc≤CBS`、`0≤Te≤EBS`。

## 三种输入色的判定与降级

`Mark(now, color, b)`（`b`∈[1, 1e6]）步骤固定：先 Refill；若
`now<until`，输出 Red（惩罚判红标志为真），不扣令牌、不记红。否则：

- **Green / Blind**（色盲，按 Green 处理）：`Tc≥b` 则 `Tc-=b` 输出
  Green；否则 `Te≥b` 则 `Te-=b` 输出 Yellow（降级，扣超额桶）；否则
  Red。
- **Yellow**：`Te≥b` 则 `Te-=b` 输出 Yellow，否则 Red；绝不触碰
  `Tc`。
- **Red**：直接输出 Red，不扣令牌、不记红。

每个报文的输入字节恰计入其输出色的报文数与字节数（`Stats()`）。

## 红记录与惩罚期边界

仅当输入色非 Red、本次输出为 Red 且不在惩罚期时记红：剔除队列中
`t+W≤now` 的旧记录（恰等即剔除，摊还 O(1)），`now` 入队；若队列长度
（含本条）`≥K`，触发惩罚并清空队列，本次输出仍为 Red。

惩罚时长递增：触发时若 `lastUntil>0` 且 `now-lastUntil<W`（恰等不
算），`s=min(s+1,3)`，否则 `s=0`；`dur=Pn*2^s`（最长 8×Pn），
`until=now+dur`、`lastUntil=until`。`now==until` 时已不在惩罚期。

## 改配置语义（Reconfigure）

`Reconfigure(now, CIR', CBS', EBS')`：先按**旧**参数 Refill(now)，再
`Tc=min(Tc,CBS')`、`Te=min(Te,EBS')`——被截去的令牌直接丢弃，**不**
转入超额桶；随后改用新参数。惩罚状态（`until/lastUntil/s`）与红队列
保持不变。参数范围同构造。

## 错误与拒绝语义

- `ErrInvalidParam`：构造/Reconfigure 参数越界、color 非法、`b` 或
  `now` 越界。
- `ErrClockBackward`：`now < last`。
- 参数非法优先于时钟回退；被拒绝的调用不 Refill、不改变 `last` 与
  任何其他状态。用 `errors.Is` 区分两类原因。

## 守恒式

在未调用 Reconfigure 的序列上（`last` 为最近结算时刻）：

- Green+Yellow 输出字节总数 ≤ `CBS+EBS+CIR*last`；
- Yellow 输出字节总数 ≤ `EBS` + 溢出入 Te 的令牌总数。

## 本地验证

```bash
go test ./tricolor/                 # 全部单测（含规格示例与边界）
go test -race ./tricolor/           # 竞态检测
go test -v ./tricolor/ -run TestNaiveSimulationComparison
                                    # 2000 组随机序列 vs 逐毫秒朴素模拟，
                                    # 日志打印每步输入、输出与判定依据
go test ./tricolor/ -run TestSpecBasicExample -v   # 规格 worked example
```
