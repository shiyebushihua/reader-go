<script setup lang="ts">
import { ref, onMounted, computed, nextTick } from 'vue'
import {
  ArrowLeft,
  BookOpen,
  RefreshCw,
  Trash2,
  Search,
  ArrowRight,
  Download,
  X,
  Shuffle,
  ArrowUpDown,
  LocateFixed,
  LoaderCircle
} from 'lucide-vue-next'
import { api, id, message } from '../api'
import type { Book, Chapter, Source, SearchResult } from '../types'
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
const tocReversed = ref(false)
const filtered = computed(() => {
  const list = chapters.value.filter(c => c.title.includes(query.value))
  return tocReversed.value ? [...list].reverse() : list
})

// 换源中心状态
const sourcesMap = ref<Record<string, string>>({})
const changeSourceOpen = ref(false)
const changeSourceLoading = ref(false)
const changeSourceResults = ref<SearchResult[]>([])
const changeSourceError = ref('')
const changeSourceSubmitting = ref(false)
const changeSourceQuery = ref('')

const currentSourceName = computed(() => {
  if (!book.value?.sourceId) return ''
  return sourcesMap.value[book.value.sourceId] || ''
})

async function loadSourcesMap() {
  try {
    const list = await api<Source[]>('/sources')
    const map: Record<string, string> = {}
    for (const s of list || []) {
      map[s.id] = s.name
    }
    sourcesMap.value = map
  } catch {
    // ignore
  }
}

async function openChangeSourceModal() {
  if (!book.value || book.value.kind !== 'online') return
  changeSourceOpen.value = true
  changeSourceQuery.value = book.value.title
  await executeSearchSources()
}

async function executeSearchSources() {
  const kw = changeSourceQuery.value.trim()
  if (!kw) return
  changeSourceLoading.value = true
  changeSourceError.value = ''
  changeSourceResults.value = []
  try {
    const sources = await api<Source[]>('/sources')
    const map: Record<string, string> = {}
    const enabledIds: string[] = []
    for (const s of sources || []) {
      map[s.id] = s.name
      if (s.enabled) enabledIds.push(s.id)
    }
    sourcesMap.value = map
    if (!enabledIds.length) {
      changeSourceError.value = '当前暂无已启用的个人书源，请先前往「书源管理」导入并启用书源。'
      return
    }
    const results = await api<SearchResult[]>('/search', 'POST', {
      keyword: kw,
      sourceIds: enabledIds.slice(0, 50),
      timeoutSeconds: 15
    })
    changeSourceResults.value = (results || []).sort((a, b) => {
      const aExact = a.title === book.value?.title ? 1 : 0
      const bExact = b.title === book.value?.title ? 1 : 0
      return bExact - aExact
    })
  } catch (e) {
    changeSourceError.value = message(e)
  } finally {
    changeSourceLoading.value = false
  }
}

async function applyChangeSource(item: SearchResult) {
  if (!book.value || changeSourceSubmitting.value) return
  changeSourceSubmitting.value = true
  changeSourceError.value = ''
  try {
    await api(`${path}/source`, 'PUT', {
      sourceId: item.sourceId,
      url: item.url
    })
    changeSourceOpen.value = false
    await load()
    notice.value = `已切换至书源「${sourcesMap.value[item.sourceId] || '新书源'}」，共 ${chapters.value.length} 章。`
  } catch (e) {
    changeSourceError.value = message(e)
  } finally {
    changeSourceSubmitting.value = false
  }
}

