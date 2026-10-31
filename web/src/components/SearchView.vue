<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import {
  Search,
  ArrowRight,
  Plus,
  Check,
  LoaderCircle,
  Globe2,
  Square,
  ChevronDown,
  ChevronUp,
  ChevronLeft,
  ChevronRight,
  AlertCircle,
  Sparkles,
  Zap,
  Radio,
  SlidersHorizontal,
  X,
  CheckSquare,
  Clock,
  BookOpen
} from 'lucide-vue-next'
import { api, id, message, streamSearch, type StreamSearchProgress, type StreamSearchStart } from '../api'
import type { Source, SearchResult, Book } from '../types'
import BookCover from './BookCover.vue'

interface SourceStatus {
  id: string
  name: string
  status: 'pending' | 'searching' | 'success' | 'error' | 'timeout'
  count: number
}

const sources = ref<Source[]>([])
const selected = ref<string[]>([])
const keyword = ref('')
const results = ref<SearchResult[]>([])
const loading = ref(true)
const searching = ref(false)
const searched = ref(false)
const stopped = ref(false)
const error = ref('')
const adding = ref('')
const readingKey = ref('')
const added = ref<Record<string, string>>({})
const searchedKeyword = ref('')
const sourceFilter = ref<'hasBooks' | 'searching' | 'all'>('hasBooks')

// 搜索范围折叠卡片与超时控制
const scopeExpanded = ref(false)
const timeoutSeconds = ref(0)
const activePreset = ref<'all' | 'top20' | 'top50' | 'group' | 'custom'>('all')
const selectedGroup = ref('')
const showPickerModal = ref(false)

// 书源挑选器弹窗内部状态（带搜索、分组过滤与分页）
const pickerQuery = ref('')
const pickerGroup = ref('')
const pickerPage = ref(1)
const pickerPageSize = 5

// 搜索结果分页状态（保持每页 5 条不变）
const resultPage = ref(1)
const resultPageSize = ref(5)
const hudExpanded = ref(false)

const sourceStatuses = ref<Record<string, SourceStatus>>({})
const completedSources = ref(0)
const totalSources = ref(0)
const currentSearchingSource = ref('')
const searchSeconds = ref(0)

let abortCtrl: AbortController | null = null
let timerId: ReturnType<typeof setInterval> | null = null

function normalizeMatchText(s: string): string {
  return (s || '')
    .toLowerCase()
    .trim()
    .replace(/[《》「」『』（）()【】\[\]：:·\s\t]+/g, '')
}

function scoreResult(item: SearchResult, kwRaw: string): number {
  const kw = normalizeMatchText(kwRaw)
  if (!kw) return 0
  const title = normalizeMatchText(item.title)
  const author = normalizeMatchText(item.author)
  if (!title) return 0
  if (title === kw) return 100
  if (title.startsWith(kw)) return 85
  if (title.includes(kw)) return 75
  if (author && author === kw) return 65
  if (author && author.includes(kw)) return 55
  if (title.length >= 2 && kw.includes(title)) return 40
  return 0
}

function isExactMatch(item: SearchResult): boolean {
  return scoreResult(item, searchedKeyword.value) === 100
}

function sortResultsByRelevance(list: SearchResult[], kw: string): SearchResult[] {
  return [...list].sort((a, b) => {
    const sa = scoreResult(a, kw)
    const sb = scoreResult(b, kw)
    if (sa !== sb) return sb - sa
    return (a.title?.length || 0) - (b.title?.length || 0)
  })
}

const enabled = computed(() => sources.value.filter(s => s.enabled))

const sourceGroups = computed(() => {
  const set = new Set<string>()
  for (const s of enabled.value) {
    if (s.group && s.group.trim()) set.add(s.group.trim())
  }
  return Array.from(set).sort()
})

const scopePresetLabel = computed(() => {
  if (activePreset.value === 'all') return '全部可用源'
  if (activePreset.value === 'top20') return '前 20 源'
  if (activePreset.value === 'top50') return '前 50 源'
  if (activePreset.value === 'group') return `分组：${selectedGroup.value || '全部'}`
  return `自定义 (${selected.value.length} 源)`
})

