# 跨阈值报警流 NOTES

## 第三节推导（T=10, H=3, T-H=7）

| 步 | delta | 累计值 | 事件 | on 状态 |
|---|---|---|---|---|
| 1 | +10 | 10 | ON（10>=T，边沿） | ON |
| 2 | +2 | 12 | 无（已 ON，不重复） | ON |
| 3 | -5 | 7 | 无（7==T-H，未破下沿） | ON |
| 4 | +2 | 9 | 无（滞回带内抖动） | ON |
| 5 | -2 | 7 | 无 | ON |
| 6 | +4 | 11 | 无（已 ON） | ON |
| 7 | -7 | 4 | OFF（4<T-H） | OFF |
| 8 | +3 | 7 | 无（7<T，未再跨阈） | OFF |

正确事件序列：ON@1、OFF@7，共 2 条（ON 1 条）。

- (甲) 电平触发（不查当前 on）：value>=T 的步为 1(10)、2(12)、6(11)，会在第 2、6 步多发 ON；正确 ON 共 1 条，电平触发错成 3 条。
- (乙) ON 条件写成 `value > T`：第 1 步 value=10 不触发（10>10 为假），状态仍 OFF；首个 ON 推迟到第 2 步（value=12）。
- (丙) OFF 条件写成 `value < T`（无迟滞）：第 3 步 value=7<10 错发 OFF；第 6 步 value=11>=T 又错发一条 ON。错误序列 ON@1、OFF@3、ON@6、OFF@7 共 4 条，比正确序列多出 OFF@3 与 ON@6 两条。

## 不变量与保证位置 / 钉住测试

1. 与朴素参照一致：`alm.Store.Add` 每步调 `thr.Judge` 判定并顺序追加事件；测试 `TestAgainstNaive`。
2. 不重复不遗漏：`thr.Judge` 只在 `!oldOn&&v>=T` 或 `oldOn&&v<T-H` 时发事件；测试 `TestNoDupNoMiss`。
3. 去抖正确：`thr.Judge` 的 OFF 条件严格为 `v < T-H`（等于 T-H 不 OFF）；测试 `TestDebounce`。
4. 失败不留痕：`alm.Store.Add` 先校验（key/上限/溢出）再写 map 与事件；测试 `TestFailureNoTrace`。

另：`alm.Store.checked` 非导出计数器只记被更新 Key，`TestScaleFlat`（alm 包内白盒）钉住；并发一致性由 `TestConcurrentDistinctKeys` 钉住。
