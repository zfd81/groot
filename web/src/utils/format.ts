// 通用展示格式化工具。

// token 数量缩写：千位以上用 K、百万位以上用 M，保留一位小数（如 39.5K）。
export function fmtTok(n: number): string {
  if (n < 1000) return String(n)
  if (n < 1_000_000) return `${(n / 1000).toFixed(1)}K`
  return `${(n / 1_000_000).toFixed(1)}M`
}
