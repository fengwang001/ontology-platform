# AUDIT — 语义逐条核对

| # | 语义 | 代码保证位置 | 钉住它的测试 |
|---|------|--------------|--------------|
| 1 | `\r\n`/`\r`→`\n`，`\r\r\n`→`\n\n` | `eol/eol.go` Feed 状态机（待定 `\r`） | `TestTable`（norm） |
| 2 | 行尾空白删除、行中保留、纯空白行变空行 | `norm/norm.go` step：`r.End` 时 `resolveWS(false)`，普通字节前 `resolveWS(true)` | `TestTable`（norm） |
| 3 | 三种末尾换行策略 | `norm/api.go` ApplyPolicy（推导见 DESIGN.md 推导 1） | `TestTable`（norm） |
| 4 | 任意切分逐字节一致（含映射） | 状态机只依赖字节流不依赖 Write 边界：`eol.Scanner`+`ws.Buf` | `TestSplitsAndTruncation`（norm，所有切点+逐字节） |
| 5 | 幂等、输出无 `\r`、无行尾空白 | 输出只经 `emit('\n')` 与 1:1 字节；行尾空白必在 `r.End` 处删除 | `TestIdempotent`（norm，三策略） |
| 6 | 双向映射、互逆、单调、被删字节折叠 | `span/span.go` ToOrig/ToOut 二分；删除区间 `ULen=0` 折叠到下一输出位 | `TestMapping`（norm） |
| 7 | NUL/非法 UTF-8 原样通过；严格模式报错进终态 | 字节不校验直接透传；`step` 中 Strict 检查返回 `ErrAt{ErrNUL}` | `TestTable`、`TestErrors`（norm） |
| 8 | 待定空白缓冲上限（拒绝）、输出上限 | `ws/ws.go` Add→`ErrOverflow`；`norm` emit/resolveWS 检查 `MaxOutput` | `TestErrors`（norm） |
| 9 | par 任意 K/切点与单线程一致 | `par/par.go`：段末待定 CR/WS 修正 + 坐标平移（DESIGN.md 推导 4） | `TestParAllCuts`（par，K=1..8 全切点） |

## 故障注入与并发

- 截断遍历：`TestSplitsAndTruncation` 对每个前缀比较「逐字节喂入+Close」与整体规范化。
- 错误可区分且带原文偏移：`TestErrors`（`ErrNUL`/`ws.ErrOverflow`/`ErrOutput`/`ErrClosed`，
  均 `errors.Is/As` 判定并断言偏移）；par 侧偏移平移见 `TestParError`。
- 并发：`TestParRepeat` 同一输入 50 次并行逐位一致 + 多 norm 实例并发；`go test -race` 干净。

## 复杂度实测（TestProbe，par 包）

- 10 万行混杂（700000 字节）：区间数 300000，查询上限 2·log2+4 = 40，实测单次 <= 19。
- 1000 万字节混杂：区间数 4285713，上限 48，实测单次 <= 23。
- 纯 `\n` 10 MB：区间数 = 1（常数，不随输出字节增长）。
- 查询为二分（`span` 内非导出计数器 `probe`，`LastProbe` 暴露给测试）。
