<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import {
  Plus,
  Search,
  BookOpen,
  ArrowUpRight,
  Upload,
  FileText,
  LoaderCircle,
  RefreshCw,
  Library,
  ChevronLeft,
  ChevronRight,
  Trash2,
  LayoutGrid,
  List as ListIcon,
  Tag,
  CheckCircle2,
  AlertCircle
} from 'lucide-vue-next'
import { api, id, message } from '../api'
import { removeBookCache } from '../offline'
import { showConfirm } from '../confirm'
import type { Book, Chapter } from '../types'
import BookCover from './BookCover.vue'

// 模块级书架与续读标题缓存，避免切页、阅读器退回时触发骨架屏与白屏闪烁
let cachedBooks: Book[] = []
let cachedContinuingTitle = ''

if (typeof window !== 'undefined') {
  window.addEventListener('session-expired', () => {
    cachedBooks = []
    cachedContinuingTitle = ''
  })
}

const props = defineProps<{ importMode: boolean }>()

const books = ref<Book[]>(cachedBooks)
const loading = ref(cachedBooks.length === 0)
const refreshing = ref(false)
const error = ref('')
const notice = ref('')
const removingId = ref('')
const query = ref('')
const group = ref('')
const sort = ref('recent')
const viewMode = ref<'grid' | 'list'>('grid')
const file = ref<File | null>(null)
const importing = ref(false)
const dragging = ref(false)
const imported = ref<Book | null>(null)

// 续读卡片的最新章节名称
const continuingChapterTitle = ref(cachedContinuingTitle)

// 分页控制
const page = ref(1)
const pageSize = ref(5)

function cleanAuthor(author: string): string {
  if (!author) return '佚名'
  return author.replace(/^(作者|著者|编著|著)[:：\s]*/i, '').trim() || '佚名'
}

const groups = computed(() => [...new Set(books.value.map(b => b.group).filter(Boolean))])

const isDuplicateTitle = (title: string) => {
  return books.value.filter(b => b.title.trim() === title.trim()).length > 1
}

const filtered = computed(() => {
  const list = books.value.filter(
    b => (!group.value || b.group === group.value) && `${b.title} ${b.author}`.toLowerCase().includes(query.value.toLowerCase())
  )
  if (sort.value === 'title') {
    return [...list].sort((a, b) => a.title.localeCompare(b.title, 'zh-CN'))
  }
  return list
})

const totalPages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize.value)))
const pagedBooks = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return filtered.value.slice(start, start + pageSize.value)
})

watch([query, group, sort], () => {
  page.value = 1
})

const continuing = computed(() => books.value.find(b => b.progress))

async function fetchContinuingChapterTitle(bookItem: Book) {
  if (!bookItem?.id || !bookItem.progress?.chapterId) {
    continuingChapterTitle.value = ''
    cachedContinuingTitle = ''
    return
  }
  try {
    const chapters = await api<Chapter[]>(`/books/${id(bookItem.id)}/chapters`)
    const target = chapters?.find(c => c.id === bookItem.progress?.chapterId)
    if (target) {
      continuingChapterTitle.value = target.title
      cachedContinuingTitle = target.title
    } else {
      continuingChapterTitle.value = ''
      cachedContinuingTitle = ''
    }
  } catch {
    // 保留已有缓存标题，避免网络抖动导致闪现
  }
}

watch(continuing, (newVal) => {
  if (newVal) {
    fetchContinuingChapterTitle(newVal)
  }
}, { immediate: true })