async function scrollToCurrentChapter() {
  if (!book.value?.progress?.chapterId) return
  query.value = ''
  await nextTick()
  const el = document.querySelector('.chapter-grid > a.selected') as HTMLElement | null
  el?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

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

onMounted(() => {
  load()
  loadSourcesMap()
})
</script>

<template>
  <a href="#/library" class="back-link"><ArrowLeft :size="17" />返回我的书架</a>
  <div v-if="error" class="error" role="alert">
    <span>{{ error }}</span>
    <div class="actions">
      <button v-if="book?.kind === 'online'" class="text-button" @click="openChangeSourceModal">
        <Shuffle :size="15" /> 一键换源
      </button>
      <button class="text-button" @click="load">重新加载</button>
    </div>
  </div>
  <div v-if="notice" class="success" role="status">{{ notice }}</div>
  <div v-if="loading" class="empty" role="status">正在翻开书籍信息…</div>
  <template v-else-if="book">
    <section class="book-detail">
      <BookCover :title="book.title" :kind="book.kind" />
      <div class="book-detail-info">
        <span class="eyebrow">
          {{ book.kind === 'online' ? 'ONLINE COLLECTION' : 'PERSONAL COLLECTION' }}
          <span v-if="currentSourceName" class="source-origin-badge">{{ currentSourceName }}</span>
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
          <button v-if="book.kind === 'online'" :disabled="busy || cachingBusy" @click="openChangeSourceModal">
            <Shuffle :size="17" />换源
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
      <div class="section-heading toc-heading-bar">
        <h2>目录 <span class="count">{{ chapters.length }}</span></h2>
        <div class="toc-controls-row">
          <button
            v-if="book.progress?.chapterId && chapters.length"
            type="button"
            class="secondary compact-btn"
            @click="scrollToCurrentChapter"
            title="定位到当前阅读章节"
          >
            <LocateFixed :size="15" />
            <span>定位当前章</span>
          </button>
          <button
            v-if="chapters.length > 1"
            type="button"
            class="secondary compact-btn"
            @click="tocReversed = !tocReversed"
            :title="tocReversed ? '切换为正序目录' : '切换为倒序目录（最新章节在前）'"
          >
            <ArrowUpDown :size="15" />
            <span>{{ tocReversed ? '倒序 · 最新在前' : '正序 · 从头看起' }}</span>
          </button>
          <label class="search-input">
            <Search :size="16" />
            <input v-model="query" aria-label="搜索章节" placeholder="搜索章节" />
          </label>
        </div>
      </div>
      <div class="chapter-grid">
        <a
          v-for="chapter in filtered"
          :key="chapter.id"
          :href="`#/read/${id(book.id)}/${id(chapter.id)}`"
          :class="{ selected: book.progress?.chapterId === chapter.id }"
        >
          <span>{{ String(chapter.index + 1).padStart(2, '0') }}</span>
          <em class="chapter-link-title">{{ chapter.title }}</em>
          <small v-if="cachedChapterIds.has(chapter.id)" class="chapter-cached-dot" title="已离线缓存">已缓存</small>
          <BookOpen v-if="book.progress?.chapterId === chapter.id" :size="15" />
        </a>
      </div>
      <p v-if="!filtered.length" class="empty">
        {{ chapters.length ? '没有匹配的章节。' : '暂无章节，在线书籍可尝试更新目录或点击换源。' }}
      </p>
    </section>

    <!-- 详情页换源中心弹窗 -->
    <div v-if="changeSourceOpen" class="modal-backdrop" @click="changeSourceOpen = false">
      <div class="modal-card change-source-dialog" @click.stop>
        <div class="modal-header">
          <div>
            <h3>换源中心</h3>
            <p class="muted" style="font-size: 13px; margin-top: 2px;">
              当前书籍：「{{ book.title }}」· 切换书源后自动保留阅读进度
            </p>
          </div>
          <button class="icon-button" @click="changeSourceOpen = false" aria-label="关闭换源">
            <X :size="18" />
          </button>
        </div>

        <div class="modal-search-row">
          <input
            v-model="changeSourceQuery"
            placeholder="搜索书籍关键词进行换源…"
            @keyup.enter="executeSearchSources"
          />
          <button class="primary" :disabled="changeSourceLoading" @click="executeSearchSources">
            <LoaderCircle v-if="changeSourceLoading" class="spin" :size="15" />
            <Search v-else :size="15" />
            <span>重新搜索</span>
          </button>
        </div>

        <div v-if="changeSourceError" class="error message-box" style="margin: 12px 20px 0;">
          <span>{{ changeSourceError }}</span>
          <a href="#/sources" class="text-button">前往书源管理</a>
        </div>

        <div class="change-source-body">
          <div v-if="changeSourceLoading" class="empty" style="padding: 40px 0;">
            <LoaderCircle class="spin" :size="28" />
            <p class="muted" style="margin-top: 10px;">正在检索你的可用书源匹配书籍…</p>
          </div>

          <div v-else-if="!changeSourceResults.length" class="empty" style="padding: 40px 0;">
            <p class="muted">未检索到其他可用书源结果，可修改关键词后重新搜索。</p>
          </div>

          <div v-else class="source-candidates-list">
            <div
              v-for="(item, idx) in changeSourceResults"
              :key="idx"
              :class="['source-card-item', { active: book.sourceId === item.sourceId }]"
              @click="applyChangeSource(item)"
            >
              <div class="source-badge-tag">
                {{ sourcesMap[item.sourceId] || '书源 ' + item.sourceId.substring(0, 8) }}
              </div>
              <div class="source-info-main">
                <div class="source-title-row">
                  <strong>{{ item.title }}</strong>
                  <span v-if="item.author" class="source-author">作者：{{ item.author }}</span>
                  <span v-if="book.sourceId === item.sourceId" class="current-badge">当前在用</span>
                </div>
                <p class="source-url-text muted">{{ item.url }}</p>
                <p v-if="item.intro" class="source-intro-text">{{ item.intro }}</p>
              </div>
              <button
                class="source-action-btn"
                :disabled="changeSourceSubmitting || book.sourceId === item.sourceId"
              >
                <LoaderCircle v-if="changeSourceSubmitting" class="spin" :size="14" />
                <span v-else>{{ book.sourceId === item.sourceId ? '使用中' : '切换此源' }}</span>
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </template>
</template>