// 书源选择器弹窗内的过滤与分页
const filteredPickerSources = computed(() => {
  return enabled.value.filter(s => {
    if (pickerGroup.value && s.group !== pickerGroup.value) return false
    if (pickerQuery.value.trim()) {
      const q = pickerQuery.value.trim().toLowerCase()
      return s.name.toLowerCase().includes(q) || s.url.toLowerCase().includes(q)
    }
    return true
  })
})

const pickerTotalPages = computed(() => {
  return Math.max(1, Math.ceil(filteredPickerSources.value.length / pickerPageSize))
})

const pagedPickerSources = computed(() => {
  const start = (pickerPage.value - 1) * pickerPageSize
  return filteredPickerSources.value.slice(start, start + pickerPageSize)
})

// 搜索结果分页
const totalResultPages = computed(() => {
  return Math.max(1, Math.ceil(results.value.length / resultPageSize.value))
})

const pagedResults = computed(() => {
  const start = (resultPage.value - 1) * resultPageSize.value
  return results.value.slice(start, start + resultPageSize.value)
})

watch(pickerQuery, () => {
  pickerPage.value = 1
})
watch(pickerGroup, () => {
  pickerPage.value = 1
})

const progressPercent = computed(() => {
  if (!totalSources.value) return 0
  return Math.min(100, Math.round((completedSources.value / totalSources.value) * 100))
})

const hitSourcesCount = computed(() => {
  return Object.values(sourceStatuses.value).filter(s => s.status === 'success' && s.count > 0).length
})

const activeSearchingSources = computed(() => {
  return Object.values(sourceStatuses.value).filter(s => s.status === 'searching')
})

const filteredSourceStatuses = computed(() => {
  const list = Object.values(sourceStatuses.value)
  if (sourceFilter.value === 'searching') {
    return list.filter(s => s.status === 'searching')
  }
  if (sourceFilter.value === 'hasBooks') {
    const hits = list.filter(s => s.status === 'success' && s.count > 0)
    return hits.length ? hits : list.filter(s => s.status === 'searching' || s.count > 0)
  }
  return list
})

const key = (r: SearchResult) => `${r.sourceId}:${r.url}`

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [srcList, bookList] = await Promise.all([
      api<Source[]>('/sources'),
      api<Book[]>('/books').catch(() => [] as Book[])
    ])
    sources.value = srcList || []
    const shelfMap: Record<string, string> = {}
    for (const b of bookList || []) {
      if (b.sourceId && b.url) {
        shelfMap[`${b.sourceId}:${b.url}`] = b.id
      }
    }
    added.value = shelfMap
    applyPreset('all')
  } catch (e) {
    error.value = message(e)
  } finally {
    loading.value = false
  }
}

function applyPreset(preset: 'all' | 'top20' | 'top50' | 'group' | 'custom') {
  activePreset.value = preset
  const all = enabled.value
  if (preset === 'all') {
    selected.value = all.map(s => s.id)
  } else if (preset === 'top20') {
    selected.value = all.slice(0, 20).map(s => s.id)
  } else if (preset === 'top50') {
    selected.value = all.slice(0, 50).map(s => s.id)
  } else if (preset === 'group') {
    if (selectedGroup.value) {
      selected.value = all.filter(s => s.group === selectedGroup.value).map(s => s.id)
    }
  }
}

function onGroupSelectChange(e: Event) {
  const g = (e.target as HTMLSelectElement).value
  selectedGroup.value = g
  if (g) {
    activePreset.value = 'group'
    applyPreset('group')
  } else {
    applyPreset('all')
  }
}

function openPicker() {
  pickerPage.value = 1
  pickerQuery.value = ''
  pickerGroup.value = selectedGroup.value || ''
  showPickerModal.value = true
}

function togglePickerSource(sourceId: string) {
  const idx = selected.value.indexOf(sourceId)
  if (idx >= 0) {
    selected.value.splice(idx, 1)
  } else {
    selected.value.push(sourceId)
  }
  activePreset.value = 'custom'
}

