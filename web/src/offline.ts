import { api, id } from './api'
import type { Book, Chapter, ChapterContent } from './types'

export const DB_NAME = 'reader_offline_v1'
export const DB_VERSION = 1

export interface CachedBookRecord extends Book {
  cachedAt?: number
}

export interface CachedChaptersRecord {
  bookId: string
  chapters: Chapter[]
  cachedAt?: number
}

export interface CachedChapterContent {
  key: string
  bookId: string
  chapterId: string
  title: string
  content: string
  cachedAt: number
}

export interface BatchProgress {
  current: number
  total: number
  chapterTitle: string
  percent: number
  status: 'caching' | 'completed' | 'cancelled' | 'error'
}

export interface BatchCacheOptions {
  bookId: string
  chapters: Chapter[]
  count?: number
  startIndex?: number
  onProgress?: (progress: BatchProgress) => void
  signal?: AbortSignal
}

export interface BatchCacheResult {
  cached: number
  total: number
  cancelled: boolean
}

let dbInstance: IDBDatabase | null = null

export function openOfflineDB(): Promise<IDBDatabase> {
  if (typeof indexedDB === 'undefined') {
    return Promise.reject(new Error('IndexedDB is not available in current environment'))
  }
  if (dbInstance) {
    return Promise.resolve(dbInstance)
  }
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains('books')) {
        db.createObjectStore('books', { keyPath: 'id' })
      }
      if (!db.objectStoreNames.contains('chapters')) {
        db.createObjectStore('chapters', { keyPath: 'bookId' })
      }
      if (!db.objectStoreNames.contains('chapter_contents')) {
        const store = db.createObjectStore('chapter_contents', { keyPath: 'key' })
        store.createIndex('bookId', 'bookId', { unique: false })
      }
    }
    request.onsuccess = () => {
      dbInstance = request.result
      dbInstance.onclose = () => { dbInstance = null }
      dbInstance.onversionchange = () => {
        if (dbInstance) {
          dbInstance.close()
          dbInstance = null
        }
      }
      resolve(dbInstance)
    }
    request.onerror = () => {
      reject(request.error)
    }
  })
}

export async function saveBook(book: Book): Promise<void> {
  const db = await openOfflineDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('books', 'readwrite')
    const store = tx.objectStore('books')
    const req = store.put({ ...book, cachedAt: Date.now() })
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
  })
}

export async function getBook(bookId: string): Promise<Book | null> {
  try {
    const db = await openOfflineDB()
    return new Promise((resolve, reject) => {
      const tx = db.transaction('books', 'readonly')
      const store = tx.objectStore('books')
      const req = store.get(bookId)
      req.onsuccess = () => resolve(req.result || null)
      req.onerror = () => reject(req.error)
    })
  } catch {
    return null
  }
}

export async function saveChapters(bookId: string, chapters: Chapter[]): Promise<void> {
  const db = await openOfflineDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('chapters', 'readwrite')
    const store = tx.objectStore('chapters')
    const req = store.put({ bookId, chapters, cachedAt: Date.now() })
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
  })
}

export async function getChapters(bookId: string): Promise<Chapter[] | null> {
  try {
    const db = await openOfflineDB()
    return new Promise((resolve, reject) => {
      const tx = db.transaction('chapters', 'readonly')
      const store = tx.objectStore('chapters')
      const req = store.get(bookId)
      req.onsuccess = () => resolve(req.result ? req.result.chapters : null)
      req.onerror = () => reject(req.error)
    })
  } catch {
    return null
  }
}

export async function saveChapterContent(
  bookId: string,
  chapterId: string,
  title: string,
  content: string
): Promise<void> {
  const db = await openOfflineDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction('chapter_contents', 'readwrite')
    const store = tx.objectStore('chapter_contents')
    const record: CachedChapterContent = {
      key: `${bookId}:${chapterId}`,
      bookId,
      chapterId,
      title,
      content,
      cachedAt: Date.now()
    }
    const req = store.put(record)
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
  })
}

