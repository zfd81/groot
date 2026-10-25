// 朗读文本处理纯函数：Markdown 清洗与流式切句。不依赖 Vue / store。

const FENCE = '```'

// 把 Markdown 文本清洗成适合送给 TTS 的纯文本。
// 规则按顺序：围栏代码块（含未闭合尾部）→ 图片 → 链接保留文字 → 表格行 →
// 行首标题/引用/列表标记 → 行内代码去反引号 → 强调标记 → 连续空白压一个空格并 trim。
export function stripMarkdown(text: string): string {
  let s = text
  // 围栏代码块整段移除；未闭合的围栏从 ``` 起到末尾全部移除
  s = s.replace(/```[\s\S]*?```/g, '')
  s = s.replace(/```[\s\S]*$/, '')
  // 图片移除
  s = s.replace(/!\[[^\]]*\]\([^)]*\)/g, '')
  // 链接只保留文字；URL 内允许一层嵌套括号，如 https://x.org/Foo_(bar)
  s = s.replace(/\[([^\]]*)\]\((?:[^()]|\([^()]*\))*\)/g, '$1')
  // 表格行：以 | 开头的整行，以及 ---|--- 形式的分隔行
  s = s.replace(/^[ \t]*\|.*$/gm, '')
  s = s.replace(/^[ \t]*:?-{3,}:?(?:[ \t]*\|[ \t]*:?-{3,}:?)+[ \t]*$/gm, '')
  // 水平线 --- / *** / ___ 单独成行
  s = s.replace(/^[ \t]*(?:-{3,}|\*{3,}|_{3,})[ \t]*$/gm, '')
  // 行首标记：#、>、- * +、1.（可叠加，如 "> - item"）
  s = s.replace(/^[ \t]*(?:#{1,6}[ \t]+|>[ \t]?|[-*+][ \t]+|\d+\.[ \t]+)+/gm, '')
  // 行内代码去反引号保留内容
  s = s.replace(/`([^`]*)`/g, '$1')
  // 强调 / 删除线标记
  s = s.replace(/\*\*|__|~~/g, '')
  s = s.replace(/\*/g, '')
  // 下划线仅在词边界处视为强调标记，保留 tenant_id 这类标识符中的下划线。
  // 不用 lookbehind（Safari < 16.4 不支持）：匹配「可选词字符 + 下划线串 + 可选词字符」，
  // 两侧都是词字符时原样保留，否则只保留两侧的词字符。
  s = s.replace(/(\w?)_+(\w?)/g, (m, a: string, b: string) => (a && b ? m : a + b))
  // 连续空白压成一个空格
  return s.replace(/\s+/g, ' ').trim()
}

// 返回 s 中所有 ``` 围栏区间 [start, end)，end 为闭合标记之后的位置；
// 未闭合的围栏延伸到 s 末尾。
function fenceRanges(s: string): Array<[number, number]> {
  const ranges: Array<[number, number]> = []
  let pos = 0
  for (;;) {
    const open = s.indexOf(FENCE, pos)
    if (open < 0) break
    const close = s.indexOf(FENCE, open + FENCE.length)
    if (close < 0) {
      ranges.push([open, s.length])
      break
    }
    const end = close + FENCE.length
    ranges.push([open, end])
    pos = end
  }
  return ranges
}

// 从 text[from..] 起切出所有完整句子。
// 句末边界：。！？；!?; 与换行，以及后接空白或文本末尾的英文句点。
// 围栏代码块内部不算边界；未闭合的围栏及其后内容在 flush=false 时不切、不消费。
// 返回已 stripMarkdown、trim 且非空的句子，以及新的消费偏移 next。
// flush=true 时剩余全部文本作为最后一句，next === text.length。
export function extractSentences(
  text: string,
  from: number,
  flush: boolean,
): { sentences: string[]; next: number } {
  const sentences: string[] = []
  const push = (raw: string) => {
    const cleaned = stripMarkdown(raw)
    if (cleaned) sentences.push(cleaned)
  }

  let end = text.length
  const rest = text.slice(from)
  if (!flush) {
    const count = rest.split(FENCE).length - 1
    if (count % 2 === 1) end = from + rest.lastIndexOf(FENCE)
  }
  const slice = text.slice(from, end)
  const fences = fenceRanges(slice)
  const insideFence = (i: number) => fences.some(([a, b]) => i >= a && i < b)

  // 英文句点仅在后接空白时算边界；流式中文本末尾的 "." 可能是 3.14 的暂态，
  // 只有 flush 时才把末尾的 "." 也视为边界（此时尾段本就整体收入）。
  const re = flush ? /[。！？；!?;\n]|\.(?=\s|$)/g : /[。！？；!?;\n]|\.(?=\s)/g
  // 行首有序列表标记 "1." 的句点不是句末：从该行行首到句点之间只有空白和数字。
  const isListMarker = (dot: number) => {
    const lineStart = slice.lastIndexOf('\n', dot - 1) + 1
    return /^[ \t]*\d+$/.test(slice.slice(lineStart, dot))
  }
  let start = 0
  let m: RegExpExecArray | null
  while ((m = re.exec(slice)) !== null) {
    if (insideFence(m.index)) continue
    if (m[0] === '.' && isListMarker(m.index)) continue
    const boundary = m.index + m[0].length
    push(slice.slice(start, boundary))
    start = boundary
  }

  let next = from + start
  if (flush) {
    push(slice.slice(start))
    next = text.length
  }
  return { sentences, next }
}
