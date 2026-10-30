export class ApiError extends Error { constructor(public status:number, public code:string, message:string) { super(message) } }
export function csrfToken() { const entry = document.cookie.split(';').map(x=>x.trim()).find(x=>x.startsWith('reader_csrf=')); if (!entry) return ''; try { return decodeURIComponent(entry.slice('reader_csrf='.length)) } catch { return entry.slice('reader_csrf='.length) } }
export interface ApiMeta { warnings?: Array<{sourceId?:string; message:string}> }
export async function api<T>(path:string, method='GET', body?:unknown, onMeta?:(meta:ApiMeta)=>void):Promise<T> {
  const headers:Record<string,string> = { Accept:'application/json' }
  if (method !== 'GET') headers['X-CSRF-Token'] = csrfToken()
  const multipart = body instanceof FormData
  if (body !== undefined && !multipart) headers['Content-Type']='application/json'
  let response:Response
  try { response = await fetch(`/api/v1${path}`, { method, headers, credentials:'same-origin', body:body === undefined ? undefined : multipart ? body : JSON.stringify(body) }) }
  catch { throw new ApiError(0,'NETWORK','无法连接服务，请检查网络后重试。') }
  const payload = await response.json().catch(()=>null)
  if (!response.ok) {
    if (response.status === 401 && path !== '/auth/login' && path !== '/auth/me') window.dispatchEvent(new Event('session-expired'))
    throw new ApiError(response.status, payload?.error?.code || 'HTTP_ERROR', payload?.error?.message || `服务暂时不可用（${response.status}）`)
  }
  onMeta?.(payload?.meta || {})
  return payload?.data as T
}
export const id = (value:string)=>encodeURIComponent(value)
export const message = (error:unknown)=>error instanceof Error ? error.message : '操作失败，请重试。'
export const date = (value:string)=>value ? new Date(value).toLocaleString('zh-CN') : '—'
export interface StreamSearchProgress {
  sourceId: string
  sourceName: string
  status: 'searching' | 'success' | 'error' | 'timeout'
  timestamp?: number
  count?: number
  results?: any[]
  error?: string
  completed: number
  total: number
}
export interface StreamSearchStart { total: number; sources?: string[] }
export interface StreamSearchDone { total: number; completed: number; totalFound: number }
export interface StreamSearchHandlers {
  onStart?: (data: StreamSearchStart) => void
  onProgress?: (progress: StreamSearchProgress) => void
  onDone?: (data: StreamSearchDone) => void
}
export async function streamSearch(
  params: { keyword: string; sourceIds?: string[]; timeoutSeconds?: number },
  handlers: StreamSearchHandlers,
  signal?: AbortSignal
): Promise<void> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'Accept': 'text/event-stream',
    'X-CSRF-Token': csrfToken()
  }
  let response: Response
  try {
    response = await fetch('/api/v1/search/stream', {
      method: 'POST',
      headers,
      credentials: 'same-origin',
      body: JSON.stringify(params),
      signal
    })
  } catch (err: any) {
    if (signal?.aborted) return
    throw new ApiError(0, 'NETWORK', '无法连接服务，请检查网络后重试。')
  }
  if (!response.ok) {
    const payload = await response.json().catch(() => null)
    if (response.status === 401) window.dispatchEvent(new Event('session-expired'))
    throw new ApiError(response.status, payload?.error?.code || 'HTTP_ERROR', payload?.error?.message || `搜索失败（${response.status}）`)
  }
  const reader = response.body?.getReader()
  if (!reader) throw new Error('流式传输不受当前环境支持')
  const decoder = new TextDecoder()
  let buffer = ''
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n\n')
    buffer = lines.pop() || ''
    for (const chunk of lines) {
      const parts = chunk.split('\n')
      let event = 'message'
      let data = ''
      for (const line of parts) {
        if (line.startsWith('event: ')) event = line.slice(7).trim()
        else if (line.startsWith('data: ')) data = line.slice(6).trim()
      }
      if (!data) continue
      try {
        const parsed = JSON.parse(data)
        if (event === 'start') handlers.onStart?.(parsed)
        else if (event === 'progress') handlers.onProgress?.(parsed)
        else if (event === 'done') handlers.onDone?.(parsed)
      } catch {}
    }
  }
}