async function load(isManual = false) {
  if (books.value.length === 0) {
    loading.value = true
  } else if (isManual) {
    refreshing.value = true
  }
  error.value = ''
  try {
    const list = (await api<Book[]>('/books')) || []
    books.value = list
    cachedBooks = list
    if (continuing.value) {
      fetchContinuingChapterTitle(continuing.value)
    }
  } catch (e) {
    if (books.value.length === 0) {
      error.value = message(e)
    }
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

function select(f?: File) {
  if (importing.value) return
  error.value = ''
  imported.value = null
  file.value = null
  if (!f) return
  if (!/\\.(txt|epub)$/i.test(f.name)) {
    error.value = '请选择 .txt 或 .epub 文件。'
    return
  }
  if (!f.size) {
    error.value = '文件为空，请选择包含有效正文的文件。'
    return
  }
  if (f.size > 100 * 1024 * 1024) {
    error.value = '文件超过服务端 100MB 限制，请精简后重新上传。'
    return
  }
  file.value = f
}

function dropped(e: DragEvent) {
  dragging.value = false
  if (importing.value) return
  select(e.dataTransfer?.files[0])
}

async function upload() {
  if (!file.value || importing.value) return
  importing.value = true
  error.value = ''
  const body = new FormData()
  body.append('file', file.value)
  try {
    imported.value = await api<Book>('/imports', 'POST', body)
    if (imported.value) {
      cachedBooks = [imported.value, ...cachedBooks.filter(b => b.id !== imported.value?.id)]
    }
    file.value = null
  } catch (e) {
    error.value = message(e)
  } finally {
    importing.value = false
  }
}

function openReaderDirect(bookId: string) {
  location.hash = `/read/${id(bookId)}`
}

async function removeBook(book: Book) {
  if (removingId.value) return
  const ok = await showConfirm({
    title: '移除书籍',
    message: `确定从书架移除《${book.title}》吗？`,
    confirmText: '确认移除',
    cancelText: '取消',
    danger: true
  })
  if (!ok) return
  removingId.value = book.id
  error.value = ''
  notice.value = ''
  try {
    await api(`/books/${id(book.id)}`, 'DELETE')
    await removeBookCache(book.id)
    books.value = books.value.filter(b => b.id !== book.id)
    cachedBooks = books.value
    if (continuing.value) {
      fetchContinuingChapterTitle(continuing.value)
    } else {
      continuingChapterTitle.value = ''
      cachedContinuingTitle = ''
    }
    if (pagedBooks.value.length === 0 && page.value > 1) {
      page.value = Math.max(1, totalPages.value)
    }
    notice.value = `《${book.title}》已从书架移除。`
    setTimeout(() => {
      if (notice.value.includes(book.title)) notice.value = ''
    }, 3000)
  } catch (e) {
    error.value = message(e)
  } finally {
    removingId.value = ''
  }
}

onMounted(() => {
  if (!props.importMode) load()
  else loading.value = false
})
</script>

<template>
  <header :class="['page-header', importMode ? 'import-header' : 'shelf-header']">
    <div>
      <h1>
        {{ importMode ? '导入书籍' : '我的书架' }}
        <span v-if="!importMode && (!loading || books.length)" class="count">{{ books.length }}</span>
      </h1>
      
    </div>
    <div class="header-actions">
      <a v-if="!importMode" class="button primary" href="#/import">
        <Plus :size="17" />导入书籍
      </a>
    </div>
  </header>

  <div v-if="error" class="error" role="alert">
    {{ error }} <button v-if="!importMode" class="text-button" @click="load()">重试</button>
  </div>
  <div v-if="notice" class="success" role="status">
    {{ notice }}
  </div>

  <!-- 导入页面分步向导模式 -->
  <template v-if="importMode">
    <section class="import-stepper-wrap">
      <form class="panel import-stepper-panel" @submit.prevent="upload">
        

        <div
          :class="['dropzone', { dragging, 'has-file': !!file }]"
          @dragover.prevent="dragging = true"
          @dragleave.prevent="dragging = false"
          @drop.prevent="dropped"
        >
          <span class="round-icon">
            <Upload :size="26" />
          </span>
          <h2>{{ file ? file.name : '拖放或选择电子书' }}</h2>
          <p>
            {{
              file
                ? `${(file.size / 1024 / 1024).toFixed(2)} MB`
                : '支持 TXT / EPUB'
            }}
          </p>

          <label class="button secondary file-picker-trigger">
            <FileText :size="16" />
            <span>{{ file ? '重新选择' : '选择文件' }}</span>
            <input
              type="file"
              accept=".txt,.epub"
              :disabled="importing"
              @change="select(($event.target as HTMLInputElement).files?.[0])"
            />
          </label>
        </div>

        <!-- 主导入按钮：未选文件时不误导，选好后高亮 -->
        <div class="import-action-bar">
          <button
            v-if="file"
            type="submit"
            class="button primary wide import-execute-btn"
            :disabled="importing"
          >
            <LoaderCircle v-if="importing" class="spin" :size="18" />
            <Upload v-else :size="18" />
            {{ importing ? '正在导入…' : '导入书架' }}
          </button>
          
        </div>

        
      </form>

      
    </section>

    <!-- 导入成功反馈 -->
    <div v-if="imported" class="success import-success" role="status">
      <div>
        <strong>《{{ imported.title }}》已加入书架</strong>
        <p>共 {{ imported.chapterCount }} 章</p>
      </div>
      <div class="actions">
        <a class="button primary" :href="`#/read/${id(imported.id)}`">
          <BookOpen :size="18" />立即阅读
        </a>
        <a class="button secondary" :href="`#/book/${id(imported.id)}`">
          书籍详情<ArrowUpRight :size="17" />
        </a>
      </div>
    </div>
  </template>

  <!-- 正常书架页面 -->
  <template v-else>
    <!-- 续读卡片（增加章节名与明确进度表达，消除歧义） -->
    <section v-if="continuing && (!loading || books.length)" class="continue-card">
      <div class="continue-card-content">
        <span class="eyebrow">最近阅读</span>
        <h2>{{ continuing.title }}</h2>
        <p class="continue-chapter-info">
          <span class="continue-author">{{ cleanAuthor(continuing.author) }}</span>
          <span class="dot">·</span>
          <span class="continue-chapter-title">{{ continuingChapterTitle || '当前章节' }}</span>
          <span class="dot">·</span>
          <span class="continue-progress-val">已读 {{ Math.round((continuing.progress?.position || 0) * 100) }}%</span>
        </p>
        <div class="continue-progress-bar-wrap">
          <div
            class="continue-progress-bar-fill"
            :style="{ width: `${Math.round((continuing.progress?.position || 0) * 100)}%` }"
            :title="`本章进度：${Math.round((continuing.progress?.position || 0) * 100)}%`"
          ></div>
        </div>
        <div class="continue-action-row">
          <a class="button primary continue-action" :href="`#/read/${id(continuing.id)}`">
            <BookOpen :size="18" />继续阅读
          </a>
          <a class="button secondary" :href="`#/book/${id(continuing.id)}`">详情</a>
        </div>
      </div>
    </section>

    <!-- 工具栏：分组过滤、搜索、排序、视图切换 -->
    <div class="library-toolbar">
      <div class="tabs">
        <button :class="{ selected: !group }" @click="group = ''">全部</button>
        <button v-for="g in groups" :key="g" :class="{ selected: group === g }" @click="group = g">{{ g }}</button>
      </div>
      <div class="toolbar-controls">
        <label class="search-input">
          <Search :size="16" />
          <input v-model="query" aria-label="筛选书架藏书" placeholder="搜索书名或作者…" />
        </label>
        <select v-model="sort" aria-label="书架排序方式">
          <option value="recent">最近阅读</option>
          <option value="title">书名排序</option>
        </select>
        <div class="view-mode-toggle" role="group" aria-label="视图切换">
          <button
            type="button"
            :class="['icon-button', { active: viewMode === 'grid' }]"
            @click="viewMode = 'grid'"
            title="网格视图"
            aria-label="网格视图"
          >
            <LayoutGrid :size="16" />
          </button>
          <button
            type="button"
            :class="['icon-button', { active: viewMode === 'list' }]"
            @click="viewMode = 'list'"
            title="列表视图"
            aria-label="列表视图"
          >
            <ListIcon :size="16" />
          </button>
        </div>
        <button class="icon-button" @click="load(true)" :disabled="loading || refreshing" aria-label="刷新书架" title="刷新书架">
          <RefreshCw :class="{ spin: refreshing }" :size="16" />
        </button>
      </div>
    </div>

    <!-- 加载骨架屏 -->
    <!-- 加载骨架屏：仅在无任何缓存书籍且首次加载时展示 -->
    <div v-if="loading && !books.length" class="book-grid" aria-label="正在加载书架" aria-busy="true">
      <div v-for="n in 6" :key="n" class="skeleton-book">
        <div class="skeleton"></div>
        <span class="skeleton"></span>
      </div>
    </div>

    <!-- 空书架 -->
    <section v-else-if="!books.length && !loading && !error" class="empty shelf-empty">
      <span class="round-icon"><Library :size="32" /></span>
      <h2>暂无书籍</h2>
      <div class="actions">
        <a class="button primary" href="#/import"><Plus :size="18" />导入书籍</a>
        <a class="button secondary" href="#/search">搜索书籍<ArrowUpRight :size="17" /></a>
      </div>
    </section>

    <!-- 筛选无匹配结果：联动搜索引导 -->
    <section v-else-if="!filtered.length && !error" class="empty filter-empty">
      <Search :size="32" />
      <h2>未找到“{{ query }}”</h2>
      <div class="actions">
        <a class="button primary" :href="`#/search?q=${encodeURIComponent(query)}`">
          搜索《{{ query }}》<ArrowUpRight :size="17" />
        </a>
        <button class="button secondary" @click="query = ''; group = ''">清除筛选</button>
      </div>
    </section>

    <!-- 书籍列表展示区 -->
    <template v-else>
      <!-- 网格视图 -->
      <div v-if="viewMode === 'grid'" class="book-grid">
        <a
          v-for="(book, index) in pagedBooks"
          :key="book.id"
          :href="`#/book/${id(book.id)}`"
          class="book-card"
        >
          <BookCover :title="book.title" :kind="book.kind" :index="index" />

          <!-- 删除按钮 -->
          <button
            type="button"
            class="book-remove-btn"
            :disabled="removingId === book.id"
            :aria-label="`从书架移除 ${book.title}`"
            title="从书架移除"
            @click.stop.prevent="removeBook(book)"
          >
            <LoaderCircle v-if="removingId === book.id" class="spin" :size="14" />
            <Trash2 v-else :size="14" />
          </button>

          <div class="book-meta">
            <!-- 来源标签：同名书显式高亮区分 -->
            <div class="book-source-tag-row">
              <span
                :class="['book-source-tag', { 'duplicate-source': isDuplicateTitle(book.title) }]"
                :title="isDuplicateTitle(book.title) ? '' : ''"
              >
                {{ book.sourceName || (book.kind === 'local' ? '本地导入' : '网络书源') }}
              </span>
            </div>

            <h3 :title="book.title">{{ book.title }}</h3>
            <p class="book-author" :title="cleanAuthor(book.author)">{{ cleanAuthor(book.author) }}</p>

            <div class="book-status">
              <span>
                {{ book.progress ? '阅读中' : '未读' }}
                <template v-if="book.progress"> · {{ Math.round(book.progress.position * 100) }}%</template>
              </span>
              <span>{{ book.chapterCount }} 章</span>
            </div>

            <div class="progress-track" v-if="book.progress">
              <span :style="{ width: `${book.progress.position * 100}%` }"></span>
            </div>

            <!-- 操作按钮：继续阅读不换行，目录详情次级点击 -->
            <div class="book-card-quick-actions">
              <button
                type="button"
                class="book-quick-read-btn"
                @click.stop.prevent="openReaderDirect(book.id)"
              >
                <BookOpen :size="13" />
                <span>{{ book.progress ? '继续阅读' : '立即阅读' }}</span>
              </button>
              <span class="book-quick-detail-hint">详情</span>
            </div>
          </div>
        </a>
      </div>

      <!-- 紧凑列表视图（更适合书多时的快速扫读） -->
      <div v-else class="book-list">
        <div
          v-for="book in pagedBooks"
          :key="book.id"
          class="book-list-item"
        >
          <div class="book-list-main" @click="openReaderDirect(book.id)">
            <div class="book-list-titles">
              <h3 :title="book.title">{{ book.title }}</h3>
              <div class="book-list-sub">
                <span class="book-author">{{ cleanAuthor(book.author) }}</span>
                <span class="dot">·</span>
                <span :class="['book-source-tag', { 'duplicate-source': isDuplicateTitle(book.title) }]">
                  {{ book.sourceName || (book.kind === 'local' ? '本地导入' : '网络书源') }}
                </span>
                <span class="dot">·</span>
                <span class="book-chapters">{{ book.chapterCount }} 章</span>
              </div>
            </div>
            <div class="book-list-progress">
              <span v-if="book.progress">已读 {{ Math.round(book.progress.position * 100) }}%</span>
              <span v-else class="muted">未读</span>
            </div>
          </div>
          <div class="book-list-actions">
            <button
              type="button"
              class="button primary small"
              @click="openReaderDirect(book.id)"
            >
              <BookOpen :size="14" />
              <span>{{ book.progress ? '继续' : '阅读' }}</span>
            </button>
            <a class="button secondary small" :href="`#/book/${id(book.id)}`">目录</a>
            <button
              type="button"
              class="icon-button danger-icon small"
              :disabled="removingId === book.id"
              @click="removeBook(book)"
              title="从书架移除"
            >
              <LoaderCircle v-if="removingId === book.id" class="spin" :size="14" />
              <Trash2 v-else :size="14" />
            </button>
          </div>
        </div>
      </div>

      <!-- 书架分页栏 -->
      <div class="pagination-bar" v-if="filtered.length > pageSize">
        <div class="pagination-info">
          <span>第 {{ page }} / {{ totalPages }} 页 (共 {{ filtered.length }} 本)</span>
          <select v-model="pageSize" @change="page = 1" aria-label="每页显示本数" class="page-size-select">
            <option :value="6">6 本/页</option>
            <option :value="12">12 本/页</option>
            <option :value="24">24 本/页</option>
          </select>
        </div>
        <div class="pagination-controls">
          <button
            class="secondary pagination-btn"
            :disabled="page <= 1"
            @click="page--"
          >
            <ChevronLeft :size="16" /> 上一页
          </button>
          <span class="pagination-current">{{ page }}</span>
          <button
            class="secondary pagination-btn"
            :disabled="page >= totalPages"
            @click="page++"
          >
            下一页 <ChevronRight :size="16" />
          </button>
        </div>
      </div>

      <footer class="shelf-footer">
        共 {{ filtered.length }} 本
      </footer>
    </template>
  </template>
</template>
