<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import {
  ArrowLeft,
  BookOpen,
  RefreshCw,
  Trash2,
  Search,
  ArrowRight,
  Download,
  X
} from 'lucide-vue-next'
import { api, id, message } from '../api'
import type { Book, Chapter } from '../types'
import {
  saveBook,
  getBook,
  saveChapters,
  getChapters,
  getCachedChapterIds,
  batchCacheChapters,
  removeBookCache
} from '../offline'
import BookCover from './BookCover.vue'

const props = defineProps<{ bookId: string }>()
const book = ref<Book | null>(null)
const chapters = ref<Chapter[]>([])
const loading = ref(true)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const query = ref('')
const group = ref('')
const isOfflineReady = ref(false)

const cachedChapterIds = ref<Set<string>>(new Set())
const cachedCount = computed(() => cachedChapterIds.value.size)
const cachingBusy = ref(false)
const cachingProgress = ref({
  current: 0,
  total: 0,
  chapterTitle: '',
  percent: 0
})
let abortController: AbortController | null = null

const path = `/books/${id(props.bookId)}`
const filtered = computed(() => chapters.value.filter(c => c.title.includes(query.value)))

async function refreshCachedStatus() {
  if (props.bookId) {
    cachedChapterIds.value = await getCachedChapterIds(props.bookId)
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [b, c] = await Promise.all([
      api<Book>(path),
      api<Chapter[]>(`${path}/chapters`)
    ])
    book.value = b
    chapters.value = c || []
    group.value = b.group || ''
    isOfflineReady.value = false
    // Persist to offline cache
    saveBook(b).catch(() => {})
    saveChapters(b.id, chapters.value).catch(() => {})
  } catch (e) {
    // Fallback to offline cache
    const cachedBook = await getBook(props.bookId)
    const cachedChapters = await getChapters(props.bookId)
    if (cachedBook) {
      book.value = cachedBook
      chapters.value = cachedChapters || []
      group.value = cachedBook.group || ''
      isOfflineReady.value = true
      notice.value = '离线缓存就绪'
      error.value = ''
    } else {
      error.value = message(e)
    }
  } finally {
    loading.value = false
    await refreshCachedStatus()
  }
}

async function saveGroup() {
  busy.value = true
  error.value = ''
  try {
    await api(path, 'PATCH', { group: group.value.trim() })
    if (book.value) book.value.group = group.value.trim()
    notice.value = '分组已保存。'
  } catch (e) {
    error.value = message(e)
  } finally {
    busy.value = false
  }
}

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    await api(`${path}/refresh`, 'POST')
    await load()
    notice.value = '目录已更新。'
  } catch (e) {
    error.value = message(e)
  } finally {
    busy.value = false
  }
}

async function remove() {
  if (!confirm('确定从书架移除此书吗？阅读进度、书签和本地文件也会被删除，此操作无法撤销。')) return
  busy.value = true
  try {
    await api(path, 'DELETE')
    await removeBookCache(props.bookId)
    location.hash = '/library'
  } catch (e) {
    error.value = message(e)
  } finally {
    busy.value = false
  }
}

async function startBatchCaching(startIndex: number) {
  if (cachingBusy.value || !book.value) return
  cachingBusy.value = true
  notice.value = ''
  abortController = new AbortController()
  const totalToCache = Math.max(0, chapters.value.length - startIndex)
  cachingProgress.value = {
    current: 0,
    total: totalToCache,
    chapterTitle: '',
    percent: 0
  }
  try {
    const res = await batchCacheChapters({
      bookId: book.value.id,
      chapters: chapters.value,
      startIndex,
      signal: abortController.signal,
      onProgress: (p) => {
        cachingProgress.value = {
          current: p.current,
          total: p.total,
          chapterTitle: p.chapterTitle,
          percent: p.percent
        }
      }
    })
    if (res.cancelled) {
      notice.value = '离线缓存已停止。'
    } else {
      notice.value = `离线缓存已完成（已缓存 ${res.cached} 章新内容）。`
    }
  } catch {
    notice.value = '离线缓存过程中出现异常，部分章节已保存。'
  } finally {
    cachingBusy.value = false
    abortController = null
    await refreshCachedStatus()
  }
}

function cacheSubsequent() {
  const currentChapterId = book.value?.progress?.chapterId
  let startIndex = 0
  if (currentChapterId) {
    const idx = chapters.value.findIndex(c => c.id === currentChapterId)
    if (idx >= 0) startIndex = idx
  }
  startBatchCaching(startIndex)
}

function cacheAll() {
  startBatchCaching(0)
}

