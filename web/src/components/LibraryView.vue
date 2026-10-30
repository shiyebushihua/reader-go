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
  Trash2
} from 'lucide-vue-next'
import { api, id, message } from '../api'
import { removeBookCache } from '../offline'
import type { Book } from '../types'
import BookCover from './BookCover.vue'

const props = defineProps<{ importMode: boolean }>()

const books = ref<Book[]>([])
const loading = ref(true)
const error = ref('')
const notice = ref('')
const removingId = ref('')
const query = ref('')
const group = ref('')
const sort = ref('recent')
const file = ref<File | null>(null)
const importing = ref(false)
const dragging = ref(false)
const imported = ref<Book | null>(null)

// 分页控制
const page = ref(1)
const pageSize = ref(5)

const groups = computed(() => [...new Set(books.value.map(b => b.group).filter(Boolean))])

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

async function load() {
  loading.value = true
  error.value = ''
  try {
    books.value = (await api<Book[]>('/books')) || []
  } catch (e) {
    error.value = message(e)
  } finally {
    loading.value = false
  }
}

function select(f?: File) {
  if (importing.value) return
  error.value = ''
  imported.value = null
  file.value = null
  if (!f) return
  if (!/\.(txt|epub)$/i.test(f.name)) {
    error.value = '请选择 .txt 或 .epub 文件。'
    return
  }
  if (!f.size) {
    error.value = '文件为空，请选择包含正文的文件。'
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
  if (!confirm(`确定从书架移除《${book.title}》吗？阅读进度、书签和本地文件也会被删除，此操作无法撤销。`)) return
  removingId.value = book.id
  error.value = ''
  notice.value = ''
  try {
    await api(`/books/${id(book.id)}`, 'DELETE')
    await removeBookCache(book.id)
    books.value = books.value.filter(b => b.id !== book.id)
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
      <span class="eyebrow">{{ importMode ? 'BRING YOUR OWN STORIES' : 'THE PERSONAL COLLECTION' }}</span>
      <h1>{{ importMode ? '让好书，住进书架' : '我的书架' }}<span v-if="!importMode && !loading" class="count">{{ books.length }}</span></h1>
      <p>{{ importMode ? '从你的设备导入 TXT 或 EPUB 电子书，随时随地接着读。' : '收藏每一次心动，续上每一段故事。' }}</p>
    </div>
    <a v-if="!importMode" class="button primary" href="#/import"><Plus :size="18" />导入书籍</a>
  </header>

  <div v-if="error" class="error" role="alert">
    {{ error }} <button v-if="!importMode" class="text-button" @click="load">重试</button>
  </div>
  <div v-if="notice" class="success" role="status">
    {{ notice }}
  </div>

  <template v-if="importMode">
    <section class="import-grid">
      <form class="panel import-panel" @submit.prevent="upload">
        <div :class="['dropzone', { dragging, 'has-file': !!file }]" @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false" @drop.prevent="dropped">
          <span class="round-icon"><Upload :size="26" /></span>
          <h2>{{ file ? file.name : '把下一本好书带进来' }}</h2>
          <p>{{ file ? `${(file.size / 1024 / 1024).toFixed(2)} MB · 已准备好导入` : '拖放 TXT / EPUB 文件到这里，或点击下方按钮从设备中选择' }}</p>
          <label class="button secondary file-picker">
            <FileText :size="16" />
            <span>{{ file ? '重新选择文件' : '选择本地文件 (.txt / .epub)' }}</span>
            <input type="file" accept=".txt,.epub" :disabled="importing" @change="select(($event.target as HTMLInputElement).files?.[0])" />
          </label>
        </div>
        <button class="primary wide import-submit-btn" :disabled="!file || importing">
          <LoaderCircle v-if="importing" class="spin" :size="18" />
          <Upload v-else :size="18" />
          {{ importing ? '正在上传并解析，请稍候…' : '导入到我的书架' }}
        </button>
        <p class="fine-print">导入完成后直接加入书架。文件大小与章节限制由服务器校验。</p>
      </form>

      <aside class="panel import-notes">
        <div class="import-notes-head">
          <span class="eyebrow">READ YOUR WAY</span>
          <h2>熟悉的文字，舒适的新居。</h2>
        </div>
        <div class="import-note-list">
          <div class="import-note-card">
            <span class="import-note-icon"><FileText :size="20" /></span>
            <div class="import-note-text">
              <h3>TXT 纯文本</h3>
              <p>支持 UTF-8 与 GBK 编码，智能识别卷章目录。</p>
            </div>
          </div>
          <div class="import-note-card">
            <span class="import-note-icon"><BookOpen :size="20" /></span>
            <div class="import-note-text">
              <h3>EPUB 电子书</h3>
              <p>解析目录与正文，以清爽护眼排版呈现。</p>
            </div>
          </div>
        </div>
        <p class="fine-print">请仅导入你拥有合法阅读权利的内容。</p>
      </aside>
    </section>

    <div v-if="imported" class="success import-success" role="status">
      <div>
        <strong>《{{ imported.title }}》已加入书架</strong>
        <p>共 {{ imported.chapterCount }} 章，新的故事等你翻开。</p>
      </div>
      <div class="actions">
        <a class="button primary" :href="`#/read/${id(imported.id)}`"><BookOpen :size="18" />立即阅读</a>
        <a class="button secondary" :href="`#/book/${id(imported.id)}`">查看目录<ArrowUpRight :size="17" /></a>
      </div>
    </div>
  </template>

  <template v-else>
    <section v-if="continuing && !loading" class="continue-card">
      <div>
        <span class="eyebrow continue-desktop-label">PICK UP WHERE YOU LEFT OFF</span><span class="continue-mobile-label">接着读</span>
        <h2>{{ continuing.title }}</h2>
        <p>{{ continuing.author || '佚名' }}<span class="dot">·</span>本章已读 {{ Math.round((continuing.progress?.position || 0) * 100) }}%</p>
        <a class="button primary continue-action" :href="`#/read/${id(continuing.id)}`"><BookOpen :size="18" />继续阅读</a>
      </div>
      <div class="continue-decoration" aria-hidden="true">
        <span>再翻一页</span>
        <BookOpen :size="110" :stroke-width="0.7" />
      </div>
    </section>

    <div class="library-toolbar">
      <div class="tabs">
        <button :class="{ selected: !group }" @click="group = ''">全部藏书</button>
        <button v-for="g in groups" :key="g" :class="{ selected: group === g }" @click="group = g">{{ g }}</button>
      </div>
      <div class="toolbar-controls">
        <label class="search-input">
          <Search :size="17" />
          <input v-model="query" aria-label="筛选书架" placeholder="搜索书名、作者" />
        </label>
        <select v-model="sort" aria-label="排序方式">
          <option value="recent">最近加入</option>
          <option value="title">书名排序</option>
        </select>
        <button class="icon-button" @click="load" :disabled="loading" aria-label="刷新书架">
          <RefreshCw :size="17" />
        </button>
      </div>
    </div>

    <div v-if="loading" class="book-grid" aria-label="正在加载书架" aria-busy="true">
      <div v-for="n in 5" :key="n" class="skeleton-book">
        <div class="skeleton"></div>
        <span class="skeleton"></span>
      </div>
    </div>

    <section v-else-if="!books.length && !error" class="empty shelf-empty">
      <span class="round-icon"><Library :size="32" /></span>
      <span class="eyebrow">A NEW CHAPTER STARTS HERE</span>
      <h2>书架留白，故事待续</h2>
      <p>导入一本珍藏的电子书，或去发现你的下一本好书。<br>每一次阅读，都从翻开第一页开始。</p>
      <div class="actions">
        <a class="button primary" href="#/import"><Plus :size="18" />导入第一本书</a>
        <a class="button secondary" href="#/search">发现好书<ArrowUpRight :size="17" /></a>
      </div>
    </section>

    <section v-else-if="!filtered.length && !error" class="empty">
      <Search :size="32" />
      <h2>没有找到匹配的藏书</h2>
      <p>试试其他书名、作者或分组。</p>
      <button @click="query = ''; group = ''">清除筛选</button>
    </section>

    <template v-else>
      <div class="book-grid">
        <a
          v-for="(book, index) in pagedBooks"
          :key="book.id"
          :href="`#/book/${id(book.id)}`"
          class="book-card"
          :style="{ '--stagger': `${Math.min(index, 8) * 25}ms` }"
        >
          <BookCover :title="book.title" :kind="book.kind" :index="index" />
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
            <h3>{{ book.title }}</h3>
            <p>{{ book.author || '佚名' }}</p>
            <div class="book-status">
              <span>
                {{ book.progress ? '阅读中' : '未开始' }}
                <template v-if="book.progress"> · 本章 {{ Math.round(book.progress.position * 100) }}%</template>
              </span>
              <span>{{ book.chapterCount }} 章</span>
            </div>
            <div class="progress-track" v-if="book.progress">
              <span :style="{ width: `${book.progress.position * 100}%` }"></span>
            </div>
            <div class="book-card-quick-actions">
              <button
                type="button"
                class="book-quick-read-btn"
                @click.stop.prevent="openReaderDirect(book.id)"
              >
                <BookOpen :size="13" />
                <span>{{ book.progress ? '继续阅读' : '立即阅读' }}</span>
              </button>
              <span class="book-quick-detail-hint">目录 · 详情</span>
            </div>
          </div>
        </a>
      </div>

      <!-- 书架分页栏 -->
      <div class="pagination-bar" v-if="filtered.length > pageSize">
        <div class="pagination-info">
          <span>第 {{ page }} / {{ totalPages }} 页 (共 {{ filtered.length }} 本藏书)</span>
          <select v-model="pageSize" @change="page = 1" aria-label="书架每页显示本数" class="page-size-select">
            <option :value="3">3 本/页</option>
            <option :value="5">5 本/页</option>
            <option :value="10">10 本/页</option>
            <option :value="20">20 本/页</option>
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
        {{ filtered.length }} 本藏书 <span>·</span> 把时间留给值得的文字
      </footer>
    </template>
  </template>
</template>
