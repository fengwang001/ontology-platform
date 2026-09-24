# 双时钟水位 NOTES

## 第三节推导（L=5，-∞ 表示负无穷）

| 步 | 操作 | maxEt | ETW | PTW | 判定 | LateCount |
|---|---|---|---|---|---|---|
| 1 | Ingest(10,"a") | 10 | 5 | -∞ | 按时（首个事件） | 0 |
| 2 | Heartbeat(100) | 10 | 5 | 100 | —（只推进 PTW） | 0 |
| 3 | Ingest(8,"b") | 10 | 5 | 100 | 按时（8 > 5） | 0 |
| 4 | Ingest(20,"c") | 20 | 15 | 100 | 按时（20 > 5） | 0 |
| 5 | Ingest(14,"d") | 20 | 15 | 100 | 迟到（14 <= 15） | 1 |
| 6 | Heartbeat(200) | 20 | 15 | 200 | —（只推进 PTW） | 1 |
| 7 | Ingest(16,"e") | 20 | 15 | 200 | 按时（16 > 15，PTW=200 不参与） | 1 |
| 8 | Ingest(15,"f") | 20 | 15 | 200 | 迟到（15 <= 15，等号算迟到） | 2 |

最终 View（et 升序、同 et 按 id）：(8,b) (10,a) (16,e) (20,c)。

- **(甲)** 若判定误写成 `et < ETW`：第 8 步 `15 < 15` 为假 → f 被误判**按时**并接受；`LateCount` 错成 **1**（应为 2），`View` 多出一条 **(15,f)**。
- **(乙)** 若误用 PTW 判定（`et <= PTW`）：第 7 步 `16 <= 200` → e 被误判**迟到**并丢弃；`LateCount` 在第 7 步错成 2，`View` 少了本应按时接受的 **(16,e)**。
- **(丙)** 若心跳误抬 ETW 为 `PTW − L`：第 2 步后 ETW 错成 **95**（正确值 5）；第 3 步 `Ingest(8,"b")` 因 `8 <= 95` 被误判**迟到**并丢弃（正确应按时）。违反不变量 **2**（迟到判定只看事件时间水位）与不变量 **3**（Heartbeat 绝不改变 ETW/已接受事件）。

## 四条不变量的落点

1. **与朴素重放一致**：`dual.Engine.Ingest` 先判后收、判定与状态推进在同一临界区；钉于 `TestNaiveReplayConsistency`。
2. **迟到只看 ETW**：判定唯一入口 `etime.Clock.Ingest` 的 `et <= maxEt-L`，PTW 不传入；钉于 `TestLateOnlyByETW`。
3. **两水位单调独立**：`etime` 的 maxEt 只取 max、`dual.Heartbeat` 只升且不碰事件侧状态；钉于 `TestMonotonicIndependent`。
4. **失败不留痕**：所有参数/回退校验先于任何状态修改（`api.New`、`dual.Ingest`、`dual.Heartbeat` 入口）；钉于 `TestRejectedOpsLeaveNoTrace`。