function selectCurrentPickerPage() {
  const pageIds = pagedPickerSources.value.map(s => s.id)
  const set = new Set([...selected.value, ...pageIds])
  selected.value = Array.from(set)
  activePreset.value = 'custom'
}

function deselectCurrentPickerPage() {
  const pageIds = new Set(pagedPickerSources.value.map(s => s.id))
  selected.value = selected.value.filter(id => !pageIds.has(id))
  activePreset.value = 'custom'
}

function clearPickerSelection() {
  selected.value = []
  activePreset.value = 'custom'
}

function selectAllPickerSources() {
  selected.value = enabled.value.map(s => s.id)
  activePreset.value = 'all'
}

function confirmPicker() {
  showPickerModal.value = false
}

function stopSearch() {
  if (abortCtrl) {
    abortCtrl.abort()
    abortCtrl = null
  }
  if (timerId) {
    clearInterval(timerId)
    timerId = null
  }
  searching.value = false
  stopped.value = true
  currentSearchingSource.value = ''
  searched.value = true
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    if (showPickerModal.value) {
      showPickerModal.value = false
    } else if (searching.value) {
      stopSearch()
    }
  }
}

function changeResultPage(p: number) {
  if (p < 1 || p > totalResultPages.value) return
  resultPage.value = p
  const el = document.getElementById('search-results-anchor')
  if (el) {
    el.scrollIntoView({ behavior: 'smooth' })
  }
}

async function search() {
  if (searching.value) {
    stopSearch()
    return
  }
  const kw = keyword.value.trim()
  if (!kw || !selected.value.length) return

  const targetIds = selected.value.slice(0, 1000)

  searching.value = true
  stopped.value = false
  error.value = ''
  searched.value = false
  results.value = []
  resultPage.value = 1
  searchedKeyword.value = kw
  completedSources.value = 0
  totalSources.value = targetIds.length
  searchSeconds.value = 0
  sourceFilter.value = 'hasBooks'

  const statuses: Record<string, SourceStatus> = {}
  for (const sid of targetIds) {
    const s = sources.value.find(item => item.id === sid)
    statuses[sid] = {
      id: sid,
      name: s?.name?.trim() || sid,
      status: 'pending',
      count: 0
    }
  }
  sourceStatuses.value = statuses

  if (timerId) clearInterval(timerId)
  const startTime = Date.now()
  timerId = setInterval(() => {
    searchSeconds.value = Math.round(((Date.now() - startTime) / 1000) * 10) / 10
  }, 100)

  abortCtrl = new AbortController()

  try {
    await streamSearch(
      { keyword: kw, sourceIds: targetIds, timeoutSeconds: timeoutSeconds.value },
      {
        onStart: (data: StreamSearchStart) => {
          totalSources.value = data.total
        },
        onProgress: (p: StreamSearchProgress) => {
          completedSources.value = p.completed
          currentSearchingSource.value = p.sourceName
          const target = sourceStatuses.value[p.sourceId]
          if (target) {
            target.status = p.status
            if (p.count !== undefined) target.count = p.count
          }

          if (p.results && p.results.length > 0) {
            const existingKeys = new Set(results.value.map(r => key(r)))
            const merged = [...results.value]
            let addedAny = false
            for (const item of p.results) {
              if (scoreResult(item, kw) <= 0) continue
              const k = key(item)
              if (!existingKeys.has(k)) {
                merged.push(item)
                existingKeys.add(k)
                addedAny = true
              }
            }
            if (addedAny) {
              results.value = sortResultsByRelevance(merged, kw)
            }
          }
        },
        onDone: () => {
          searching.value = false
          searched.value = true
          currentSearchingSource.value = ''
          if (timerId) {
            clearInterval(timerId)
            timerId = null
          }
          abortCtrl = null
        }
      },
      abortCtrl.signal
    )
  } catch (e: any) {
    if (abortCtrl?.signal.aborted) {
      return
    }
    try {
      const fallback = await api<SearchResult[]>('/search', 'POST', {
        keyword: kw,
        sourceIds: targetIds,
        timeoutSeconds: timeoutSeconds.value
      }) || []
      results.value = sortResultsByRelevance(
        fallback.filter(item => scoreResult(item, kw) > 0),
        kw
      )
    } catch (err) {
      error.value = message(err)
    }
  } finally {
    searching.value = false
    searched.value = true
    currentSearchingSource.value = ''
    if (timerId) {
      clearInterval(timerId)
      timerId = null
    }
    abortCtrl = null
  }
}

