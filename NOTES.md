# 事件时间单调化输出 — 推导与不变量

## 一、七行分步表（delay=3，wm = maxSeen − 3，可发射判据 TS <= wm）

| 步 | 到达 | 本步后 wm | 本步发射（ID: 原TS → outTS） | 本步后缓冲区 |
|---|---|---|---|---|
| 1 | a(10) | 7 | 无 | a(10) |
| 2 | b(7) | 7 | b: 7 → 7 | a(10) |
| 3 | c(12) | 9 | 无 | a(10) c(12) |
| 4 | d(9) | 9 | d: 9 → 9 | a(10) c(12) |
| 5 | e(5) | 9 | e: 5 → 9（上钳） | a(10) c(12) |
| 6 | f(11) | 9 | 无 | a(10) c(12) f(11) |
| 7 | g(8) | 9 | g: 8 → 9（上钳） | a(10) c(12) f(11) |

Flush（wm=+∞）：a: 10→10，f: 11→11，c: 12→12。全序列：b7 d9 e9 g9 a10 f11 c12，单调不减。

## 二、三问

- **(甲)** e(5) 被上钳为 **9**（=上一条 d 的 outTS）。若不上钳直接输出 5，则第 3 条输出 5 < 第 2 条的 9，单调不减在第 3 条处被破坏。
- **(乙)** 是。TS=7 == wm=7 满足 `TS <= wm`，本步发射 b: 7→7。若错写成 `TS < wm`，第 2 步输出错成「无」，b 延后到第 3 步（c 到达使 wm 升为 9）才以 7→7 发射。
- **(丙)** 第 5 步 wm 应为 maxSeen−delay = 12−3 = **9**。若错写成「当前事件 TS − delay」= 5−3 = **2**，wm 从 9 回退到 2，违背不变量 3（水位线单调）。

## 三、四条不变量的保证位置与钉住测试

注：`Feed` 一批事件时，批内先统一推进水位线、全部入缓冲，再做一次发射阶段（单事件批即逐步语义）；故整批喂入时 Flush 后输出 == 朴素重算。

1. **与朴素重算一致**：`buf.Buffer.Feed/Emit/Flush`（pending 按 (TS,到达序) 有序取最小 + `tm.Clamp` 上钳）；测试 `TestNaiveConsistency`（整批随机序列比对 api 内 naive）、`TestSevenEventSequence`。
2. **输出单调**：`buf.Buffer.Emit` 中 `outTS = tm.Clamp(TS, last)`；测试 `TestMonotonicAndWatermark`。
3. **水位线单调**：`tm.Watermark.Observe` 只对 maxSeen 取 max，wm 只进不退；测试 `TestMonotonicAndWatermark`。
4. **失败不留痕**：`api.Engine.Feed` 先整批校验（ID 非空、容量）再落地，`New` 拒绝负 delay；测试 `TestFailuresLeaveNoTrace`。