function cancelCaching() {
  if (abortController) {
    abortController.abort()
    abortController = null
  }
}

onMounted(load)
</script>

<template>
  <a href="#/library" class="back-link"><ArrowLeft :size="17" />返回我的书架</a>
  <div v-if="error" class="error" role="alert">
    {{ error }} <button class="text-button" @click="load">重新加载</button>
  </div>
  <div v-if="notice" class="success" role="status">{{ notice }}</div>
  <div v-if="loading" class="empty" role="status">正在翻开书籍信息…</div>
  <template v-else-if="book">
    <section class="book-detail">
      <BookCover :title="book.title" :kind="book.kind" />
      <div class="book-detail-info">
        <span class="eyebrow">
          {{ book.kind === 'online' ? 'ONLINE COLLECTION' : 'PERSONAL COLLECTION' }}
          <span v-if="isOfflineReady" class="offline-badge">离线缓存就绪</span>
        </span>
        <h1>{{ book.title }}</h1>
        <p class="muted">
          {{ book.author || '佚名' }}<span class="dot">·</span>{{ book.chapterCount }} 章<span class="dot">·</span>{{ book.kind.toUpperCase() }}
        </p>
        <p class="book-intro">{{ book.intro || '暂无简介。故事的精彩，等你亲自翻开。' }}</p>
        <div class="actions">
          <a v-if="chapters.length" class="button primary" :href="`#/read/${id(book.id)}`">
            <BookOpen :size="18" />{{ book.progress ? '继续阅读' : '开始阅读' }}<ArrowRight :size="17" />
          </a>
          <button v-else disabled>暂无可读章节</button>
          <button v-if="book.kind === 'online'" :disabled="busy || cachingBusy" @click="refresh">
            <RefreshCw :size="17" />更新目录
          </button>
          <button v-if="chapters.length && !cachingBusy" :disabled="busy" @click="cacheSubsequent" title="从当前阅读进度向后缓存章节">
            <Download :size="17" />离线缓存后续章节
          </button>
          <button v-if="chapters.length && !cachingBusy" :disabled="busy" @click="cacheAll" title="离线下载全书所有章节">
            <Download :size="17" />离线全书
          </button>
          <button v-if="cachingBusy" class="danger" @click="cancelCaching">
            <X :size="17" />取消缓存
          </button>
        </div>
        <div v-if="chapters.length" class="offline-cache-panel">
          <div class="offline-cache-status">
            <span class="offline-cached-count">已缓存 {{ cachedCount }} / {{ chapters.length }} 章</span>
            <span v-if="cachingBusy" class="offline-progress-text">
              正在缓存：{{ cachingProgress.chapterTitle || '正在拉取…' }} ({{ cachingProgress.current }}/{{ cachingProgress.total }} · {{ cachingProgress.percent }}%)
            </span>
          </div>
          <div v-if="cachingBusy" class="offline-progress-track">
            <div class="offline-progress-bar" :style="{ width: `${cachingProgress.percent}%` }"></div>
          </div>
        </div>
        <p v-if="book.progress" class="fine-print">
          上次读到：{{ chapters.find(c => c.id === book?.progress?.chapterId)?.title || '已保存章节' }} · 章内 {{ Math.round(book.progress.position * 100) }}%
        </p>
        <form class="inline-form" @submit.prevent="saveGroup">
          <label>书架分组<input v-model="group" maxlength="80" placeholder="如：文学、正在读" /></label>
          <button :disabled="busy">保存分组</button>
        </form>
        <button class="text-button danger" :disabled="busy" @click="remove">
          <Trash2 :size="15" />从书架移除
        </button>
      </div>
    </section>
    <section class="toc-section">
      <div class="section-heading">
        <h2>目录 <span class="count">{{ chapters.length }}</span></h2>
        <label class="search-input">
          <Search :size="16" />
          <input v-model="query" aria-label="搜索章节" placeholder="搜索章节" />
        </label>
      </div>
      <div class="chapter-grid">
        <a
          v-for="chapter in filtered"
          :key="chapter.id"
          :href="`#/read/${id(book.id)}/${id(chapter.id)}`"
          :class="{ selected: book.progress?.chapterId === chapter.id }"
        >
          <span>{{ String(chapter.index + 1).padStart(2, '0') }}</span>
          {{ chapter.title }}
          <BookOpen v-if="book.progress?.chapterId === chapter.id" :size="15" />
        </a>
      </div>
      <p v-if="!filtered.length" class="empty">
        {{ chapters.length ? '没有匹配的章节。' : '暂无章节，在线书籍可尝试更新目录。' }}
      </p>
    </section>
  </template>
</template>
