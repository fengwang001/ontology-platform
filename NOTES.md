# 跨阈值报警流 NOTES

## 一、八步推导（T=10, H=3, T-H=7，起点 value=0, on=OFF）

| 步 | delta | Add 后 value | 事件 | 之后 on |
|---|---|---|---|---|
| 1 | +10 | 10 | ON（10>=T） | ON |
| 2 | +2 | 12 | 无（已 ON，12 不 <7） | ON |
| 3 | -5 | 7 | 无（7==T-H，不 <7） | ON |
| 4 | +2 | 9 | 无（9>=7） | ON |
| 5 | -2 | 7 | 无（7==T-H） | ON |
| 6 | +4 | 11 | 无（已 ON） | ON |
| 7 | -7 | 4 | OFF（4<7） | OFF |
| 8 | +3 | 7 | 无（7<10） | OFF |

正确事件序列：ON@1、OFF@7，共 2 条。

- (甲) 电平触发（每次 value>=T 就发 ON）：value>=T 出现在第 1(10)、2(12)、6(11) 步，
  会在第 2、6 步多发 ON；正确 ON 共 1 条，电平触发错成 3 条。
- (乙) ON 条件写成 value>T：第 1 步 value=10，10>10 不成立，不发 ON、状态仍 OFF；
  首个 ON 推迟到第 2 步（value=12>10）。
- (丙) OFF 条件写成 value<T（无迟滞）：第 3 步 value=7<10，错发 OFF@3；此后状态为 OFF，
  第 6 步 value=11>=10 又错发 ON@6；第 7 步 value=4<10 再发 OFF@7（与正确巧合）。
  错误序列 ON@1、OFF@3、ON@6、OFF@7 共 4 条，比正确序列多出 OFF@3 与 ON@6 两条。

## 二、四条不变量的保证位置与钉住测试

1. 与朴素参照一致：`alm.Store.Add` 每步调 `thr.Judge` 判定；`api.SelfCheck` 内置逐步参照重放比对；测试 `TestNaiveReference`。
2. 不重复不遗漏：`thr.Judge` 仅在 on 翻转时返回事件类型，否则 KindNone；测试 `TestNoDuplicateNoOmission`。
3. 去抖正确：`thr.Judge` 的 OFF 条件为 `newV < t-h`（等于 t-h 不 OFF）；测试 `TestDebounce`。
4. 失败不留痕：`alm.Store.Add` 先做全部校验（空 Key/溢出/超容量），通过后才改状态；测试 `TestRejectNoTrace`。
