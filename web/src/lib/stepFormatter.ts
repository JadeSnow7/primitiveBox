import type { GoalStep } from '@/types/goal'

const PRIMITIVE_LABELS: Record<string, string> = {
  'fs.read':    '读取文件',
  'fs.write':   '写入文件',
  'fs.list':    '列出目录',
  'fs.delete':  '删除文件',
  'shell.exec': '执行命令',
  'http.fetch': '请求网络',
  'http.post':  '发送请求',
}

const KEY_PARAMS: Record<string, string> = {
  'fs.read':    'path',
  'fs.write':   'path',
  'fs.list':    'path',
  'fs.delete':  'path',
  'shell.exec': 'command',
  'http.fetch': 'url',
  'http.post':  'url',
}

export function formatStepLabel(step: GoalStep): string {
  const verb = PRIMITIVE_LABELS[step.primitive] ?? step.primitive
  const keyParam = KEY_PARAMS[step.primitive]
  const paramValue = keyParam !== undefined ? step.input[keyParam] : undefined
  return paramValue !== undefined ? `${verb} ${String(paramValue)}` : verb
}