async function add(result: SearchResult) {
  adding.value = key(result)
  error.value = ''
  try {
    const book = await api<Book>('/books/online', 'POST', result)
    added.value[key(result)] = book.id
  } catch (e) {
    error.value = message(e)
  } finally {
    adding.value = ''
  }
}

async function readNow(result: SearchResult) {
  const k = key(result)
  if (added.value[k]) {
    location.hash = `/read/${id(added.value[k])}`
    return
  }
  readingKey.value = k
  error.value = ''
  try {
    const book = await api<Book>('/books/online', 'POST', result)
    added.value[k] = book.id
    location.hash = `/read/${id(book.id)}`
  } catch (e) {
    error.value = message(e)
  } finally {
    readingKey.value = ''
  }
}

onMounted(() => {
  load()
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  if (timerId) clearInterval(timerId)
  if (abortCtrl) abortCtrl.abort()
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <header class="page-header search-page-header">
    <div>
      <span class="eyebrow">SEARCH NOVELS</span>
      <h1>搜索</h1>
      <p>输入书名或作者，在你的个人书源中检索书籍。</p>
    </div>
    <Globe2 class="header-symbol" :size="38" :stroke-width="1.2" />
  </header>

  <form class="panel search-panel" @submit.prevent="search">
    <!-- 高对比度一体化搜书输入框（放大镜与输入框合体） -->
    <div class="search-bar-row">
      <div class="search-input-box">
        <span class="search-icon-slot" aria-hidden="true">
          <Search :size="20" />
        </span>
        <input
          v-model="keyword"
          required
          aria-label="搜索书名或作者"
          placeholder="输入准确书名或作者名，例如：凡人修仙传…"
        />
        <button
          v-if="keyword && !searching"
          type="button"
          class="search-clear-btn"
          aria-label="清空输入"
          @click="keyword = ''"
        >
          <X :size="16" />
        </button>
      </div>

      <button
        v-if="searching"
        type="button"
        class="search-stop-btn search-main-btn"
        @click="stopSearch"
      >
        <Square :size="16" />
        <span>停止 (Esc)</span>
      </button>
      <button
        v-else
        type="submit"
        class="primary search-main-btn"
        :disabled="loading || !selected.length || !keyword.trim()"
      >
        <span>搜索</span>
        <ArrowRight :size="18" />
      </button>
    </div>

    <!-- 上下折叠式「搜索范围与超时设置」卡片 -->
    <div class="scope-collapse-card">
      <button
        type="button"
        class="scope-collapse-header"
        :aria-expanded="scopeExpanded"
        @click="scopeExpanded = !scopeExpanded"
      >
        <div class="scope-header-left">
          <SlidersHorizontal :size="15" class="scope-header-icon" />
          <span class="scope-header-title">搜索范围与检索设置</span>
          <span class="scope-summary-pill">
            {{ scopePresetLabel }} · {{ selected.length }}/{{ enabled.length }} 源
          </span>
          <span class="scope-summary-pill muted-pill">
            <Clock :size="12" /> {{ timeoutSeconds === 0 ? '不限超时 · 完整等待' : `超时 ${timeoutSeconds}s` }}
          </span>
        </div>
        <div class="scope-header-right">
          <span>{{ scopeExpanded ? '收起' : '展开调整' }}</span>
          <ChevronUp v-if="scopeExpanded" :size="16" />
          <ChevronDown v-else :size="16" />
        </div>
      </button>

      <div v-show="scopeExpanded" class="scope-collapse-body">
        <div class="scope-setting-row">
          <span class="scope-row-label">书源范围</span>
          <div class="source-scope-presets">
            <button
              type="button"
              :class="['scope-tab-btn', { active: activePreset === 'all' }]"
              :disabled="searching || !enabled.length"
              @click="applyPreset('all')"
            >
              <Globe2 :size="13" class="scope-btn-icon" /> 全部可用 ({{ enabled.length }})
            </button>
            <button
              type="button"
              :class="['scope-tab-btn', { active: activePreset === 'top20' }]"
              :disabled="searching || !enabled.length"
              @click="applyPreset('top20')"
            >
              <Zap :size="13" class="scope-btn-icon" /> 前 20 源
            </button>
            <button
              type="button"
              :class="['scope-tab-btn', { active: activePreset === 'top50' }]"
              :disabled="searching || !enabled.length"
              @click="applyPreset('top50')"
            >
              <Radio :size="13" class="scope-btn-icon" /> 前 50 源
            </button>

            <select
              v-if="sourceGroups.length"
              class="scope-group-select"
              :value="activePreset === 'group' ? selectedGroup : ''"
              :disabled="searching"
              aria-label="按书源分组筛选"
              @change="onGroupSelectChange"
            >
              <option value="">按分组筛选…</option>
              <option v-for="g in sourceGroups" :key="g" :value="g">
                {{ g }} ({{ enabled.filter(s => s.group === g).length }})
              </option>
            </select>

            <button
              type="button"
              :class="['scope-tab-btn', 'picker-trigger-btn', { active: activePreset === 'custom' }]"
              :disabled="searching || !enabled.length"
              @click="openPicker"
            >
              <SlidersHorizontal :size="14" />
              自选书源
              <span v-if="activePreset === 'custom'" class="picker-count-tag">
                {{ selected.length }}
              </span>
            </button>
          </div>
        </div>

        <div class="scope-setting-row">
          <span class="scope-row-label">单源超时</span>
          <div class="source-scope-presets">
            <button
              v-for="opt in [
                { val: 0, label: '0秒 · 完整等待' },
                { val: 15, label: '15秒 · 推荐' },
                { val: 25, label: '25秒 · 慢源兼容' },
                { val: 35, label: '35秒 · 深度等待' }
              ]"
              :key="opt.val"
              type="button"
              :class="['scope-tab-btn', { active: timeoutSeconds === opt.val }]"
              :disabled="searching"
              @click="timeoutSeconds = opt.val"
            >
              {{ opt.label }}
            </button>
          </div>
        </div>
      </div>

      <p v-if="loading" role="status" class="scope-empty-tip muted">正在读取可用书源…</p>
      <p v-else-if="!enabled.length" class="scope-empty-tip muted">
        暂无可用书源。请先前往 <a href="#/sources" class="accent-link">我的书源</a> 导入并启用属于你的书源。
      </p>
    </div>
  </form>

  <div v-if="error" class="error" role="alert">
    {{ error }}
    <button v-if="!sources.length" class="text-button" @click="load">重新读取书源</button>
  </div>

  <!-- 简洁清爽的检索进度与动态面板 -->
  <section v-if="searching || (searched && results.length > 0) || stopped" class="search-hud" aria-live="polite">
    <div class="search-hud-header">
      <div class="search-hud-status">
        <span v-if="searching" class="search-pulse-dot"></span>
        <Check v-else-if="!stopped" :size="18" class="hud-icon-success" />
        <AlertCircle v-else :size="18" class="hud-icon-warn" />
        <span v-if="searching">
          正在检索《<strong>{{ searchedKeyword }}</strong>》
          <small class="muted">
            · 已找到 <strong>{{ results.length }}</strong> 本书籍 ({{ completedSources }}/{{ totalSources }} 源 · {{ searchSeconds }}s)
          </small>
        </span>
        <span v-else-if="stopped">
          检索已停止：共找到 <strong>{{ results.length }}</strong> 本与《{{ searchedKeyword }}》相关的书籍（耗时 {{ searchSeconds }}s）
        </span>
        <span v-else>
          检索完成：共找到 <strong>{{ results.length }}</strong> 本与《{{ searchedKeyword }}》相关的书籍（耗时 {{ searchSeconds }}s）
        </span>
      </div>
      <div class="search-hud-actions">
        <button
          v-if="searching"
          type="button"
          class="search-stop-btn"
          @click="stopSearch"
        >
          <Square :size="13" />
          就看这些
        </button>
        <button
          type="button"
          class="hud-expand-toggle-btn"
          @click="hudExpanded = !hudExpanded"
          :aria-expanded="hudExpanded"
        >
          <span>{{ hudExpanded ? '收起动态' : '书源动态' }}</span>
          <ChevronUp v-if="hudExpanded" :size="14" />
          <ChevronDown v-else :size="14" />
        </button>
      </div>
    </div>

    <div class="stream-progress-track">
      <div
        class="stream-progress-fill"
        :style="{ width: `${progressPercent}%` }"
      ></div>
    </div>

    <!-- 展开后的精简书源动态卡片 -->
    <div v-show="hudExpanded" class="search-hud-details">
      <div class="hud-stat-grid">
        <div class="hud-stat-card">
          <span class="hud-stat-label">检索进度</span>
          <strong class="hud-stat-val">{{ completedSources }} / {{ totalSources }}</strong>
        </div>
        <div class="hud-stat-card">
          <span class="hud-stat-label">命中书源</span>
          <strong class="hud-stat-val success">{{ hitSourcesCount }} 个</strong>
        </div>
        <div class="hud-stat-card">
          <span class="hud-stat-label">匹配书籍</span>
          <strong class="hud-stat-val highlight">{{ results.length }} 本</strong>
        </div>
      </div>

      <div class="stream-filter-bar">
        <span class="stream-filter-label">查看书源：</span>
        <button
          type="button"
          :class="['filter-pill', { active: sourceFilter === 'hasBooks' }]"
          @click="sourceFilter = 'hasBooks'"
        >
          有结果 ({{ hitSourcesCount }})
        </button>
        <button
          v-if="activeSearchingSources.length > 0"
          type="button"
          :class="['filter-pill', { active: sourceFilter === 'searching' }]"
          @click="sourceFilter = 'searching'"
        >
          检索中 ({{ activeSearchingSources.length }})
        </button>
        <button
          type="button"
          :class="['filter-pill', { active: sourceFilter === 'all' }]"
          @click="sourceFilter = 'all'"
        >
          全部已选 ({{ Object.keys(sourceStatuses).length }})
        </button>
      </div>

      <div v-if="filteredSourceStatuses.length" class="stream-source-flow">
        <span
          v-for="s in filteredSourceStatuses"
          :key="s.id"
          :class="[
            'stream-badge',
            s.status === 'searching' ? 'searching' :
            s.status === 'success' && s.count > 0 ? 'success' : 'empty'
          ]"
        >
          <LoaderCircle v-if="s.status === 'searching'" class="spin" :size="12" />
          <Check v-else-if="s.status === 'success' && s.count > 0" :size="12" />
          <span class="stream-badge-name">{{ s.name }}</span>
          <strong v-if="s.count > 0">+{{ s.count }}</strong>
        </span>
      </div>
      <p v-else class="muted" style="font-size: 12px;">暂无符合当前筛选条件的书源。</p>
    </div>
  </section>

  <!-- 搜索中首批等待骨架屏 -->
  <div v-if="searching && results.length === 0" class="search-skeleton-container">
    <div class="search-skeleton-tip">
      <Radio class="spin" :size="16" style="color: var(--accent);" />
      <span>正在检索《{{ searchedKeyword }}》，请稍候…</span>
    </div>
    <div class="search-skeleton-grid">
      <div v-for="n in 4" :key="n" class="skeleton-card">
        <div class="skeleton-cover skeleton-shimmer"></div>
        <div class="skeleton-content">
          <div class="skeleton-line title skeleton-shimmer"></div>
          <div class="skeleton-line author skeleton-shimmer"></div>
          <div class="skeleton-line desc skeleton-shimmer"></div>
          <div class="skeleton-line btn skeleton-shimmer"></div>
        </div>
      </div>
    </div>
  </div>

  <!-- 锚点 -->
  <div id="search-results-anchor"></div>

  <!-- 搜索结果列表（按匹配度精准排序 + 分页器） -->
  <template v-if="results.length > 0">
    <div class="section-heading search-result-head">
      <div>
        <h2>
          搜索结果 <span class="count">{{ results.length }}</span>
        </h2>
        <span class="muted">按相关度排序</span>
      </div>

      <div class="result-pagination-ctrls">
        <label class="page-size-label">
          <span>每页</span>
          <select v-model.number="resultPageSize" class="page-size-select" @change="resultPage = 1">
            <option :value="3">3 本</option>
            <option :value="5">5 本</option>
            <option :value="10">10 本</option>
            <option :value="20">20 本</option>
          </select>
        </label>
        <span class="page-current-info">第 {{ resultPage }} / {{ totalResultPages }} 页</span>
      </div>
    </div>

    <div class="result-grid">
      <article
        v-for="(result, index) in pagedResults"
        :key="key(result)"
        class="panel result-card"
      >
        <BookCover :title="result.title" :index="index + (resultPage - 1) * resultPageSize" />
        <div class="result-card-body">
          <div class="result-card-tags">
            <span v-if="isExactMatch(result)" class="exact-match-badge">
              <Sparkles :size="11" /> 精准匹配
            </span>
            <span class="tag">
              {{ sources.find(s => s.id === result.sourceId)?.name || '在线书源' }}
            </span>
          </div>
          <h2>{{ result.title }}</h2>
          <p class="muted result-author">{{ result.author || '佚名' }}</p>
          <p v-if="result.latestChapter" class="result-latest-chapter">
            <span class="latest-chapter-label">最新：</span>{{ result.latestChapter }}
          </p>
          <p class="result-intro">{{ result.intro || '暂无简介。' }}</p>
          <div class="result-card-action">
            <template v-if="added[key(result)]">
              <a
                class="button primary"
                :href="`#/read/${id(added[key(result)])}`"
              >
                <BookOpen :size="16" />
                立即阅读
              </a>
              <a
                class="button secondary"
                :href="`#/book/${id(added[key(result)])}`"
              >
                <Check :size="15" />
                已在书架 · 目录
              </a>
            </template>
            <template v-else>
              <button
                type="button"
                class="primary"
                :disabled="adding === key(result) || readingKey === key(result)"
                @click="readNow(result)"
              >
                <LoaderCircle v-if="readingKey === key(result)" class="spin" :size="16" />
                <BookOpen v-else :size="16" />
                立即阅读
              </button>
              <button
                type="button"
                class="secondary"
                :disabled="adding === key(result) || readingKey === key(result)"
                @click="add(result)"
              >
                <LoaderCircle v-if="adding === key(result)" class="spin" :size="16" />
                <Plus v-else :size="16" />
                加入书架
              </button>
            </template>
          </div>
        </div>
      </article>
    </div>

    <!-- 搜索结果分页导航栏 -->
    <div v-if="totalResultPages > 1" class="pagination-bar">
      <div class="pagination-info">
        <span>第 <strong>{{ resultPage }}</strong> / {{ totalResultPages }} 页（共 {{ results.length }} 本匹配书籍）</span>
      </div>
      <div class="pagination-controls">
        <button
          type="button"
          class="secondary pagination-btn"
          :disabled="resultPage <= 1"
          @click="changeResultPage(1)"
        >
          首页
        </button>
        <button
          type="button"
          class="secondary pagination-btn"
          :disabled="resultPage <= 1"
          @click="changeResultPage(resultPage - 1)"
        >
          <ChevronLeft :size="16" />
          上一页
        </button>
        <span class="pagination-current">{{ resultPage }}</span>
        <button
          type="button"
          class="secondary pagination-btn"
          :disabled="resultPage >= totalResultPages"
          @click="changeResultPage(resultPage + 1)"
        >
          下一页
          <ChevronRight :size="16" />
        </button>
        <button
          type="button"
          class="secondary pagination-btn"
          :disabled="resultPage >= totalResultPages"
          @click="changeResultPage(totalResultPages)"
        >
          末页
        </button>
      </div>
    </div>
  </template>

  <section v-else-if="searched && !searching && !error" class="empty">
    <Search :size="32" />
    <h2>未找到与《{{ searchedKeyword }}》匹配的书籍</h2>
    <p>可尝试检查书名错别字，或前往「书源管理」添加更多可用书源。</p>
  </section>

  <!-- 自选书源分页挑选器弹窗 (Modal) -->
  <div v-if="showPickerModal" class="modal-backdrop" @click.self="showPickerModal = false">
    <div class="modal picker-modal panel" role="dialog" aria-modal="true" aria-labelledby="picker-modal-title">
      <div class="modal-header">
        <div class="modal-title-wrap">
          <SlidersHorizontal :size="18" style="color: var(--accent);" />
          <h2 id="picker-modal-title">自定义选择搜索书源</h2>
          <span class="picker-selected-badge">
            已勾选 {{ selected.length }} / {{ enabled.length }} 个源
          </span>
        </div>
        <button type="button" class="icon-button" @click="showPickerModal = false" aria-label="关闭弹窗">
          <X :size="18" />
        </button>
      </div>

      <div class="picker-toolbar">
        <div class="picker-search-input">
          <Search :size="16" />
          <input
            v-model="pickerQuery"
            placeholder="按书源名称或网址快速搜索…"
            aria-label="搜索书源"
          />
        </div>

        <select v-model="pickerGroup" class="picker-group-select" aria-label="分组筛选">
          <option value="">全部分组</option>
          <option v-for="g in sourceGroups" :key="g" :value="g">
            {{ g }}
          </option>
        </select>
      </div>

      <div class="picker-batch-bar">
        <span class="picker-batch-info">
          匹配到 {{ filteredPickerSources.length }} 个可用书源
        </span>
        <div class="picker-batch-actions">
          <button type="button" class="text-button" @click="selectCurrentPickerPage">
            勾选本页
          </button>
          <button type="button" class="text-button" @click="deselectCurrentPickerPage">
            取消本页
          </button>
          <button type="button" class="text-button" @click="selectAllPickerSources">
            全选所有
          </button>
          <button type="button" class="text-button" @click="clearPickerSelection">
            清空已选
          </button>
        </div>
      </div>

      <div class="picker-source-grid">
        <div
          v-for="s in pagedPickerSources"
          :key="s.id"
          :class="['picker-source-item', { checked: selected.includes(s.id) }]"
          @click="togglePickerSource(s.id)"
        >
          <div class="picker-item-checkbox">
            <CheckSquare v-if="selected.includes(s.id)" :size="16" style="color: var(--accent);" />
            <div v-else class="picker-checkbox-empty"></div>
          </div>
          <div class="picker-item-info">
            <span class="picker-item-name">{{ s.name }}</span>
            <span class="picker-item-url">{{ s.url }}</span>
          </div>
          <span v-if="s.group" class="picker-item-group">{{ s.group }}</span>
        </div>
      </div>

      <div v-if="pickerTotalPages > 1" class="picker-pagination">
        <button
          type="button"
          class="secondary pagination-btn"
          :disabled="pickerPage <= 1"
          @click="pickerPage--"
        >
          <ChevronLeft :size="14" />
          上一页
        </button>
        <span class="picker-page-text">
          第 {{ pickerPage }} / {{ pickerTotalPages }} 页
        </span>
        <button
          type="button"
          class="secondary pagination-btn"
          :disabled="pickerPage >= pickerTotalPages"
          @click="pickerPage++"
        >
          下一页
          <ChevronRight :size="14" />
        </button>
      </div>

      <div class="modal-actions picker-modal-footer">
        <button type="button" class="button secondary" @click="showPickerModal = false">
          取消
        </button>
        <button type="button" class="button primary" @click="confirmPicker">
          确定并应用 (已选 {{ selected.length }} 个书源)
        </button>
      </div>
    </div>
  </div>
</template>
