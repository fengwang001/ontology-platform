// Package league implements a thread-safe football league table with
// recursive head-to-head tie breaking.
//
// Teams are numbered 1 through n, where 2 <= n <= 32. Add registers one
// ordered home/away pair; Remove deletes it. Invalid operations return their
// sentinel error without mutating registered matches.
//
// Teams are first grouped by total points. Every tied group of at least two
// teams is ranked recursively:
//
//   - Step A uses only matches whose two teams are both in the current group.
//     Teams are split by mutual points, mutual goal difference, and mutual
//     goals for in descending lexicographic order.
//   - If step A produces one class, step B splits using total goal difference
//     and total goals for from all registered matches.
//   - If step B also produces one class, every team in the current group
//     shares one rank.
//
// When either step creates multiple classes, classes are placed in descending
// order and each class of at least two teams restarts from step A using only
// matches internal to that class. Shared ranks count only teams in strictly
// earlier rank blocks, so tied teams can produce sequences such as 1, 2, 2, 4.
package league