export async function getChapterContent(
  bookId: string,
  chapterId: string
): Promise<CachedChapterContent | null> {
  try {
    const db = await openOfflineDB()
    return new Promise((resolve, reject) => {
      const tx = db.transaction('chapter_contents', 'readonly')
      const store = tx.objectStore('chapter_contents')
      const req = store.get(`${bookId}:${chapterId}`)
      req.onsuccess = () => resolve(req.result || null)
      req.onerror = () => reject(req.error)
    })
  } catch {
    return null
  }
}

export async function getCachedChapterIds(bookId: string): Promise<Set<string>> {
  try {
    const db = await openOfflineDB()
    return new Promise((resolve, reject) => {
      const tx = db.transaction('chapter_contents', 'readonly')
      const store = tx.objectStore('chapter_contents')
      const index = store.index('bookId')
      const req = index.getAll(IDBKeyRange.only(bookId))
      req.onsuccess = () => {
        const set = new Set<string>()
        if (Array.isArray(req.result)) {
          for (const item of req.result) {
            if (item && item.chapterId) {
              set.add(item.chapterId)
            }
          }
        }
        resolve(set)
      }
      req.onerror = () => reject(req.error)
    })
  } catch {
    return new Set<string>()
  }
}

export async function removeBookCache(bookId: string): Promise<void> {
  try {
    const db = await openOfflineDB()
    const tx = db.transaction(['books', 'chapters', 'chapter_contents'], 'readwrite')
    tx.objectStore('books').delete(bookId)
    tx.objectStore('chapters').delete(bookId)
    const contentStore = tx.objectStore('chapter_contents')
    const index = contentStore.index('bookId')
    const req = index.getAllKeys(IDBKeyRange.only(bookId))
    req.onsuccess = () => {
      if (Array.isArray(req.result)) {
        for (const key of req.result) {
          contentStore.delete(key)
        }
      }
    }
    await new Promise<void>((resolve, reject) => {
      tx.oncomplete = () => resolve()
      tx.onerror = () => reject(tx.error)
    })
  } catch {}
}

export async function batchCacheChapters({
  bookId,
  chapters,
  count,
  startIndex = 0,
  onProgress,
  signal
}: BatchCacheOptions): Promise<BatchCacheResult> {
  const start = Math.max(0, startIndex)
  let target = chapters.slice(start)
  if (count !== undefined && count > 0) {
    target = target.slice(0, count)
  }
  const total = target.length
  if (total === 0) {
    onProgress?.({
      current: 0,
      total: 0,
      chapterTitle: '',
      percent: 100,
      status: 'completed'
    })
    return { cached: 0, total: 0, cancelled: false }
  }

  const cachedSet = await getCachedChapterIds(bookId)
  let processed = 0
  let newlyCached = 0

  for (let i = 0; i < target.length; i++) {
    if (signal?.aborted) {
      onProgress?.({
        current: processed,
        total,
        chapterTitle: target[i]?.title || '',
        percent: Math.round((processed / total) * 100),
        status: 'cancelled'
      })
      return { cached: newlyCached, total, cancelled: true }
    }

    const ch = target[i]
    if (cachedSet.has(ch.id)) {
      processed++
      onProgress?.({
        current: processed,
        total,
        chapterTitle: ch.title,
        percent: Math.round((processed / total) * 100),
        status: processed === total ? 'completed' : 'caching'
      })
      continue
    }

    try {
      const res = await api<ChapterContent>(`/books/${id(bookId)}/chapters/${id(ch.id)}`)
      if (res && res.content !== undefined) {
        await saveChapterContent(bookId, ch.id, res.title || ch.title, res.content)
        cachedSet.add(ch.id)
        newlyCached++
      }
    } catch {
      if (signal?.aborted) {
        onProgress?.({
          current: processed,
          total,
          chapterTitle: ch.title,
          percent: Math.round((processed / total) * 100),
          status: 'cancelled'
        })
        return { cached: newlyCached, total, cancelled: true }
      }
    }

    processed++
    onProgress?.({
      current: processed,
      total,
      chapterTitle: ch.title,
      percent: Math.round((processed / total) * 100),
      status: processed === total ? 'completed' : 'caching'
    })
  }

  return { cached: newlyCached, total, cancelled: false }
}
