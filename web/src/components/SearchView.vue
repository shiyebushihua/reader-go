<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch } from 'vue'
import {
  Search,
  ArrowRight,
  Plus,
  Check,
  LoaderCircle,
  Globe2,
  BookOpen,
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
  CheckSquare
} from 'lucide-vue-next'
import { api, id, message, streamSearch, type StreamSearchProgress, type StreamSearchStart, type ApiMeta } from '../api'
import type { Source, SearchResult, Book } from '../types'
import BookCover from './BookCover.vue'

interface SourceStatus {
  id: string
  name: string
  status: 'pending' | 'searching' | 'success' | 'error' | 'timeout'
  count: number
  error?: string
}

interface ActivityLog {
  id: number
  time: string
  text: string
  type: 'searching' | 'success' | 'warn' | 'info'
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
const added = ref<Record<string, string>>({})
const warnings = ref<NonNullable<ApiMeta['warnings']>>([])
const searchedKeyword = ref('')
const sourceFilter = ref<'all' | 'searching' | 'hasBooks' | 'failed'>('all')

// 搜索范围预设与自选
const activePreset = ref<'top20' | 'top50' | 'all' | 'group' | 'custom'>('top20')
const selectedGroup = ref('')
const showPickerModal = ref(false)

// 书源挑选器弹窗内部状态（带搜索、分组过滤与分页）
const pickerQuery = ref('')
const pickerGroup = ref('')
const pickerPage = ref(1)
const pickerPageSize = 5

// 搜索结果分页状态
const resultPage = ref(1)
const resultPageSize = ref(5)
const hudExpanded = ref(false)

const sourceStatuses = ref<Record<string, SourceStatus>>({})
const completedSources = ref(0)
const totalSources = ref(0)
const currentSearchingSource = ref('')
const activityLogs = ref<ActivityLog[]>([])
const searchSeconds = ref(0)

const hotKeywords = ['斗破苍穹', '凡人修仙传', '完美世界', '诡秘之主', '大奉打更人']

let abortCtrl: AbortController | null = null
let timerId: ReturnType<typeof setInterval> | null = null
let logCounter = 0

function nowTimeString(): string {
  const d = new Date()
  const pad = (n: number) => (n < 10 ? '0' + n : String(n))
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function pushLog(text: string, type: ActivityLog['type']) {
  logCounter++
  activityLogs.value.unshift({
    id: logCounter,
    time: nowTimeString(),
    text,
    type
  })
  if (activityLogs.value.length > 20) {
    activityLogs.value.pop()
  }
}

const enabled = computed(() => sources.value.filter(s => s.enabled))

const sourceGroups = computed(() => {
  const set = new Set<string>()
  for (const s of enabled.value) {
    if (s.group && s.group.trim()) set.add(s.group.trim())
  }
  return Array.from(set).sort()
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

const successSourcesCount = computed(() => {
  return Object.values(sourceStatuses.value).filter(s => s.status === 'success').length
})

const failedSourcesCount = computed(() => {
  return Object.values(sourceStatuses.value).filter(s => s.status === 'error' || s.status === 'timeout').length
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
    return list.filter(s => s.status === 'success' && s.count > 0)
  }
  if (sourceFilter.value === 'failed') {
    return list.filter(s => s.status === 'error' || s.status === 'timeout')
  }
  return list
})

const key = (r: SearchResult) => `${r.sourceId}:${r.url}`

async function load() {
  loading.value = true
  error.value = ''
  try {
    sources.value = await api<Source[]>('/sources') || []
    applyPreset('top20')
  } catch (e) {
    error.value = message(e)
  } finally {
    loading.value = false
  }
}

function applyPreset(preset: 'top20' | 'top50' | 'all' | 'group' | 'custom') {
  activePreset.value = preset
  const all = enabled.value
  if (preset === 'top20') {
    const liudaIndex = all.findIndex(s => s.name.includes('溜达'))
    if (liudaIndex > 0) {
      const liuda = all[liudaIndex]
      const rest = all.filter((_, i) => i !== liudaIndex)
      selected.value = [liuda.id, ...rest.slice(0, 19).map(s => s.id)]
    } else {
      selected.value = all.slice(0, 20).map(s => s.id)
    }
  } else if (preset === 'top50') {
    selected.value = all.slice(0, 50).map(s => s.id)
  } else if (preset === 'all') {
    selected.value = all.map(s => s.id)
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
  pushLog(`搜索已由用户主动停止。当前已保留 ${results.value.length} 本书籍。`, 'info')
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

function useHotKeyword(kw: string) {
  keyword.value = kw
  search()
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

  // 限制单次检索最多 100 个书源以保证高吞吐性能
  const targetIds = selected.value.slice(0, 100)

  searching.value = true
  stopped.value = false
  error.value = ''
  warnings.value = []
  searched.value = false
  results.value = []
  resultPage.value = 1
  activityLogs.value = []
  searchedKeyword.value = kw
  completedSources.value = 0
  totalSources.value = targetIds.length
  searchSeconds.value = 0

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

  pushLog(`启动并发流式搜索《${kw}》，已连接 ${targetIds.length} 个书源…`, 'info')

  if (timerId) clearInterval(timerId)
  const startTime = Date.now()
  timerId = setInterval(() => {
    searchSeconds.value = Math.round(((Date.now() - startTime) / 1000) * 10) / 10
  }, 100)

  abortCtrl = new AbortController()

  try {
    await streamSearch(
      { keyword: kw, sourceIds: targetIds },
      {
        onStart: (data: StreamSearchStart) => {
          totalSources.value = data.total
          pushLog(`引擎就绪：开启 16 线程并发通道全速检索…`, 'searching')
        },
        onProgress: (p: StreamSearchProgress) => {
          completedSources.value = p.completed
          currentSearchingSource.value = p.sourceName
          const target = sourceStatuses.value[p.sourceId]
          if (target) {
            target.status = p.status
            if (p.count) target.count = p.count
            if (p.error) target.error = p.error
          }

          if (p.status === 'searching') {
            pushLog(`正在检索书源 [${p.sourceName}]…`, 'searching')
          } else if (p.status === 'success') {
            if (p.count && p.count > 0) {
              pushLog(`✨ [${p.sourceName}] 成功命中 ${p.count} 本相关图书！`, 'success')
            } else {
              pushLog(`[${p.sourceName}] 检索完毕，未收录此书`, 'info')
            }
          } else if (p.status === 'timeout' || p.status === 'error') {
            const err = p.error || (p.status === 'timeout' ? '超时' : '请求失败')
            pushLog(`⚠️ [${p.sourceName}] ${err}，已跳过`, 'warn')
            warnings.value.push({ sourceId: p.sourceId, message: err })
          }

          if (p.results && p.results.length > 0) {
            const existingKeys = new Set(results.value.map(r => key(r)))
            for (const item of p.results) {
              const k = key(item)
              if (!existingKeys.has(k)) {
                results.value.push(item)
                existingKeys.add(k)
              }
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
          pushLog(`搜索完成！共检索 ${completedSources.value} 个书源，累计发现 ${results.value.length} 本图书。`, 'success')
        }
      },
      abortCtrl.signal
    )
  } catch (e: any) {
    if (abortCtrl?.signal.aborted) {
      return
    }
    // 降级使用基础同步检索
    pushLog(`流式通道中断，尝试降级兼容检索…`, 'warn')
    try {
      results.value = await api<SearchResult[]>('/search', 'POST', {
        keyword: kw,
        sourceIds: targetIds
      }, meta => warnings.value = meta.warnings || []) || []
    } catch (err) {
      error.value = message(err)
      pushLog(`检索异常：${message(err)}`, 'warn')
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
  <header class="page-header">
    <div>
      <span class="eyebrow">FIND YOUR NEXT CHAPTER</span>
      <h1>发现好书</h1>
      <p>在你信任的书源里，寻找下一段值得沉浸的故事。</p>
    </div>
    <Globe2 class="header-symbol" :size="40" :stroke-width="1" />
  </header>

  <form class="panel search-panel" @submit.prevent="search">
    <div class="large-search">
      <Search :size="23" />
      <input
        v-model="keyword"
        required
        aria-label="搜索书名或作者"
        placeholder="输入书名或作者，开始寻找…"
      />
      <button
        v-if="searching"
        type="button"
        class="search-stop-btn"
        @click="stopSearch"
      >
        <Square :size="16" />
        停止搜索 (Esc)
      </button>
      <button
        v-else
        type="submit"
        class="primary"
        :disabled="loading || !selected.length || !keyword.trim()"
      >
        搜索
        <ArrowRight :size="18" />
      </button>
    </div>

    <!-- 热门搜索推荐 -->
    <div class="hot-keywords">
      <span class="hot-label">大家都在搜：</span>
      <button
        v-for="kw in hotKeywords"
        :key="kw"
        type="button"
        class="hot-chip"
        :disabled="searching"
        @click="useHotKeyword(kw)"
      >
        {{ kw }}
      </button>
    </div>

    <!-- 现代书源选择范围：预设标签栏 + 自选弹窗，彻底告别丑陋平铺 -->
    <div class="source-scope-section">
      <div class="source-scope-header">
        <span class="source-scope-title">
          搜索范围
          <small class="source-scope-count">
            已就绪 {{ selected.length }} / {{ enabled.length }} 个书源
          </small>
        </span>

        <div class="source-scope-presets">
          <button
            type="button"
            :class="['scope-tab-btn', { active: activePreset === 'top20' }]"
            :disabled="searching || !enabled.length"
            @click="applyPreset('top20')"
          >
            ⚡ 精选20源
          </button>
          <button
            type="button"
            :class="['scope-tab-btn', { active: activePreset === 'top50' }]"
            :disabled="searching || !enabled.length"
            @click="applyPreset('top50')"
          >
            🌐 主流50源
          </button>
          <button
            type="button"
            :class="['scope-tab-btn', { active: activePreset === 'all' }]"
            :disabled="searching || !enabled.length"
            @click="applyPreset('all')"
          >
            🚀 全部可用 ({{ enabled.length }})
          </button>

          <!-- 分组快速过滤下拉 -->
          <select
            v-if="sourceGroups.length"
            class="scope-group-select"
            :value="activePreset === 'group' ? selectedGroup : ''"
            :disabled="searching"
            @change="onGroupSelectChange"
          >
            <option value="">📂 按分组筛选…</option>
            <option v-for="g in sourceGroups" :key="g" :value="g">
              {{ g }} ({{ enabled.filter(s => s.group === g).length }})
            </option>
          </select>

          <!-- 自选书源（分页弹窗） -->
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

      <p v-if="loading" role="status" class="muted">正在读取可用书源…</p>
      <p v-else-if="!enabled.length" class="muted">
        暂无可用书源。请在管理面板导入并启用书源。
      </p>
    </div>
  </form>

  <div v-if="error" class="error" role="alert">
    {{ error }}
    <button v-if="!sources.length" class="text-button" @click="load">重新读取书源</button>
  </div>

  <!-- 实时流式交互监控 HUD 面板 -->
  <section v-if="searching || (searched && results.length > 0) || stopped" class="search-hud" aria-live="polite">
    <div class="search-hud-header">
      <div class="search-hud-status">
        <span v-if="searching" class="search-pulse-dot"></span>
        <Check v-else-if="!stopped" :size="18" style="color: #2a9d8f;" />
        <AlertCircle v-else :size="18" style="color: #e76f51;" />
        <span v-if="searching">
          正在检索：<strong>{{ currentSearchingSource || '并发连线中…' }}</strong>
          <small class="muted" style="margin-left: 6px;">
            (16并发 / 耗时 {{ searchSeconds }}s)
          </small>
        </span>
        <span v-else-if="stopped">
          已停止检索：耗时 {{ searchSeconds }}s，在 {{ completedSources }} 个书源中保留 <strong>{{ results.length }}</strong> 本书籍
        </span>
        <span v-else>
          检索完成：耗时 {{ searchSeconds }}s，在 {{ completedSources }} 个书源中找到 <strong>{{ results.length }}</strong> 本书籍
        </span>
      </div>
      <div class="search-hud-actions">
        <button
          v-if="searching"
          type="button"
          class="search-stop-btn"
          @click="stopSearch"
        >
          <Square :size="14" />
          停止检索
        </button>
        <button
          type="button"
          class="hud-expand-toggle-btn"
          @click="hudExpanded = !hudExpanded"
          :aria-expanded="hudExpanded"
        >
          <span>{{ hudExpanded ? '收起书源动态' : '展开书源动态' }}</span>
          <ChevronUp v-if="hudExpanded" :size="15" />
          <ChevronDown v-else :size="15" />
        </button>
      </div>
    </div>

    <div v-show="hudExpanded" class="search-hud-details">

    <!-- 动态进度条 -->
    <div class="stream-progress-track">
      <div
        class="stream-progress-fill"
        :style="{ width: `${progressPercent}%` }"
      ></div>
    </div>

    <!-- 聚合指标面板 -->
    <div class="search-hud-metrics">
      <div class="metric-item">
        <span class="metric-num">{{ completedSources }} / {{ totalSources }}</span>
        <span class="metric-label">已扫描书源</span>
      </div>
      <div class="metric-item">
        <span class="metric-num success-text">{{ successSourcesCount }}</span>
        <span class="metric-label">响应成功</span>
      </div>
      <div class="metric-item">
        <span class="metric-num warn-text">{{ failedSourcesCount }}</span>
        <span class="metric-label">跳过 / 超时</span>
      </div>
      <div class="metric-item">
        <span class="metric-num highlight-text">{{ results.length }}</span>
        <span class="metric-label">发现书籍</span>
      </div>
    </div>

    <!-- 实时滚动活动流水日志 -->
    <div v-if="activityLogs.length" class="activity-ticker">
      <div class="ticker-header">
        <span class="ticker-title">
          <Zap :size="13" /> 检索动态流水 (最近 {{ activityLogs.length }} 条)
        </span>
        <span v-if="searching" class="ticker-badge">实时更新中</span>
      </div>
      <div class="ticker-window">
        <div
          v-for="log in activityLogs"
          :key="log.id"
          :class="['ticker-line', log.type]"
        >
          <time class="ticker-time">{{ log.time }}</time>
          <span class="ticker-text">{{ log.text }}</span>
        </div>
      </div>
    </div>

    <!-- 书源状态过滤标签 -->
    <div class="stream-filter-bar">
      <span class="stream-filter-label">状态过滤：</span>
      <button
        type="button"
        :class="['filter-pill', { active: sourceFilter === 'all' }]"
        @click="sourceFilter = 'all'"
      >
        全部 ({{ Object.keys(sourceStatuses).length }})
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
        :class="['filter-pill', { active: sourceFilter === 'hasBooks' }]"
        @click="sourceFilter = 'hasBooks'"
      >
        有结果 ({{ Object.values(sourceStatuses).filter(s => s.count > 0).length }})
      </button>
      <button
        type="button"
        :class="['filter-pill', { active: sourceFilter === 'failed' }]"
        @click="sourceFilter = 'failed'"
      >
        异常 / 超时 ({{ failedSourcesCount }})
      </button>
    </div>

    <!-- 选定书源实时状态流徽章 -->
    <div v-if="filteredSourceStatuses.length" class="stream-source-flow">
      <span
        v-for="s in filteredSourceStatuses"
        :key="s.id"
        :class="[
          'stream-badge',
          s.status === 'searching' ? 'searching' :
          s.status === 'success' ? (s.count > 0 ? 'success' : 'empty') :
          s.status === 'error' || s.status === 'timeout' ? 'error' : 'pending'
        ]"
      >
        <LoaderCircle v-if="s.status === 'searching'" class="spin" :size="12" />
        <Check v-else-if="s.status === 'success' && s.count > 0" :size="12" />
        {{ s.name }}
        <strong v-if="s.count > 0">+{{ s.count }}</strong>
        <small v-else-if="s.status === 'error'">跳过</small>
        <small v-else-if="s.status === 'timeout'">超时</small>
      </span>
      </div>
    </div>
  </section>

  <!-- 搜索中首批等待骨架屏 (Skeleton Shimmer) -->
  <div v-if="searching && results.length === 0" class="search-skeleton-container">
    <div class="search-skeleton-tip">
      <Radio class="spin" :size="16" style="color: var(--accent);" />
      <span>正在 16 线程并发检索各书源，抓取《{{ searchedKeyword }}》中，首批书籍即将抵达…</span>
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

  <!-- 边搜边看流式提示横幅 (Live Streaming Banner) -->
  <div v-if="searching && results.length > 0" class="stream-live-banner">
    <div class="stream-live-banner-content">
      <Zap :size="18" style="color: var(--accent);" />
      <span>
        已实时捕获 <strong>{{ results.length }}</strong> 本图书！其余书源仍在继续检索中，您可以直接加入书架或点击阅读，无需等待全部完成。
      </span>
    </div>
    <button type="button" class="search-stop-btn" @click="stopSearch">
      <Square :size="14" />
      就看这些 (停止检索)
    </button>
  </div>

  <!-- 警告提示 -->
  <div v-if="warnings.length && !searching" class="warning" role="alert">
    <strong>部分书源未能完成搜索，已为您展示其它可用结果：</strong>
    <ul>
      <li v-for="(warning, i) in warnings" :key="i">
        {{ sources.find(s => s.id === warning.sourceId)?.name || warning.sourceId || '书源' }}：{{ warning.message }}
      </li>
    </ul>
  </div>

  <!-- 锚点 -->
  <div id="search-results-anchor"></div>

  <!-- 搜索结果列表（带分页器） -->
  <template v-if="results.length > 0">
    <div class="section-heading search-result-head">
      <div>
        <h2>
          搜索结果 <span class="count">{{ results.length }}</span>
        </h2>
        <span class="muted">“{{ searchedKeyword }}”</span>
      </div>

      <!-- 每页显示数量选择 -->
      <div class="result-pagination-ctrls">
        <label class="page-size-label">
          每页显示
          <select v-model.number="resultPageSize" @change="resultPage = 1">
            <option :value="3">3 本</option>
            <option :value="5">5 本</option>
            <option :value="10">10 本</option>
            <option :value="20">20 本</option>
          </select>
        </label>
        <span class="page-current-info">第 {{ resultPage }} / {{ totalResultPages }} 页</span>
      </div>
    </div>

    <!-- 当前页结果网格 -->
    <div class="result-grid">
      <article
        v-for="(result, index) in pagedResults"
        :key="key(result)"
        class="panel result-card"
      >
        <BookCover :title="result.title" :index="index + (resultPage - 1) * resultPageSize" />
        <div>
          <span class="tag">
            {{ sources.find(s => s.id === result.sourceId)?.name || '在线书源' }}
          </span>
          <span v-if="searching" class="live-badge">
            <Sparkles :size="10" /> 实时捕获
          </span>
          <h2>{{ result.title }}</h2>
          <p class="muted">{{ result.author || '佚名' }}</p>
          <p class="result-intro">{{ result.intro || '书源暂未提供简介。' }}</p>
          <a
            v-if="added[key(result)]"
            class="button secondary"
            :href="`#/book/${id(added[key(result)])}`"
          >
            <Check :size="16" />
            已在书架 · 立即阅读
          </a>
          <button
            v-else
            class="primary"
            :disabled="adding === key(result)"
            @click="add(result)"
          >
            <LoaderCircle v-if="adding === key(result)" class="spin" />
            <Plus v-else :size="16" />
            加入书架
          </button>
        </div>
      </article>
    </div>

    <!-- 搜索结果分页导航栏 -->
    <div v-if="totalResultPages > 1" class="pagination-bar">
      <button
        type="button"
        class="page-nav-btn"
        :disabled="resultPage <= 1"
        @click="changeResultPage(1)"
      >
        首页
      </button>
      <button
        type="button"
        class="page-nav-btn"
        :disabled="resultPage <= 1"
        @click="changeResultPage(resultPage - 1)"
      >
        <ChevronLeft :size="16" />
        上一页
      </button>

      <span class="page-indicator">
        第 <strong>{{ resultPage }}</strong> / {{ totalResultPages }} 页
      </span>

      <button
        type="button"
        class="page-nav-btn"
        :disabled="resultPage >= totalResultPages"
        @click="changeResultPage(resultPage + 1)"
      >
        下一页
        <ChevronRight :size="16" />
      </button>
      <button
        type="button"
        class="page-nav-btn"
        :disabled="resultPage >= totalResultPages"
        @click="changeResultPage(totalResultPages)"
      >
        末页
      </button>
    </div>
  </template>

  <section v-else-if="searched && !searching && !error" class="empty">
    <Search :size="32" />
    <h2>未找到匹配的书籍</h2>
    <p>可以尝试更换书名关键词，或在上方切换至“全部可用”扩大搜索范围。</p>
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

      <!-- 弹窗内过滤工具栏 -->
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

      <!-- 批量勾选工具条 -->
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

      <!-- 分页书源列表卡片 -->
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

      <!-- 弹窗分页控制 -->
      <div v-if="pickerTotalPages > 1" class="picker-pagination">
        <button
          type="button"
          class="page-nav-btn small"
          :disabled="pickerPage <= 1"
          @click="pickerPage--"
        >
          <ChevronLeft :size="14" />
          上一页
        </button>
        <span class="picker-page-text">
          {{ pickerPage }} / {{ pickerTotalPages }}
        </span>
        <button
          type="button"
          class="page-nav-btn small"
          :disabled="pickerPage >= pickerTotalPages"
          @click="pickerPage++"
        >
          下一页
          <ChevronRight :size="14" />
        </button>
      </div>

      <!-- 弹窗底部操作 -->
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
