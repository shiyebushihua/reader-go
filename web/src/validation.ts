export const usernamePattern = '[A-Za-z0-9_\\-]{3,32}'
export function usernameError(value: string): string {
  return /^[A-Za-z0-9_\-]{3,32}$/.test(value) ? '' : '用户名须为 3–32 位 ASCII 字母、数字、下划线或连字符。'
}
export function passwordError(value: string): string {
  const bytes = new TextEncoder().encode(value).length
  return bytes >= 10 && bytes <= 72 ? '' : '密码须为 10–72 字节（中文和其他非 ASCII 字符可能占多个字节）。'
}
export const settingLimits = { fontSize: { min:12, max:40 }, lineHeight: { min:1.2, max:3 }, width: { min:320, max:1200 } }
export function settingsError(value: {theme:string;fontSize:number;lineHeight:number;width:number}): string {
  if (!['light','dark','sepia'].includes(value.theme)) return '请选择有效的阅读主题。'
  for (const key of ['fontSize','lineHeight','width'] as const) {
    if (!Number.isFinite(value[key]) || value[key] < settingLimits[key].min || value[key] > settingLimits[key].max) return '阅读设置超出允许范围，请重新调整。'
  }
  return ''
}
