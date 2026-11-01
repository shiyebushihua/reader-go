<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  ChevronUp,
  List,
  Settings2,
  BookmarkPlus,
  Bookmark as BookmarkIcon,
  Save,
  X,
  Trash2,
  LoaderCircle,
  Maximize2,
  Minimize2,
  AlignJustify,
  AlignLeft,
  Check,
  Type,
  Palette,
  Sliders,
  Sparkles,
  Moon,
  Sun,
  Shuffle,
  Columns,
  Scroll,
  Smartphone,
  Search,
  RefreshCw,
  ArrowUpDown,
  LocateFixed,
  Download,
  Headphones,
  Play,
  Pause,
  Square,
  Plus,
  Languages
} from 'lucide-vue-next'
import { api, ApiError, id, message, getExportUrl } from '../api'
import { toTraditional, toSimplified } from '../chinese'
import type { Book, Chapter, ChapterContent, Progress, Bookmark, Settings, SearchResult, Source, BookSourceCandidate, PurifyRule, BookCacheStatus } from '../types'
import { settingLimits, settingsError } from '../validation'
import {
  saveBook,
  getBook,
  saveChapters,
  getChapters,
  saveChapterContent,
  getChapterContent,
  getCachedChapterIds
} from '../offline'
import { showConfirm } from '../confirm'

interface ExtendedReaderPrefs {
  theme: string
  fontFamily: string
  fontSize: number
  lineHeight: number
  paraSpacing: number
  width: number
  textAlign: 'justify' | 'left'
  transitionSpeed: 'off' | 'fast' | 'normal' | 'smooth'
  pageMode: 'scroll' | 'slide'
  adaptiveWidth: boolean
  indent?: boolean
  letterSpacing?: number
  wakeLock?: boolean
  chineseMode?: 'original' | 'simplified' | 'traditional'
}

const props = defineProps<{ bookId: string; initialChapter?: string }>()
const path = `/books/${id(props.bookId)}`

// Core data states
const book = ref<Book | null>(null)
const chapters = ref<Chapter[]>([]), content = ref<ChapterContent | null>(null), bookmarks = ref<Bookmark[]>([])
const loading = ref(true), chapterLoading = ref(false), error = ref(''), notice = ref('')
const panel = ref<'toc' | 'settings' | 'bookmarks' | ''>('')
const position = ref(0), version = ref(0), dirty = ref(false), saving = ref(false), conflict = ref(false)
const savedLabel = ref('进度一致'), note = ref(''), bookmarkBusy = ref(false), settingsBusy = ref(false), showAdvancedPrefs = ref(false)
const tocQuery = ref(''), scroller = ref<HTMLElement | null>(null), isFullscreen = ref(false)
const tocReversed = ref(false)
const lastTargetChapterId = ref('')
const showControls = ref(typeof window !== 'undefined' ? window.innerWidth > 768 : true)
const isFromCache = ref(false)

// Slide & Page Mode States
const slidePage = ref(0)
const totalSlidePages = ref(1)
let touchStartX = 0
let touchStartY = 0
let touchStartTime = 0

// Change Source States
const changeSourceOpen = ref(false)
const changeSourceQuery = ref('')
const changeSourceLoading = ref(false)
const changeSourceSubmitting = ref(false)
const changeSourceResults = ref<SearchResult[]>([])
const changeSourceError = ref('')
const sourcesMap = ref<Record<string, string>>({})
const enabledSourceIds = ref<string[]>([])
const lastDayTheme = ref('sepia')

// Legado Feature: Real-time Clock, Battery & Screen WakeLock
const cachedChapterIds = ref<Set<string>>(new Set())
const currentTimeStr = ref('')
const batteryLevel = ref('')
let clockTimer: any = null
let wakeLockSentinel: any = null

function updateStatusInfo() {
  const d = new Date()
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  currentTimeStr.value = `${hh}:${mm}`

  if (typeof navigator !== 'undefined' && (navigator as any).getBattery) {
    (navigator as any).getBattery().then((battery: any) => {
      batteryLevel.value = `${Math.round(battery.level * 100)}%`
    }).catch(() => {})
  }
}

async function requestWakeLock() {
  if (typeof navigator !== 'undefined' && 'wakeLock' in navigator && (navigator as any).wakeLock) {
    try {
      wakeLockSentinel = await (navigator as any).wakeLock.request('screen')
    } catch {}
  }
}

function releaseWakeLock() {
  if (wakeLockSentinel) {
    try { wakeLockSentinel.release() } catch {}
    wakeLockSentinel = null
  }
}

async function refreshCachedChapterIds() {
  try {
    const ids = await getCachedChapterIds(props.bookId)
    cachedChapterIds.value = new Set(ids)
  } catch {}
}

// Configurable Reading Parameters
const prefs = reactive<ExtendedReaderPrefs>({
  theme: 'sepia',
  fontFamily: 'wenkai',
  fontSize: 19,
  lineHeight: 1.95,
  paraSpacing: 1.25,
  width: 780,
  textAlign: 'justify',
  transitionSpeed: 'normal',
  pageMode: 'scroll',
  adaptiveWidth: false,
  indent: true,
  letterSpacing: 0.5,
  wakeLock: true,
  chineseMode: 'original'
})

// Themes metadata (7 major-reader inspired eye-care themes)
const themes = [
  { id: 'sepia', name: '暖阳羊皮', bg: '#f4edd9', fg: '#332b21', border: '#e2d3b5', desc: '经典纸质，柔和护眼' },
  { id: 'meadow', name: '竹叶淡绿', bg: '#e7efe5', fg: '#1f3022', border: '#ccdccc', desc: '微信读书护眼经典' },
  { id: 'cream', name: '秋水米白', bg: '#f7f4ee', fg: '#2b2723', border: '#e6dfd1', desc: '柔和细腻，宛若纸书' },
  { id: 'light', name: '现代素白', bg: '#f8f9fa', fg: '#1f2429', border: '#e1e4e8', desc: '清爽明净，现代极简' },
  { id: 'twilight', name: '暮霭灰蓝', bg: '#222933', fg: '#d8dfe8', border: '#323c4b', desc: '暗光环境，沉静低调' },
  { id: 'midnight', name: '深邃夜读', bg: '#121316', fg: '#cfd4dc', border: '#252932', desc: 'OLED纯粹黑，省电不刺眼' },
  { id: 'vintage', name: '复古咖啡', bg: '#ebdcc3', fg: '#3b2c1d', border: '#d9c4a5', desc: '旧书年轮，静谧典雅' }
]

// Fonts metadata (Popular reader fonts)
const fonts = [
  { id: 'wenkai', name: '霞鹜文楷', family: 'var(--font-wenkai)', sample: '落霞与孤鹜齐飞', desc: '温润人文 · 舒适护眼' },
  { id: 'serif', name: '思源宋体', family: 'var(--font-serif)', sample: '秋水共长天一色', desc: '经典典雅 · 书卷气息' },
  { id: 'sans', name: '思源黑体', family: 'var(--font-sans)', sample: '天地有大美而不言', desc: '现代清晰 · 简洁利落' },
  { id: 'kaiti', name: '书法楷体', family: 'var(--font-kaiti)', sample: '字里千秋意蕴长', desc: '笔墨生动 · 传统韵致' },
  { id: 'fangsong', name: '典雅仿宋', family: 'var(--font-fangsong)', sample: '文质彬彬然后君子', desc: '古朴清秀 · 文学排版' }
]

// Animation speed metadata
const speedDurations: Record<string, string> = {
  off: '0.01ms',
  fast: '140ms',
  normal: '240ms',
  smooth: '380ms'
}

const isNightMode = computed(() => prefs.theme === 'midnight' || prefs.theme === 'twilight')

const currentFont = computed(() => {
  const f = fonts.find(item => item.id === prefs.fontFamily)
  return f ? f.family : 'var(--font-wenkai)'
})

const index = computed(() => chapters.value.findIndex(c => c.id === content.value?.id))
function convertText(text: string): string {
  if (!text) return ''
  if (prefs.chineseMode === 'traditional') return toTraditional(text)
  if (prefs.chineseMode === 'simplified') return toSimplified(text)
  return text
}
const currentChapterName = computed(() => {
  const raw = content.value?.title || (index.value >= 0 ? chapters.value[index.value]?.title : '')
  return convertText(raw)
})
const toc = computed(() => {
  const list = chapters.value.filter(c => c.title.toLowerCase().includes(tocQuery.value.toLowerCase()))
  return tocReversed.value ? [...list].reverse() : list
})

// Legado Feature: Text Purification Rules
const defaultPurifyRules: PurifyRule[] = [
  { id: 'p1', name: '净化网站广告水印', pattern: '(?:请收藏本站|笔趣阁|最新网址|wap\\.|www\\.).*?(?:\\r?\\n|$)', replacement: '', enabled: true, isRegex: true },
  { id: 'p2', name: '净化章节末尾催更求票', pattern: '（(?:本章未完|继续阅读|求推荐票|求月票|求打赏|加更).*?）', replacement: '', enabled: true, isRegex: true }
]
function loadPurifyRules(): PurifyRule[] {
  if (typeof localStorage === 'undefined') return defaultPurifyRules
  try {
    const raw = localStorage.getItem('reader_purify_rules_v1')
    if (raw) return JSON.parse(raw)
  } catch {}
  return defaultPurifyRules
}
const purifyRules = ref<PurifyRule[]>(loadPurifyRules())
const settingsActiveTab = ref<'prefs' | 'purify'>('prefs')
const newRuleName = ref('')
const newRulePattern = ref('')
const newRuleReplacement = ref('')
const newRuleIsRegex = ref(true)

watch(purifyRules, val => {
  if (typeof localStorage !== 'undefined') {
    try { localStorage.setItem('reader_purify_rules_v1', JSON.stringify(val)) } catch {}
  }
}, { deep: true })

function addPurifyRule() {
  if (!newRulePattern.value.trim()) return
  purifyRules.value.push({
    id: 'pr_' + Date.now(),
    name: newRuleName.value.trim() || '自定义净化规则 ' + (purifyRules.value.length + 1),
    pattern: newRulePattern.value.trim(),
    replacement: newRuleReplacement.value,
    enabled: true,
    isRegex: newRuleIsRegex.value
  })
  newRuleName.value = ''
  newRulePattern.value = ''
  newRuleReplacement.value = ''
}
function removePurifyRule(id: string) {
  purifyRules.value = purifyRules.value.filter(r => r.id !== id)
}
function applyPurify(text: string): string {
  let res = text
  for (const rule of purifyRules.value) {
    if (!rule.enabled || !rule.pattern) continue
    try {
      if (rule.isRegex) {
        const re = new RegExp(rule.pattern, 'g')
        res = res.replace(re, rule.replacement || '')
      } else {
        res = res.replaceAll(rule.pattern, rule.replacement || '')
      }
    } catch {}
  }
  return res.trim()
}

const paragraphs = computed(() => {
  if (!content.value?.content) return []
  return content.value.content
    .split(/\r?\n+/)
    .map(p => convertText(applyPurify(p.trim())))
    .filter(p => p.length > 0)
})

// Legado Feature: Pure Frontend Web Speech API Audio Reader (TTS)
const ttsActive = ref(false)
const ttsPlaying = ref(false)
const ttsIndex = ref(0)
const ttsRate = ref(1.0)
let speechUtterance: SpeechSynthesisUtterance | null = null

function initTts() {
  if (typeof window === 'undefined' || !('speechSynthesis' in window)) {
    notice.value = '当前浏览器不支持语音朗读 (Web Speech API)'
    setTimeout(() => { notice.value = '' }, 3000)
    return false
  }
  return true
}
function speakCurrentParagraph() {
  if (typeof window === 'undefined' || !('speechSynthesis' in window)) return
  window.speechSynthesis.cancel()
  if (!ttsActive.value || !ttsPlaying.value) return
  if (ttsIndex.value >= paragraphs.value.length) {
    if (index.value < chapters.value.length - 1) {
      notice.value = '本章朗读完毕，正在载入下一章…'
      setTimeout(() => { notice.value = '' }, 2500)
      openChapter(chapters.value[index.value + 1].id, 0, false).then(() => {
        ttsIndex.value = 0
        speakCurrentParagraph()
      })
    } else {
      notice.value = '全书已朗读完毕'
      stopTts()
    }
    return
  }
  const text = paragraphs.value[ttsIndex.value]
  if (!text || !text.trim()) {
    ttsIndex.value++
    speakCurrentParagraph()
    return
  }
  speechUtterance = new SpeechSynthesisUtterance(text)
  speechUtterance.rate = ttsRate.value
  speechUtterance.lang = 'zh-CN'
  speechUtterance.onend = () => {
    if (ttsActive.value && ttsPlaying.value) {
      ttsIndex.value++
      speakCurrentParagraph()
    }
  }
  speechUtterance.onerror = (e) => {
    if (e.error !== 'canceled' && e.error !== 'interrupted') {
      ttsPlaying.value = false
    }
  }
  window.speechSynthesis.speak(speechUtterance)
}
function startTts(fromIndex = 0) {
  if (!initTts()) return
  if (!paragraphs.value.length) return
  ttsActive.value = true
  ttsPlaying.value = true
  ttsIndex.value = Math.max(0, Math.min(fromIndex, paragraphs.value.length - 1))
  speakCurrentParagraph()
}
function pauseTts() {
  if (typeof window !== 'undefined' && 'speechSynthesis' in window) {
    window.speechSynthesis.cancel()
  }
  ttsPlaying.value = false
}
function resumeTts() {
  ttsPlaying.value = true
  speakCurrentParagraph()
}
function stopTts() {
  if (typeof window !== 'undefined' && 'speechSynthesis' in window) {
    window.speechSynthesis.cancel()
  }
  ttsActive.value = false
  ttsPlaying.value = false
  ttsIndex.value = 0
}
function nextTtsParagraph() {
  if (ttsIndex.value < paragraphs.value.length - 1) {
    ttsIndex.value++
    if (ttsPlaying.value) speakCurrentParagraph()
  }
}
function prevTtsParagraph() {
  if (ttsIndex.value > 0) {
    ttsIndex.value--
    if (ttsPlaying.value) speakCurrentParagraph()
  }
}
function setTtsRate(rate: number) {
  ttsRate.value = rate
  if (ttsPlaying.value) speakCurrentParagraph()
}

// Legado Feature: Batch Offline Chapter Caching
const cacheModalOpen = ref(false)
const cachingBusy = ref(false)
const cacheProgress = ref<{ total: number; cached: number }>({ total: 0, cached: 0 })

async function openCacheModal() {
  cacheModalOpen.value = true
  try {
    const status = await api<BookCacheStatus>(`${path}/cache/status`)
    cacheProgress.value = {
      total: status.totalChapters || chapters.value.length,
      cached: status.cachedChapters || 0
    }
  } catch {}
}
async function triggerBatchCache(count: number) {
  if (cachingBusy.value) return
  cachingBusy.value = true
  try {
    const curIndex = index.value >= 0 ? index.value : 0
    const res = await api<{ ok: boolean; cached: number; skipped: number; total: number }>(`${path}/cache`, 'POST', {
      fromOrdinal: curIndex,
      count: count === 0 ? chapters.value.length : count
    })
    notice.value = `已成功提交缓存任务，本次缓存 ${res.cached} 章（跳过已存在 ${res.skipped} 章）`
    setTimeout(() => { notice.value = '' }, 4000)
    const status = await api<BookCacheStatus>(`${path}/cache/status`)
    cacheProgress.value = {
      total: status.totalChapters || chapters.value.length,
      cached: status.cachedChapters || 0
    }
  } catch (e) {
    notice.value = `缓存失败：${message(e)}`
    setTimeout(() => { notice.value = '' }, 4000)
  } finally {
    cachingBusy.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
let restoring = false
let active = true
let savePromise: Promise<boolean> | null = null

// Map frontend rich theme to backend-allowed theme ('light'|'dark'|'sepia')
function mapThemeToBackend(themeId: string): 'light' | 'dark' | 'sepia' {
  if (themeId === 'midnight' || themeId === 'twilight') return 'dark'
  if (themeId === 'light') return 'light'
  return 'sepia'
}

// Map backend theme to frontend default theme
function mapBackendToTheme(backendTheme: string): string {
  if (backendTheme === 'dark') return 'midnight'
  if (backendTheme === 'light') return 'light'
  return 'sepia'
}

function applyMotionSpeed(speed: ExtendedReaderPrefs['transitionSpeed']) {
  const duration = speedDurations[speed] || '240ms'
  document.documentElement.style.setProperty('--reader-duration', duration)
}

function loadLocalPrefs() {
  try {
    const raw = localStorage.getItem('reader_extended_prefs')
    if (raw) {
      const parsed = JSON.parse(raw)
      Object.assign(prefs, parsed)
      if (parsed.lastDayTheme) lastDayTheme.value = parsed.lastDayTheme
    }
  } catch {}
  applyMotionSpeed(prefs.transitionSpeed)
}

function saveLocalPrefs() {
  try {
    const toSave = { ...prefs, lastDayTheme: lastDayTheme.value }
    localStorage.setItem('reader_extended_prefs', JSON.stringify(toSave))
  } catch {}
  applyMotionSpeed(prefs.transitionSpeed)
}

function toggleNightMode() {
  if (isNightMode.value) {
    prefs.theme = lastDayTheme.value || 'sepia'
  } else {
    lastDayTheme.value = prefs.theme
    prefs.theme = 'midnight'
  }
  saveLocalPrefs()
}

async function load() {
  loading.value = true
  error.value = ''
  loadLocalPrefs()
  try {
    const [b, c, p, m, s] = await Promise.all([
      api<Book>(path),
      api<Chapter[]>(`${path}/chapters`),
      api<Progress | null>(`${path}/progress`),
      api<Bookmark[]>(`${path}/bookmarks`),
      api<Partial<Settings>>('/settings')
    ])
    book.value = b
    chapters.value = c || []
    bookmarks.value = m || []
    if (s) {
      if (s.fontSize) prefs.fontSize = s.fontSize
      if (s.lineHeight) prefs.lineHeight = s.lineHeight
      if (s.width) prefs.width = s.width
      if (!localStorage.getItem('reader_extended_prefs') && s.theme) {
        prefs.theme = mapBackendToTheme(s.theme)
      }
    }
    version.value = p?.version || 0
    saveBook(b).catch(() => {})
    saveChapters(b.id, chapters.value).catch(() => {})
    const chapter = props.initialChapter || p?.chapterId || chapters.value[0]?.id
    if (chapter) {
      await openChapter(chapter, p?.chapterId === chapter ? p.position : 0, false)
    }
    loadSourcesList()
  } catch (e) {
    const cachedBook = await getBook(props.bookId)
    const cachedChapters = await getChapters(props.bookId)
    if (cachedBook && cachedChapters && cachedChapters.length > 0) {
      book.value = cachedBook
      chapters.value = cachedChapters
      bookmarks.value = []
      const chapter = props.initialChapter || cachedBook.progress?.chapterId || cachedChapters[0]?.id
      if (chapter) {
        await openChapter(chapter, cachedBook.progress?.chapterId === chapter ? cachedBook.progress.position : 0, false)
      }
    } else {
      error.value = message(e)
    }
    loading.value = false
  }
}

async function loadSourcesList() {
  try {
    const srcs = await api<Source[]>('/sources')
    if (Array.isArray(srcs)) {
      const map: Record<string, string> = {}
      const ids: string[] = []
      for (const s of srcs) {
        map[s.id] = s.name
        if (s.enabled) ids.push(s.id)
      }
      sourcesMap.value = map
      enabledSourceIds.value = ids
    }
  } catch {}
}

function updateSlidePageMetrics(syncCurrentPage = true) {
  if (!scroller.value || prefs.pageMode !== 'slide') return
  const el = scroller.value
  const visibleWidth = el.clientWidth
  const paper = el.querySelector<HTMLElement>('.reading-paper')
  const style = getComputedStyle(el)
  if (paper && visibleWidth > 0) {
    const height = Math.max(120, Math.floor(el.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)))
    const inset = prefs.adaptiveWidth ? 20 : Math.max(24, (visibleWidth - prefs.width) / 2)
    paper.style.setProperty('--slide-height', `${height}px`)
    paper.style.setProperty('--slide-inset', `${inset}px`)
  }
  const totalWidth = el.scrollWidth
  if (visibleWidth > 0) {
    totalSlidePages.value = Math.max(1, Math.round(totalWidth / visibleWidth))
    if (syncCurrentPage) {
      slidePage.value = Math.min(totalSlidePages.value - 1, Math.max(0, Math.round(el.scrollLeft / visibleWidth)))
    }
  }
}

let slideAnimating = false
let slideLockTimer: ReturnType<typeof setTimeout> | undefined
let slideSnapTimer: ReturnType<typeof setTimeout> | undefined

async function restore(ratio: number) {
  restoring = true
  clearTimeout(slideLockTimer)
  clearTimeout(slideSnapTimer)
  slideAnimating = false
  position.value = Math.max(0, Math.min(1, ratio))
  await nextTick()
  await new Promise<void>(resolve => requestAnimationFrame(() => resolve()))
  if (scroller.value) {
    if (prefs.pageMode === 'slide') {
      scroller.value.scrollTop = 0
      updateSlidePageMetrics(false)
      await nextTick()
      updateSlidePageMetrics(false)
      const targetPage = Math.round(position.value * (totalSlidePages.value - 1))
      scrollToSlidePage(targetPage, false)
    } else {
      scroller.value.scrollLeft = 0
      scroller.value.scrollTop = position.value * Math.max(0, scroller.value.scrollHeight - scroller.value.clientHeight)
    }
  }
  setTimeout(() => (restoring = false), 120)
}

function scrollToSlidePage(page: number, smooth = true) {
  if (!scroller.value) return
  updateSlidePageMetrics(false)
  const p = Math.max(0, Math.min(totalSlidePages.value - 1, page))
  slidePage.value = p
  scroller.value.scrollTop = 0
  const scrollTarget = p * scroller.value.clientWidth
  clearTimeout(slideLockTimer)
  clearTimeout(slideSnapTimer)
  if (smooth && prefs.transitionSpeed !== 'off') {
    slideAnimating = true
    scroller.value.scrollTo({ left: scrollTarget, behavior: 'smooth' })
    slideLockTimer = setTimeout(() => {
      slideAnimating = false
      if (scroller.value && prefs.pageMode === 'slide') {
        scroller.value.scrollLeft = p * scroller.value.clientWidth
      }
    }, 420)
  } else {
    slideAnimating = false
    scroller.value.scrollLeft = scrollTarget
  }
  if (totalSlidePages.value > 1) {
    position.value = p / (totalSlidePages.value - 1)
  } else {
    position.value = 1
  }
  dirty.value = true
  savedLabel.value = '待保存'
  clearTimeout(timer)
  if (!conflict.value) timer = setTimeout(() => saveProgress(), 1400)
}

async function prefetchNextChapter(currentChapterId: string) {
  const currIdx = chapters.value.findIndex(c => c.id === currentChapterId)
  if (currIdx >= 0 && currIdx < chapters.value.length - 1) {
    const nextChap = chapters.value[currIdx + 1]
    if (nextChap && nextChap.id) {
      try {
        const cached = await getChapterContent(props.bookId, nextChap.id)
        if (!cached) {
          const res = await api<ChapterContent>(`${path}/chapters/${id(nextChap.id)}`)
          if (res && res.content !== undefined) {
            await saveChapterContent(props.bookId, nextChap.id, res.title || nextChap.title, res.content)
          }
        }
      } catch {}
    }
  }
}

async function openChapter(chapterId: string, ratio = 0, save = true) {
  if (chapterLoading.value || restoring) return
  if (save && dirty.value && !(await saveProgress())) return
  clearTimeout(timer)
  lastTargetChapterId.value = chapterId
  chapterLoading.value = true
  error.value = ''
  try {
    const next = await api<ChapterContent>(`${path}/chapters/${id(chapterId)}`)
    content.value = next
    isFromCache.value = false
    panel.value = ''
    chapterLoading.value=false;loading.value=false;await restore(ratio)
    dirty.value = true
    savedLabel.value = '待保存'
    if (active) timer = setTimeout(() => saveProgress(), 1500)
    const chapTitle = next.title || chapters.value.find(c => c.id === chapterId)?.title || ''
    saveChapterContent(props.bookId, chapterId, chapTitle, next.content).catch(() => {})
    prefetchNextChapter(chapterId).catch(() => {})
  } catch (e) {
    const cached = await getChapterContent(props.bookId, chapterId)
    if (cached) {
      content.value = {
        id: cached.chapterId,
        title: cached.title,
        content: cached.content,
        format: 'text'
      }
      isFromCache.value = true
      error.value = ''
      panel.value = ''
      chapterLoading.value=false;loading.value=false;await restore(ratio)
      dirty.value = true
      savedLabel.value = '待保存'
      if (active) timer = setTimeout(() => saveProgress(), 1500)
    } else {
      error.value = message(e)
    }
  } finally {
    chapterLoading.value = false
  }
}

async function retryFailedChapter() {
  const targetId = lastTargetChapterId.value || content.value?.id
  if (targetId && chapters.value.length) {
    await openChapter(targetId, targetId === content.value?.id ? position.value : 0, false)
  } else {
    await load()
  }
}

function getScrollPageStep(el: HTMLElement) {
  const linePx = Math.max(24, Math.round(prefs.fontSize * prefs.lineHeight))
  return Math.max(120, el.clientHeight - linePx)
}

function onScroll() {
  if (restoring || slideAnimating || chapterLoading.value || !content.value || !scroller.value) return
  const el = scroller.value
  if (prefs.pageMode === 'slide') {
    if (el.clientWidth > 0) {
      totalSlidePages.value = Math.max(1, Math.round(el.scrollWidth / el.clientWidth))
      slidePage.value = Math.min(totalSlidePages.value - 1, Math.max(0, Math.round(el.scrollLeft / el.clientWidth)))
      position.value = totalSlidePages.value > 1 ? slidePage.value / (totalSlidePages.value - 1) : 1
      clearTimeout(slideSnapTimer)
      const alignedLeft = slidePage.value * el.clientWidth
      if (Math.abs(el.scrollLeft - alignedLeft) > 2) {
        slideSnapTimer = setTimeout(() => {
          if (scroller.value && prefs.pageMode === 'slide' && !slideAnimating && !restoring) {
            scroller.value.scrollLeft = slidePage.value * scroller.value.clientWidth
          }
        }, 120)
      }
    }
  } else {
    const max = el.scrollHeight - el.clientHeight
    position.value = max > 0 ? Math.min(1, Math.max(0, el.scrollTop / max)) : 1
    if (showControls.value && !panel.value && !changeSourceOpen.value && el.scrollTop > 60) {
      showControls.value = false
    }
  }
  dirty.value = true
  savedLabel.value = '待保存'
  clearTimeout(timer)
  if (!conflict.value) timer = setTimeout(() => saveProgress(), 1400)
}

function onSliderScrub(e: Event) {
  const target = e.target as HTMLInputElement
  const ratio = parseFloat(target.value) / 100
  if (scroller.value) {
    if (prefs.pageMode === 'slide') {
      updateSlidePageMetrics(false)
      const targetPage = Math.round(ratio * (totalSlidePages.value - 1))
      scrollToSlidePage(targetPage, false)
    } else {
      const max = scroller.value.scrollHeight - scroller.value.clientHeight
      scroller.value.scrollTop = ratio * max
      position.value = ratio
    }
    dirty.value = true
    savedLabel.value = '待保存'
  }
}

// Previous Page / Action (handles edge of chapter -> prev chapter)
async function prevPage() {
  if (chapterLoading.value || saving.value) return
  if (prefs.pageMode === 'slide') {
    if (slidePage.value > 0) {
      scrollToSlidePage(slidePage.value - 1, true)
    } else if (index.value > 0) {
      // At beginning of chapter: navigate to previous chapter's end
      await openChapter(chapters.value[index.value - 1].id, 1)
    }
  } else {
    if (scroller.value) {
      const step = getScrollPageStep(scroller.value)
      if (scroller.value.scrollTop <= 10 && index.value > 0) {
        await openChapter(chapters.value[index.value - 1].id, 1)
      } else {
        scroller.value.scrollBy({ top: -step, behavior: 'smooth' })
      }
    }
  }
}

// Next Page / Action (handles edge of chapter -> next chapter: 翻页等于下一章)
async function nextPage() {
  if (chapterLoading.value || saving.value) return
  if (prefs.pageMode === 'slide') {
    if (slidePage.value < totalSlidePages.value - 1) {
      scrollToSlidePage(slidePage.value + 1, true)
    } else if (index.value < chapters.value.length - 1) {
      // Reached bottom / last page: flip page equals NEXT CHAPTER!
      await openChapter(chapters.value[index.value + 1].id, 0)
    }
  } else {
    if (scroller.value) {
      const max = scroller.value.scrollHeight - scroller.value.clientHeight
      const remaining = max - scroller.value.scrollTop
      if (remaining <= 30 && index.value < chapters.value.length - 1) {
        await openChapter(chapters.value[index.value + 1].id, 0)
      } else {
        const step = getScrollPageStep(scroller.value)
        scroller.value.scrollBy({ top: step, behavior: 'smooth' })
      }
    }
  }
}

// Touch Gestures for Mobile Swipe
function onTouchStart(e: TouchEvent) {
  if (e.touches.length !== 1) return
  touchStartX = e.touches[0].clientX
  touchStartY = e.touches[0].clientY
  touchStartTime = Date.now()
}

function onTouchEnd(e: TouchEvent) {
  if (e.changedTouches.length !== 1) return
  const deltaX = e.changedTouches[0].clientX - touchStartX
  const deltaY = e.changedTouches[0].clientY - touchStartY
  const duration = Date.now() - touchStartTime

  // Horizontal swipe gesture detection
  if (Math.abs(deltaX) > 30 && Math.abs(deltaX) > Math.abs(deltaY) * 1.2 && duration < 1200) {
    if (deltaX < 0) {
      // Swipe left -> Next Page / Next Chapter
      nextPage()
    } else {
      // Swipe right -> Previous Page / Previous Chapter
      prevPage()
    }
  }
}

// Fullscreen Click Area Navigation
function onReaderBodyClick(e: MouseEvent) {
  const target = e.target as HTMLElement
  if (target.closest('button, a, input, select, textarea, .reader-floating-header, .reader-floating-footer, .reader-drawer, .chapter-navigation, .modal-backdrop, .modal-card, .drawer-drag-bar')) {
    return
  }
  if (panel.value) {
    panel.value = ''
    return
  }
  if (changeSourceOpen.value) {
    changeSourceOpen.value = false
    return
  }

  const width = window.innerWidth
  const clickX = e.clientX

  // Left 28%: Previous Page / Chapter
  if (clickX < width * 0.28) {
    prevPage()
    return
  }
  // Right 28%: Next Page / Chapter (翻页等于下一章)
  if (clickX > width * 0.72) {
    nextPage()
    return
  }
  // Center 44%: Toggle floating navigation bars
  showControls.value = !showControls.value
}

// Change Source Logic
async function openChangeSourceModal() {
  changeSourceOpen.value = true
  changeSourceError.value = ''
  changeSourceQuery.value = book.value?.title || ''
  await executeSearchSources()
}

async function executeSearchSources() {
  if (!changeSourceQuery.value.trim()) return
  changeSourceLoading.value = true
  changeSourceError.value = ''
  try {
    if (changeSourceQuery.value.trim() === (book.value?.title || '').trim()) {
      try {
        const candidates = await api<BookSourceCandidate[]>(`${path}/sources`)
        if (Array.isArray(candidates) && candidates.length > 0) {
          changeSourceResults.value = candidates.map(c => ({
            sourceId: c.sourceId,
            title: c.title,
            author: c.author,
            url: c.bookUrl || '',
            cover: c.cover || '',
            intro: '',
            latestChapter: c.latestChapter
          }))
          changeSourceLoading.value = false
          return
        }
      } catch {}
    }
    if (!enabledSourceIds.value.length) {
      await loadSourcesList()
    }
    const sourceIds = enabledSourceIds.value.slice(0, 50)
    if (!sourceIds.length) {
      changeSourceError.value = '当前暂无已启用的个人书源，请先前往「书源管理」导入并启用书源。'
      return
    }
    const results = await api<SearchResult[]>('/search', 'POST', {
      keyword: changeSourceQuery.value.trim(),
      sourceIds,
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

async function applyChangeSource(result: SearchResult) {
  if (changeSourceSubmitting.value) return
  changeSourceSubmitting.value = true
  changeSourceError.value = ''
  try {
    const res = await api<{ ok: boolean; currentChapterId: string; sourceName: string }>(`${path}/change-source`, 'POST', {
      sourceId: result.sourceId,
      url: result.url,
      currentChapterTitle: currentChapterName.value
    })
    notice.value = `换源成功！已切至「${res.sourceName || '新书源'}」，章节已智能衔接。`
    changeSourceOpen.value = false
    setTimeout(() => { notice.value = '' }, 3500)
    // Reload book metadata and chapters
    const [b, c] = await Promise.all([
      api<Book>(path),
      api<Chapter[]>(`${path}/chapters`)
    ])
    book.value = b
    chapters.value = c || []
    if (res.currentChapterId) {
      await openChapter(res.currentChapterId, 0, false)
    } else if (chapters.value[0]) {
      await openChapter(chapters.value[0].id, 0, false)
    }
  } catch (e) {
    changeSourceError.value = message(e)
  } finally {
    changeSourceSubmitting.value = false
  }
}

async function saveProgress(): Promise<boolean> {
  if (savePromise) {
    await savePromise
    return dirty.value && !conflict.value ? saveProgress() : !conflict.value
  }
  if (!content.value || !dirty.value) return true
  if (conflict.value) return false
  clearTimeout(timer)
  const snapshot = { chapterId: content.value.id, position: position.value, version:version.value }
  saving.value = true
  savePromise = (async () => {
    try {
      const saved = await api<Progress>(`${path}/progress`, 'PUT', snapshot)
      version.value = saved.version
      dirty.value = content.value?.id !== snapshot.chapterId || Math.abs(position.value - snapshot.position) > 0.0001
      savedLabel.value = dirty.value ? '待同步' : '已同步到云端'
      if (book.value) {
        book.value.progress = {
          chapterId: snapshot.chapterId,
          position: snapshot.position,
          version: saved.version
        }
        saveBook(book.value).catch(() => {})
      }
      return true
    } catch (e) {
      if (e instanceof ApiError && e.status===409) {
        conflict.value = true
        error.value = '另一设备已更新阅读进度。请选择要保留的位置。'
        savedLabel.value = '同步冲突'
        return false
      } else {
        if (book.value) {
          book.value.progress = {
            chapterId: snapshot.chapterId,
            position: snapshot.position,
            version: version.value
          }
          saveBook(book.value).catch(() => {})
        }
        if (isFromCache.value || !navigator.onLine || (e instanceof ApiError && e.status === 0)) {
          dirty.value = false
          savedLabel.value = '已保存到本机（离线）'
          return true
        }
        error.value = message(e)
        savedLabel.value = '同步失败，可重试'
        return false
      }
    } finally {
      saving.value = false
      savePromise = null
    }
  })()
  return savePromise
}

async function resolveConflict(keepLocal: boolean) {
  try {
    const remote = await api<Progress>(`${path}/progress`)
    version.value = remote?.version || 0
    conflict.value = false
    error.value = ''
    if (keepLocal) {
      dirty.value = true
      await saveProgress()
    } else {
      dirty.value = false
      if (remote?.chapterId) await openChapter(remote.chapterId, remote.position, false)
      else if (chapters.value[0]) await openChapter(chapters.value[0].id, 0, false)
    }
  } catch (e) {
    error.value = message(e)
  }
}

async function canLeave() {
  clearTimeout(timer)
  if (!dirty.value) return true
  const saved = await saveProgress()
  if (saved && dirty.value) return saveProgress()
  if (!saved) {
    return await showConfirm({
      title: '未保存的阅读进度',
      message: '阅读进度尚未保存。确定放弃本次未保存的位置并离开吗？',
      confirmText: '放弃离开',
      cancelText: '留在本页',
      danger: true
    })
  }
  return true
}

function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) {
    e.preventDefault()
    e.returnValue = ''
  }
}

async function addBookmark() {
  if (!content.value) return
  bookmarkBusy.value = true
  error.value = ''
  try {
    await api(`${path}/bookmarks`, 'POST', {
      chapterId: content.value.id,
      position: position.value,
      note: note.value.trim()
    })
    bookmarks.value = (await api<Bookmark[]>(`${path}/bookmarks`)) || []
    note.value = ''
    notice.value = '书签已保存。'
    setTimeout(() => { notice.value = '' }, 2500)
  } catch (e) {
    error.value = message(e)
  } finally {
    bookmarkBusy.value = false
  }
}

async function removeBookmark(mark: Bookmark) {
  bookmarkBusy.value = true
  error.value = ''
  try {
    await api(`/bookmarks/${id(mark.id)}`, 'DELETE')
    bookmarks.value = bookmarks.value.filter(b => b.id !== mark.id)
    notice.value = '书签已删除。'
    setTimeout(() => { notice.value = '' }, 2500)
  } catch (e) {
    error.value = message(e)
  } finally {
    bookmarkBusy.value = false
  }
}

async function saveSettings() {
  const backendPayload = {
    theme: mapThemeToBackend(prefs.theme),
    fontSize: prefs.fontSize,
    lineHeight: prefs.lineHeight,
    fontFamily: prefs.fontFamily,
    width: prefs.width
  }
  const err = settingsError(backendPayload)
  if (err) {
    error.value = err
    return
  }
  settingsBusy.value = true
  saveLocalPrefs()
  try {
    await api('/settings', 'PUT', backendPayload)
    notice.value = '阅读偏好已保存并同步。'
    setTimeout(() => { notice.value = '' }, 2500)
  } catch (e) {
    error.value = message(e)
  } finally {
    settingsBusy.value = false
  }
}

async function scrollToActiveTocItem() {
  tocQuery.value = ''
  await nextTick()
  const activeBtn = document.querySelector('.reader-toc button.selected') as HTMLElement | null
  activeBtn?.scrollIntoView({ behavior: 'smooth', block: 'center' })
}

function toggle(value: 'toc' | 'settings' | 'bookmarks') {
  panel.value = panel.value === value ? '' : value
  if (panel.value) {
    showControls.value = true
    if (panel.value === 'toc') {
      void refreshCachedChapterIds()
      void scrollToActiveTocItem()
    }
  }
}

function toggleFullscreen() {
  if (!document.fullscreenElement) {
    document.documentElement.requestFullscreen().then(() => (isFullscreen.value = true)).catch(() => {})
  } else {
    document.exitFullscreen().then(() => (isFullscreen.value = false)).catch(() => {})
  }
}

function keydown(e: KeyboardEvent) {
  const target = e.target as HTMLElement | null
  const inInput = !!target?.closest('input, textarea, select')
  if (e.key === 'Escape') {
    if (panel.value) panel.value = ''
    else if (changeSourceOpen.value) changeSourceOpen.value = false
    else showControls.value = !showControls.value
    return
  }
  if (inInput || panel.value || changeSourceOpen.value) return
  if (e.key === 'ArrowLeft' || e.key === 'PageUp' || (prefs.pageMode === 'slide' && e.key === 'ArrowUp')) {
    e.preventDefault()
    prevPage()
  } else if (e.key === 'ArrowRight' || e.key === 'PageDown' || (prefs.pageMode === 'slide' && e.key === 'ArrowDown')) {
    e.preventDefault()
    nextPage()
  } else if (e.key === ' ') {
    e.preventDefault()
    if (e.shiftKey) prevPage()
    else nextPage()
  }
}

function adjustFontSize(delta: number) {
  prefs.fontSize = Math.min(settingLimits.fontSize.max, Math.max(settingLimits.fontSize.min, prefs.fontSize + delta))
  saveLocalPrefs()
}

function adjustLineHeight(delta: number) {
  const next = Math.round((prefs.lineHeight + delta) * 10) / 10
  prefs.lineHeight = Math.min(settingLimits.lineHeight.max, Math.max(settingLimits.lineHeight.min, next))
  saveLocalPrefs()
}

function adjustParaSpacing(delta: number) {
  const next = Math.round((prefs.paraSpacing + delta) * 10) / 10
  prefs.paraSpacing = Math.min(2.5, Math.max(0.4, next))
  saveLocalPrefs()
  saveLocalPrefs()
}

function adjustLetterSpacing(delta: number) {
  const next = Math.round(((prefs.letterSpacing || 0) + delta) * 10) / 10
  prefs.letterSpacing = Math.min(3, Math.max(0, next))
  saveLocalPrefs()
}

watch(
  () => [prefs.fontSize, prefs.lineHeight, prefs.paraSpacing, prefs.fontFamily, prefs.width, prefs.textAlign, prefs.pageMode, prefs.adaptiveWidth, prefs.indent, prefs.letterSpacing, prefs.wakeLock, prefs.chineseMode],
  () => {
    saveLocalPrefs()
    if (content.value) restore(position.value)
  }
)

watch(
  () => prefs.theme,
  () => {
    saveLocalPrefs()
  }
)

let readingResizeObserver: ResizeObserver | undefined
function handleResize() {
  if (prefs.pageMode === 'slide' && content.value) void restore(position.value)
}

onMounted(() => {
  load()
  updateStatusInfo()
  clockTimer = setInterval(updateStatusInfo, 30000)
  if (prefs.wakeLock) void requestWakeLock()
  void refreshCachedChapterIds()
  window.addEventListener('beforeunload', beforeUnload)
  window.addEventListener('keydown', keydown)
  window.addEventListener('resize', handleResize)
  readingResizeObserver = new ResizeObserver(handleResize)
  if (scroller.value) readingResizeObserver.observe(scroller.value)
  document.fonts?.addEventListener('loadingdone', handleResize)
  document.addEventListener('fullscreenchange', () => {
    isFullscreen.value = !!document.fullscreenElement
  })
})

onUnmounted(() => {
  active = false
  clearTimeout(timer)
  clearTimeout(slideLockTimer)
  clearTimeout(slideSnapTimer)
  stopTts()
  clearInterval(clockTimer)
  releaseWakeLock()
  window.removeEventListener('beforeunload', beforeUnload)
  window.removeEventListener('keydown', keydown)
  window.removeEventListener('resize', handleResize)
  readingResizeObserver?.disconnect()
  document.fonts?.removeEventListener('loadingdone', handleResize)
})

defineExpose({canLeave})
</script>

<template>
  <div
    :class="['reader', `theme-${prefs.theme}`, { 'is-night': isNightMode, 'adaptive-mode': prefs.adaptiveWidth }]"
    :style="{
      '--reader-para-spacing': `${prefs.paraSpacing}em`,
      '--reader-align': prefs.textAlign,
      '--reader-indent': prefs.indent ? '2em' : '0',
      '--reader-letter-spacing': `${prefs.letterSpacing || 0}px`,
      '--reader-duration': speedDurations[prefs.transitionSpeed] || '240ms'
    }"
  >
    <!-- Top Floating Capsule Header -->
    <header :class="['reader-floating-header', { hidden: !showControls && !panel && !changeSourceOpen }]">
      <div class="reader-header-left">
        <a class="reader-header-back" href="#/library" title="返回书架">
          <ArrowLeft :size="16" />
          <span>书架</span>
        </a>
      </div>

      <div class="reader-header-title">
        <strong>{{ book?.title || '阅读空间' }}</strong>
        <span>
          {{ currentChapterName || '正在载入书页…' }}
          <span v-if="isFromCache" class="offline-badge" style="font-size: 10px; padding: 1px 6px;">离线已缓存</span>
        </span>
      </div>

      <div class="reader-header-tools">
        <!-- Change Source Quick Action (Desktop) -->
        <button
          v-if="book?.kind === 'online'"
          class="tool-btn-highlight desktop-only"
          :class="{ selected: changeSourceOpen }"
          @click="openChangeSourceModal"
          aria-label="一键换源"
          title="为此书更换书源"
        >
          <Shuffle :size="16" />
          <span class="tool-label">换源</span>
        </button>

        <!-- Quick Day / Night Toggle (Desktop) -->
        <button
          class="desktop-only"
          @click="toggleNightMode"
          :aria-label="isNightMode ? '切换到日间模式' : '切换到夜间护眼模式'"
          :title="isNightMode ? '切换到日间模式' : '切换到夜间护眼模式'"
        >
          <Sun v-if="isNightMode" :size="17" />
          <Moon v-else :size="17" />
        </button>

        <!-- TOC Button (Desktop) -->
        <button
          class="desktop-only"
          :class="{ selected: panel === 'toc' }"
          :aria-expanded="panel === 'toc'"
          @click="toggle('toc')"
          aria-label="章节目录"
          title="章节目录"
        >
          <List :size="18" />
        </button>
        <!-- Bookmarks Button -->
        <button
          :class="{ selected: panel === 'bookmarks' }"
          :aria-expanded="panel === 'bookmarks'"
          @click="toggle('bookmarks')"
          aria-label="书签"
          title="书签"
        >
          <BookmarkIcon :size="18" />
        </button>
        <!-- Batch Offline Cache Button (Desktop) -->
        <button
          class="desktop-only"
          :class="{ selected: cacheModalOpen }"
          @click="openCacheModal"
          aria-label="离线缓存"
          title="批量离线缓存"
        >
          <Download :size="17" />
        </button>

        <!-- TTS Audio Player Button (Desktop) -->
        <button
          class="desktop-only"
          :class="{ selected: ttsActive }"
          @click="ttsActive ? stopTts() : startTts(0)"
          aria-label="听书朗读"
          title="听书朗读 (TTS)"
        >
          <Headphones :size="17" />
          <Headphones :size="17" />
        </button>

        <!-- Export Book TXT Button (Desktop) -->
        <a
          class="button desktop-only"
          :href="getExportUrl(props.bookId)"
          target="_blank"
          aria-label="导出TXT"
          title="导出全书已缓存章节为TXT文档"
          style="text-decoration: none; padding: 0 10px; display: inline-flex; align-items: center; gap: 4px;"
        >
          <Download :size="16" />
          <span class="tool-label">导出</span>
        </a>

        <!-- Settings Button -->
        <button
          :class="{ selected: panel === 'settings' }"
          :aria-expanded="panel === 'settings'"
          @click="toggle('settings')"
          aria-label="阅读偏好设置"
          title="阅读偏好设置"
        >
          <Settings2 :size="18" />
        </button>

        <!-- Fullscreen Button -->
        <button
          @click="toggleFullscreen"
          :aria-label="isFullscreen ? '退出全屏' : '全屏沉浸阅读'"
          :title="isFullscreen ? '退出全屏' : '全屏沉浸阅读'"
        >
          <Minimize2 v-if="isFullscreen" :size="17" />
          <Maximize2 v-else :size="17" />
        </button>
      </div>
    </header>

    <!-- Notification Banners -->
    <div v-if="error" class="error reader-message" role="alert" style="margin: 70px 24px 0; z-index: 45;">
      <span>{{ error }}</span>
      <div v-if="conflict" class="actions">
        <button @click="resolveConflict(false)" :disabled="saving">使用云端位置</button>
        <button @click="resolveConflict(true)" :disabled="saving">保留当前位置并覆盖</button>
      </div>
      <div v-else class="actions">
        <button v-if="book?.kind === 'online'" class="text-button" @click="openChangeSourceModal">
          <Shuffle :size="15" /> 一键换源
        </button>
        <button class="text-button" @click="retryFailedChapter">
          <RefreshCw :size="14" /> 重试加载
        </button>
        <button class="icon-button" @click="error = ''" aria-label="关闭错误提示"><X :size="15" /></button>
      </div>
    </div>
    <div v-if="notice" class="success reader-message" role="status" style="margin: 70px 24px 0; z-index: 45;">
      {{ notice }}
      <button class="icon-button" @click="notice = ''" aria-label="关闭提示"><X :size="15" /></button>
    </div>

    <!-- Main Reading Body Canvas with Touch Gestures & 3-Area Click -->
    <div
      class="reader-body"
      @click="onReaderBodyClick"
      @touchstart.passive="onTouchStart"
      @touchend.passive="onTouchEnd"
    >
      <!-- Backdrop for Drawers -->
      <div v-if="panel" class="reader-backdrop" @click="panel = ''"></div>

      <!-- Rounded Slide-In Drawer Panels (Native Bottom Sheet on Mobile) -->
      <aside v-if="panel" class="reader-drawer" :class="{ 'is-toc': panel === 'toc' }" :aria-label="panel === 'toc' ? '章节目录' : panel === 'settings' ? '阅读设置' : '我的书签'">
        <div class="drawer-drag-bar" aria-hidden="true" @click="panel = ''"></div>
        <div class="drawer-header">
          <h2>{{ panel === 'toc' ? '章节目录' : panel === 'settings' ? '阅读偏好' : '我的书签' }}</h2>
          <button class="icon-button" @click="panel = ''" aria-label="关闭面板"><X :size="18" /></button>
        </div>
        <div class="drawer-body">
          <!-- 1. TOC Panel -->
          <template v-if="panel === 'toc'">
            <div class="reader-toc-toolbar">
              <input v-model="tocQuery" placeholder="搜索章节名称…" aria-label="搜索章节" />
              <div class="reader-toc-actions">
                <button
                  type="button"
                  class="secondary compact-btn"
                  @click="scrollToActiveTocItem"
                  title="定位当前阅读章节"
                >
                  <LocateFixed :size="14" />
                  <span>当前章</span>
                </button>
                <button
                  type="button"
                  class="secondary compact-btn"
                  @click="tocReversed = !tocReversed"
                  :title="tocReversed ? '切换为正序' : '切换为倒序'"
                >
                  <ArrowUpDown :size="14" />
                  <span>{{ tocReversed ? '倒序' : '正序' }}</span>
                </button>
              </div>
            </div>
            <div class="reader-toc">
              <button
                v-for="chapter in toc"
                :key="chapter.id"
                :class="{ selected: content?.id === chapter.id }"
                :disabled="chapterLoading || saving"
                @click="openChapter(chapter.id)"
              >
                <small>{{ chapter.index + 1 }}</small>
                <span>{{ chapter.title }}</span>
                <span v-if="cachedChapterIds.has(chapter.id)" class="toc-cached-badge">已缓存</span>
                <Check v-if="content?.id === chapter.id" :size="14" style="margin-left: auto;" />
              </button>
              <p v-if="!toc.length" class="muted" style="text-align: center; padding: 20px 0;">未找到匹配章节</p>
            </div>
          </template>

          <!-- 2. Settings Panel -->
          <div v-else-if="panel === 'settings'" class="form-stack">
            <!-- Tabs: Preferences vs Purification Rules -->
            <div class="settings-tab-bar">
              <button
                type="button"
                :class="['tab-btn', { active: settingsActiveTab === 'prefs' }]"
                @click="settingsActiveTab = 'prefs'"
              >
                排版与操作
              </button>
              <button
                type="button"
                :class="['tab-btn', { active: settingsActiveTab === 'purify' }]"
                @click="settingsActiveTab = 'purify'"
              >
                净化规则 ({{ purifyRules.filter(r => r.enabled).length }}/{{ purifyRules.length }})
              </button>
            </div>

            <!-- Tab 1: Prefs -->
            <form v-if="settingsActiveTab === 'prefs'" class="form-stack" @submit.prevent="saveSettings">
              <!-- Quick Actions Bar -->
              <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin-bottom: 8px;">
                <button
                  type="button"
                  class="button secondary"
                  @click="panel = ''; ttsActive ? stopTts() : startTts(0)"
                >
                  <Headphones :size="15" />
                  <span>{{ ttsActive ? '关闭听书' : '听书朗读' }}</span>
                </button>
                <button
                  type="button"
                  class="button secondary"
                  @click="panel = ''; openCacheModal()"
                >
                  <Download :size="15" />
                  <span>离线缓存</span>
                </button>
              </div>
            <!-- 排版实时预览卡片 (UI改造 U06) -->
            <div
              class="reader-prefs-preview"
              :class="`theme-${prefs.theme}`"
              :style="{
                fontFamily: currentFont,
                fontSize: `${Math.min(Math.max(prefs.fontSize, 15), 22)}px`,
                lineHeight: prefs.lineHeight,
                letterSpacing: `${prefs.letterSpacing || 0}px`,
                textAlign: prefs.textAlign
              }"
            >
              <div class="preview-header">
                <Sparkles :size="12" />
                <span>排版实时预览</span>
              </div>
              <p>行云流水，落纸如烟。文字在指尖静谧流淌，于方寸之间照见辽阔天地。</p>
            </div>

            <!-- 第一层：高频排版核心调节 (常驻) -->
            <!-- Font Size -->
            <div class="setting-row">
              <div class="setting-header">
                <span>正文字号</span>
                <div class="setting-stepper">
                  <button type="button" class="stepper-btn" @click="adjustFontSize(-1)">−</button>
                  <span style="font-weight: 600; min-width: 48px; text-align: center;">{{ prefs.fontSize }}px</span>
                  <button type="button" class="stepper-btn" @click="adjustFontSize(1)">+</button>
                </div>
              </div>
              <input v-model.number="prefs.fontSize" type="range" :min="settingLimits.fontSize.min" :max="settingLimits.fontSize.max" step="1" />
            </div>

            <!-- Line Height -->
            <div class="setting-row">
              <div class="setting-header">
                <span>行距倍率</span>
                <div class="setting-stepper">
                  <button type="button" class="stepper-btn" @click="adjustLineHeight(-0.1)">−</button>
                  <span style="font-weight: 600; min-width: 48px; text-align: center;">{{ prefs.lineHeight }}x</span>
                  <button type="button" class="stepper-btn" @click="adjustLineHeight(0.1)">+</button>
                </div>
              </div>
              <input v-model.number="prefs.lineHeight" type="range" :min="settingLimits.lineHeight.min" :max="settingLimits.lineHeight.max" step="0.1" />
            </div>

            <!-- Page Navigation Mode Selection -->
            <div class="settings-section-title">
              <Columns :size="13" style="display: inline; margin-right: 4px;" />
              翻页阅读方式
            </div>
            <div class="mode-selector-grid">
              <button
                type="button"
                :class="['mode-tab-btn', { active: prefs.pageMode === 'slide' }]"
                @click="prefs.pageMode = 'slide'"
              >
                <Columns :size="16" />
                <div class="mode-text">
                  <strong>左右滑动翻页</strong>
                  <small>触控滑屏 · 点击两侧 · 翻页即换章</small>
                </div>
              </button>
              <button
                type="button"
                :class="['mode-tab-btn', { active: prefs.pageMode === 'scroll' }]"
                @click="prefs.pageMode = 'scroll'"
              >
                <Scroll :size="16" />
                <div class="mode-text">
                  <strong>上下连续滚动</strong>
                  <small>垂直滑动 · 鼠标滚轮 · 触底换章</small>
                </div>
              </button>
            </div>

            <!-- Theme Color Swatches -->
            <div class="settings-section-title">
              <Palette :size="13" style="display: inline; margin-right: 4px;" />
              阅读背景纸张 (含深邃夜读)
            </div>
            <div class="theme-swatches">
              <div
                v-for="t in themes"
                :key="t.id"
                :class="['theme-swatch', { selected: prefs.theme === t.id }]"
                @click="prefs.theme = t.id"
                :title="t.desc"
              >
                <div class="swatch-circle" :style="{ background: t.bg, borderColor: t.border }"></div>
                <span>{{ t.name }}</span>
              </div>
            </div>

            <!-- 第二层：精选中文字体与简繁转换 -->
            <div class="settings-section-title">
              <Type :size="13" style="display: inline; margin-right: 4px;" />
              精选中文字体
            </div>
            <div class="font-picker">
              <div
                v-for="f in fonts"
                :key="f.id"
                :class="['font-option', { selected: prefs.fontFamily === f.id }]"
                :style="{ fontFamily: f.family }"
                @click="prefs.fontFamily = f.id"
                :title="f.desc"
              >
                <strong>{{ f.name }}</strong>
                <small style="display: block; font-size: 10px; opacity: 0.75;">{{ f.desc }}</small>
              </div>
            </div>

            <!-- Chinese Simplified / Traditional Switch -->
            <div class="settings-section-title">
              <Languages :size="13" style="display: inline; margin-right: 4px;" />
              简繁转换
            </div>
            <div class="mode-selector-grid" style="grid-template-columns: repeat(3, 1fr);">
              <button
                type="button"
                :class="['mode-tab-btn', { active: !prefs.chineseMode || prefs.chineseMode === 'original' }]"
                @click="prefs.chineseMode = 'original'; saveLocalPrefs()"
              >
                <div class="mode-text">
                  <strong>原文</strong>
                  <small>书源原始字型</small>
                </div>
              </button>
              <button
                type="button"
                :class="['mode-tab-btn', { active: prefs.chineseMode === 'simplified' }]"
                @click="prefs.chineseMode = 'simplified'; saveLocalPrefs()"
              >
                <div class="mode-text">
                  <strong>简体</strong>
                  <small>规范简体中文</small>
                </div>
              </button>
              <button
                type="button"
                :class="['mode-tab-btn', { active: prefs.chineseMode === 'traditional' }]"
                @click="prefs.chineseMode = 'traditional'; saveLocalPrefs()"
              >
                <div class="mode-text">
                  <strong>繁体</strong>
                  <small>正体/繁体中文</small>
                </div>
              </button>
            </div>

            <!-- 第三层：更多排版细节 (折叠面板，功能06 & UI U06) -->
            <button
              type="button"
              class="advanced-prefs-toggle"
              :class="{ open: showAdvancedPrefs }"
              @click="showAdvancedPrefs = !showAdvancedPrefs"
            >
              <div class="toggle-title">
                <Sliders :size="14" />
                <span>更多排版与显示细节</span>
              </div>
              <ChevronUp v-if="showAdvancedPrefs" :size="15" />
              <ChevronDown v-else :size="15" />
            </button>

            <div v-show="showAdvancedPrefs" class="advanced-prefs-body">
              <!-- Paragraph Spacing -->
              <div class="setting-row">
                <div class="setting-header">
                  <span>段落间距</span>
                  <div class="setting-stepper">
                    <button type="button" class="stepper-btn" @click="adjustParaSpacing(-0.2)">−</button>
                    <span style="font-weight: 600; min-width: 48px; text-align: center;">{{ prefs.paraSpacing }}em</span>
                    <button type="button" class="stepper-btn" @click="adjustParaSpacing(0.2)">+</button>
                  </div>
                </div>
                <input v-model.number="prefs.paraSpacing" type="range" min="0.4" max="2.4" step="0.1" />
              </div>

              <!-- Letter Spacing -->
              <div class="setting-row">
                <div class="setting-header">
                  <span>字间微距</span>
                  <div class="setting-stepper">
                    <button type="button" class="stepper-btn" @click="adjustLetterSpacing(-0.2)">−</button>
                    <span style="font-weight: 600; min-width: 48px; text-align: center;">{{ prefs.letterSpacing || 0 }}px</span>
                    <button type="button" class="stepper-btn" @click="adjustLetterSpacing(0.2)">+</button>
                  </div>
                </div>
                <input v-model.number="prefs.letterSpacing" type="range" min="0" max="3" step="0.1" />
              </div>

              <!-- First Line Indent & WakeLock Toggles -->
              <div class="toggle-option-card" @click="prefs.indent = !prefs.indent">
                <div>
                  <strong>首行缩进 2 字符</strong>
                  <p>遵循中文出版物标准首行缩进排版</p>
                </div>
                <input type="checkbox" :checked="prefs.indent" @click.stop="prefs.indent = !prefs.indent" />
              </div>

              <div class="toggle-option-card" @click="prefs.wakeLock = !prefs.wakeLock; prefs.wakeLock ? requestWakeLock() : releaseWakeLock()">
                <div>
                  <strong>沉浸阅读屏幕常亮</strong>
                  <p>阅读中保持手机屏幕常亮，防止锁屏熄灭</p>
                </div>
                <input type="checkbox" :checked="prefs.wakeLock" @click.stop="prefs.wakeLock = !prefs.wakeLock; prefs.wakeLock ? requestWakeLock() : releaseWakeLock()" />
              </div>

              <!-- Mobile / Responsive Viewport Mode -->
              <div class="toggle-option-card" @click="prefs.adaptiveWidth = !prefs.adaptiveWidth">
                <div class="toggle-info">
                  <strong>手机全宽自适应排版</strong>
                  <p>针对小屏移动端自适应铺展，消除两侧白边</p>
                </div>
                <input type="checkbox" :checked="prefs.adaptiveWidth" @click.stop="prefs.adaptiveWidth = !prefs.adaptiveWidth" />
              </div>

              <!-- Reading Width -->
              <div v-if="!prefs.adaptiveWidth" class="setting-row">
                <div class="setting-header">
                  <span>版心宽度</span>
                  <span style="font-weight: 600;">{{ prefs.width }}px</span>
                </div>
                <input v-model.number="prefs.width" type="range" :min="settingLimits.width.min" :max="settingLimits.width.max" step="20" />
              </div>

              <!-- Text Alignment -->
              <div class="setting-row">
                <div class="setting-header">
                  <span>排版对齐</span>
                </div>
                <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 8px;">
                  <button
                    type="button"
                    :class="['button', { primary: prefs.textAlign === 'justify' }]"
                    @click="prefs.textAlign = 'justify'"
                  >
                    <AlignJustify :size="15" />
                    两端对齐
                  </button>
                  <button
                    type="button"
                    :class="['button', { primary: prefs.textAlign === 'left' }]"
                    @click="prefs.textAlign = 'left'"
                  >
                    <AlignLeft :size="15" />
                    常规左对齐
                  </button>
                </div>
              </div>

              <!-- Animation Transition Speed -->
              <div class="setting-row">
                <div class="setting-header">
                  <span>
                    <Sparkles :size="13" style="display: inline; margin-right: 4px;" />
                    页面过渡动效
                  </span>
                </div>
                <div class="speed-picker">
                  <div
                    v-for="s in [
                      { id: 'off', label: '关闭', hint: '0ms' },
                      { id: 'fast', label: '快速', hint: '140ms' },
                      { id: 'normal', label: '标准', hint: '240ms' },
                      { id: 'smooth', label: '丝滑', hint: '380ms' }
                    ]"
                    :key="s.id"
                    :class="['speed-option', { selected: prefs.transitionSpeed === s.id }]"
                    @click="prefs.transitionSpeed = (s.id as any)"
                  >
                    <strong>{{ s.label }}</strong>
                    <small style="display: block; opacity: 0.7;">{{ s.hint }}</small>
                  </div>
                </div>
              </div>
            </div>

            <!-- Save Action Button (功能07) -->
            <div style="margin-top: 16px;">
              <button class="primary wide" :disabled="settingsBusy">
                <Save :size="16" />
                {{ settingsBusy ? '正在同步至云端…' : '同步阅读偏好' }}
              </button>
              <p class="fine-print text-center" style="margin-top: 8px;">
                排版调整已在当前设备即时生效，点击可同步至账号云端。
              </p>
            </div>
            </form>

            <!-- Tab 2: Text Purification Rules -->
            <div v-else class="purify-settings-section">
              <p class="fine-print" style="margin-bottom: 12px; line-height: 1.5;">
                正文净化规则：支持关键词和正则表达式过滤，实时对章节内容生效，过滤广告和杂质。
              </p>

              <!-- Rules List -->
              <div class="purify-rules-list">
                <div v-for="rule in purifyRules" :key="rule.id" class="purify-rule-item">
                  <div style="display: flex; align-items: center; gap: 8px; flex: 1; min-width: 0;">
                    <input
                      type="checkbox"
                      v-model="rule.enabled"
                      :id="`rule-${rule.id}`"
                    />
                    <div style="min-width: 0; flex: 1;">
                      <label :for="`rule-${rule.id}`" style="font-weight: 600; font-size: 13px; display: block; cursor: pointer;">
                        {{ rule.name }}
                      </label>
                      <small class="muted" style="display: block; font-family: var(--font-mono); font-size: 11px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis;">
                        {{ rule.isRegex ? '/' + rule.pattern + '/g' : rule.pattern }} → {{ rule.replacement ? rule.replacement : '(空)' }}
                      </small>
                    </div>
                  </div>
                  <button
                    type="button"
                    class="icon-button danger"
                    @click="removePurifyRule(rule.id)"
                    aria-label="删除规则"
                    title="删除规则"
                  >
                    <Trash2 :size="14" />
                  </button>
                </div>
              </div>

              <!-- Add Custom Rule Card -->
              <div class="cache-stat-box" style="margin-top: 16px;">
                <div class="settings-section-title" style="margin-bottom: 8px;">
                  <Plus :size="13" style="display: inline; margin-right: 4px;" />
                  添加自定义净化规则
                </div>
                <div style="display: flex; flex-direction: column; gap: 8px;">
                  <input
                    v-model="newRuleName"
                    placeholder="规则名称（选填，如：去除某某广告）"
                    style="font-size: 13px;"
                  />
                  <input
                    v-model="newRulePattern"
                    placeholder="匹配模式（关键词或正则表达式）*"
                    style="font-size: 13px;"
                  />
                  <input
                    v-model="newRuleReplacement"
                    placeholder="替换内容（选填，默认置空）"
                    style="font-size: 13px;"
                  />
                  <div style="display: flex; align-items: center; justify-content: space-between; margin-top: 4px;">
                    <label style="display: flex; align-items: center; gap: 6px; font-size: 12px; cursor: pointer;">
                      <input type="checkbox" v-model="newRuleIsRegex" />
                      <span>正则表达式模式</span>
                    </label>
                    <button
                      type="button"
                      class="button primary"
                      style="padding: 4px 14px; font-size: 12px;"
                      :disabled="!newRulePattern.trim()"
                      @click="addPurifyRule"
                    >
                      添加规则
                    </button>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- 3. Bookmarks Panel -->
          <template v-else>
            <form class="form-stack bookmark-form" @submit.prevent="addBookmark" style="margin-bottom: 24px;">
              <label>
                在此页添加批注记号
                <textarea v-model="note" rows="2" maxlength="1000" placeholder="写下你的感想或备忘（选填）…" />
              </label>
              <button class="primary wide" :disabled="bookmarkBusy || !content || chapterLoading">
                <BookmarkPlus :size="16" />
                添加当前位置书签 ({{ Math.round(position * 100) }}%)
              </button>
            </form>

            <div v-if="!bookmarks.length" class="empty" style="padding: 24px 0;">
              <BookmarkIcon :size="28" style="opacity: 0.3;" />
              <p class="muted" style="margin-top: 8px;">还没有书签。值得回味的字句，记下来。</p>
            </div>

            <article v-for="mark in bookmarks" :key="mark.id" class="bookmark-item">
              <button
                class="bookmark-jump"
                :disabled="chapterLoading || saving"
                @click="panel = ''; openChapter(mark.chapterId, mark.position)"
              >
                <strong>{{ mark.chapterTitle || chapters.find(c => c.id === mark.chapterId)?.title || ('第 ' + mark.chapterId + ' 章') }}</strong>
                <small style="color: var(--accent); font-weight: 600; display: block; margin: 2px 0 4px;">
                  章内 {{ Math.round(mark.position * 100) }}%
                </small>
                <p v-if="mark.note" style="font-size: 13px; color: var(--muted); margin: 0;">{{ mark.note }}</p>
              </button>
              <button
                class="icon-button danger"
                :disabled="bookmarkBusy"
                @click="removeBookmark(mark)"
                aria-label="删除书签"
                title="删除书签"
              >
                <Trash2 :size="15" />
              </button>
            </article>
          </template>
        </div>
      </aside>

      <!-- Change Source Dialog Modal -->
      <div v-if="changeSourceOpen" class="modal-backdrop" @click="changeSourceOpen = false">
        <div class="modal-card change-source-dialog" @click.stop>
          <div class="modal-header">
            <div>
              <h3>换源中心</h3>
              <p class="muted" style="font-size: 13px; margin-top: 2px;">
                当前书籍：「{{ book?.title }}」· 自动匹配章节进度
              </p>
            </div>
            <button class="icon-button" @click="changeSourceOpen = false" aria-label="关闭换源">
              <X :size="18" />
            </button>
          </div>

          <!-- Search Bar within Modal -->
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

          <div v-if="changeSourceError" class="error message-box" style="margin-bottom: 12px;">
            <span>{{ changeSourceError }}</span>
            <a href="#/sources" class="text-button">前往书源管理</a>
          </div>

          <!-- Source Candidates List -->
          <div class="change-source-body">
            <div v-if="changeSourceLoading" class="empty" style="padding: 40px 0;">
              <LoaderCircle class="spin" :size="28" />
              <p class="muted" style="margin-top: 10px;">正在检索各大书源匹配书籍…</p>
            </div>

            <div v-else-if="!changeSourceResults.length" class="empty" style="padding: 40px 0;">
              <p class="muted">未检索到其他可用书源结果，可修改关键词后重新搜索。</p>
            </div>

            <div v-else class="source-candidates-list">
              <div
                v-for="(item, idx) in changeSourceResults"
                :key="idx"
                :class="['source-card-item', { active: book?.sourceId === item.sourceId }]"
                @click="applyChangeSource(item)"
              >
                <div class="source-badge-tag">
                  {{ sourcesMap[item.sourceId] || '书源 ' + item.sourceId.substring(0, 8) }}
                </div>
                <div class="source-info-main">
                  <div class="source-title-row">
                    <strong>{{ item.title }}</strong>
                    <span v-if="item.author" class="source-author">作者：{{ item.author }}</span>
                    <span v-if="book?.sourceId === item.sourceId" class="current-badge">当前在用</span>
                  </div>
                  <p v-if="item.latestChapter" class="source-latest-chapter" style="font-size: 12px; color: var(--accent); margin: 2px 0 0;">
                    最新：{{ item.latestChapter }}
                  </p>
                  <p class="source-url-text muted">{{ item.url }}</p>
                  <p v-if="item.intro" class="source-intro-text">{{ item.intro }}</p>
                </div>
                <button
                  class="source-action-btn"
                  :disabled="changeSourceSubmitting || book?.sourceId === item.sourceId"
                  @click.stop="applyChangeSource(item)"
                >
                  <LoaderCircle v-if="changeSourceSubmitting" class="spin" :size="14" />
                  <span v-else>{{ book?.sourceId === item.sourceId ? '使用中' : '切换此源' }}</span>
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- Batch Cache Modal -->
      <div v-if="cacheModalOpen" class="modal-backdrop" @click="cacheModalOpen = false">
        <div class="modal-card" style="max-width: 480px;" @click.stop>
          <div class="modal-header">
            <div>
              <h3>批量离线缓存</h3>
              <p class="muted" style="font-size: 13px; margin-top: 2px;">
                《{{ book?.title }}》· 下载正文到本地实现秒读
              </p>
            </div>
            <button class="icon-button" @click="cacheModalOpen = false" aria-label="关闭缓存弹窗">
              <X :size="18" />
            </button>
          </div>

          <div class="modal-content" style="padding: 16px 0;">
            <div class="cache-stat-box" style="margin-bottom: 20px;">
              <div style="display: flex; justify-content: space-between; margin-bottom: 8px; font-size: 13px;">
                <span>已缓存进度</span>
                <strong>
                  {{ cacheProgress.cached }} / {{ cacheProgress.total || chapters.length }} 章
                  ({{ Math.round((cacheProgress.cached / Math.max(1, cacheProgress.total || chapters.length)) * 100) }}%)
                </strong>
              </div>
              <div class="progress-bar-track">
                <div
                  class="progress-bar-fill"
                  :style="{ width: `${Math.min(100, Math.round((cacheProgress.cached / Math.max(1, cacheProgress.total || chapters.length)) * 100))}%` }"
                ></div>
              </div>
            </div>

            <div class="settings-section-title" style="margin-bottom: 10px;">
              从当前阅读位置开始批量缓存
            </div>
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 16px;">
              <button
                type="button"
                class="button secondary"
                :disabled="cachingBusy"
                @click="triggerBatchCache(20)"
              >
                缓存后 20 章
              </button>
              <button
                type="button"
                class="button secondary"
                :disabled="cachingBusy"
                @click="triggerBatchCache(50)"
              >
                缓存后 50 章
              </button>
              <button
                type="button"
                class="button secondary"
                :disabled="cachingBusy"
                @click="triggerBatchCache(100)"
              >
                缓存后 100 章
              </button>
              <button
                type="button"
                class="button primary"
                :disabled="cachingBusy"
                @click="triggerBatchCache(0)"
              >
                缓存全书剩余
              </button>
            </div>

            <div v-if="cachingBusy" class="empty" style="padding: 16px 0;">
              <LoaderCircle class="spin" :size="20" />
              <p style="font-size: 13px; margin-top: 8px;">正在从书源批量下载缓存章节，请稍候…</p>
            </div>
          </div>
        </div>
      </div>

      <!-- Main Text Reading Area: Supports Scroll or Slide Mode -->
      <div
        ref="scroller"
        :class="['reading-scroll', `mode-${prefs.pageMode}`, { 'adaptive-fill': prefs.adaptiveWidth }]"
        tabindex="0"
        aria-label="章节正文，可滚动或滑动翻页阅读"
        @scroll.passive="onScroll"
      >
        <div v-if="loading || chapterLoading" class="empty" role="status" style="padding-top: 160px;">
          <LoaderCircle class="spin" :size="32" />
          <p style="margin-top: 14px; font-family: var(--font-serif); letter-spacing: 1px;">正在铺开书页…</p>
        </div>

        <article
          v-else-if="content"
          class="reading-paper"
          :style="{
            maxWidth: prefs.adaptiveWidth ? '100%' : `${prefs.width}px`,
            fontSize: `${prefs.fontSize}px`,
            lineHeight: prefs.lineHeight,
            fontFamily: currentFont
          }"
        >
          <!-- Subtle Top Chapter Info -->
          <span class="eyebrow">
            {{ book?.title }} · 第 {{ index + 1 }} / {{ chapters.length }} 章
            <span v-if="prefs.pageMode === 'slide' && totalSlidePages > 1" class="slide-page-badge">
              (第 {{ slidePage + 1 }}/{{ totalSlidePages }} 屏)
            </span>
          </span>
          <h1>
            {{ currentChapterName }}
            <span v-if="isFromCache" class="offline-badge">离线已缓存</span>
          </h1>

          <div class="chapter-text" :style="{ textAlign: prefs.textAlign }">
            <p
              v-for="(p, i) in paragraphs"
              :key="i"
              :class="['chapter-paragraph', { 'tts-active-paragraph': ttsActive && ttsIndex === i }]"
            >
              {{ p }}
            </p>
          </div>

          <div v-if="!content.content" class="empty-chapter-fallback">
            <p class="muted">此章节未能解析到正文内容，可能原书源规则变更或站点限流。</p>
            <div class="actions" style="justify-content: center; margin-top: 12px;">
              <button type="button" class="secondary" @click="openChapter(content.id, 0, false)">
                <RefreshCw :size="15" /> 重新获取本章
              </button>
              <button v-if="book?.kind === 'online'" type="button" class="primary" @click="openChangeSourceModal">
                <Shuffle :size="15" /> 一键换源
              </button>
            </div>
          </div>

          <div class="chapter-end">
            <span></span>
            本章完
            <span></span>
          </div>

          <!-- Bottom Navigation Pills -->
          <div class="chapter-navigation">
            <button
              :disabled="index <= 0 || saving"
              @click="openChapter(chapters[index - 1].id)"
              title="快捷键：键盘左方向键 ← 或滑屏向右"
            >
              <ChevronLeft :size="18" />
              上一章
            </button>
            <button
              class="primary"
              :disabled="index < 0 || index >= chapters.length - 1 || saving"
              @click="openChapter(chapters[index + 1].id)"
              title="快捷键：键盘右方向键 → 或滑屏向左"
            >
              下一章
              <ChevronRight :size="18" />
            </button>
          </div>

          <!-- Tap for Next Chapter Prompt (触底翻页) -->
          <div
            v-if="index < chapters.length - 1"
            class="touch-next-chapter-card"
            @click.stop="openChapter(chapters[index + 1].id)"
          >
            <span>点击或继续翻页直达下一章</span>
            <strong>{{ chapters[index + 1]?.title }}</strong>
            <ChevronRight :size="16" />
          </div>

          <p v-if="index === chapters.length - 1" class="fine-print text-center" style="margin-top: 36px;">
            已读至全书最后一章，愿文字常伴身旁。
          </p>
        </article>

        <div v-else-if="!error" class="empty" style="padding-top: 140px;">
          <h2>暂无可读章节</h2>
          <a :href="`#/book/${id(bookId)}`" class="button secondary" style="margin-top: 16px;">返回书籍</a>
        </div>
      </div>

      <!-- Immersive Micro Status Bar (Bottom Edge Indicator when HUD is hidden) -->
      <div
        v-if="content && !showControls && !panel && !changeSourceOpen"
        class="reader-micro-status"
        aria-hidden="true"
      >
        <span class="micro-chapter-title">{{ currentChapterName }}</span>
        <span class="micro-progress-info">
          第 {{ index + 1 }}/{{ chapters.length }} 章
          <template v-if="prefs.pageMode === 'slide'">
            · {{ slidePage + 1 }}/{{ totalSlidePages }} 页
          </template>
          <template v-else>
            · {{ Math.round(position * 100) }}%
          </template>
        </span>
        <span v-if="currentTimeStr" class="micro-time-battery">
          {{ currentTimeStr }}<template v-if="batteryLevel"> · 🔋{{ batteryLevel }}</template>
        </span>
      </div>
    </div>

    <!-- Floating Bottom Control Capsule -->
    <footer :class="['reader-floating-footer', { hidden: !showControls && !panel && !changeSourceOpen }]">
      <!-- Upper: Progress scrub & Chapter jumps -->
      <div class="reader-footer-progress-row">
        <button
          class="footer-step-btn"
          :disabled="index <= 0 || saving"
          @click="openChapter(chapters[index - 1].id)"
          title="上一章"
        >
          <ChevronLeft :size="15" />
          <span>上一章</span>
        </button>

        <div class="reader-progress-group">
          <input
            class="reader-footer-slider"
            type="range"
            min="0"
            max="100"
            step="1"
            :value="Math.round(position * 100)"
            @input="onSliderScrub"
            aria-label="章内进度快速拖动"
          />
          <span class="footer-progress-text">{{ Math.round(position * 100) }}%</span>
        </div>

        <button
          class="footer-step-btn"
          :disabled="index >= chapters.length - 1 || saving"
          @click="openChapter(chapters[index + 1].id)"
          title="下一章"
        >
          <span>下一章</span>
          <ChevronRight :size="15" />
        </button>
      </div>

      <!-- Lower: Action Bar (TOC, Night/Day, Settings, Flip Mode, Change Source, Save) -->
      <div class="reader-footer-nav-row">
        <button
          class="footer-nav-item"
          :class="{ selected: panel === 'toc' }"
          @click="toggle('toc')"
          aria-label="章节目录"
        >
          <List :size="18" />
          <span>目录</span>
        </button>

        <button
          class="footer-nav-item"
          @click="toggleNightMode"
          :aria-label="isNightMode ? '日间模式' : '夜间模式'"
        >
          <Sun v-if="isNightMode" :size="18" />
          <Moon v-else :size="18" />
          <span>{{ isNightMode ? '日间' : '夜间' }}</span>
        </button>

        <button
          class="footer-nav-item"
          :class="{ selected: panel === 'settings' }"
          @click="toggle('settings')"
          aria-label="阅读设置"
        >
          <Settings2 :size="18" />
          <span>设置</span>
        </button>

        <button
          class="footer-nav-item"
          @click="prefs.pageMode = prefs.pageMode === 'slide' ? 'scroll' : 'slide'"
          :aria-label="prefs.pageMode === 'slide' ? '横滑翻页' : '竖卷阅读'"
        >
          <Columns v-if="prefs.pageMode === 'slide'" :size="18" />
          <Scroll v-else :size="18" />
          <span>{{ prefs.pageMode === 'slide' ? '横滑' : '竖滚' }}</span>
        </button>

        <button
          v-if="book?.kind === 'online'"
          class="footer-nav-item"
          :class="{ selected: changeSourceOpen }"
          @click="openChangeSourceModal"
          aria-label="换源"
        >
          <Shuffle :size="18" />
          <span>换源</span>
        </button>

        <button
          class="footer-nav-item"
          :disabled="saving || !dirty || conflict || chapterLoading"
          @click="saveProgress"
          aria-label="立即同步进度"
          :title="dirty ? '将当前阅读进度立即同步至云端' : '当前设备与云端进度已保持一致'"
        >
          <Save :size="18" />
          <span>{{ saving ? '同步中' : (dirty ? '立即同步' : '已同步') }}</span>
        </button>
      </div>
    </footer>
    <!-- Floating TTS Audio Player Capsule -->
    <div v-if="ttsActive" class="reader-tts-capsule">
      <div class="tts-capsule-info">
        <Headphones :size="15" />
        <span>{{ ttsIndex + 1 }}/{{ paragraphs.length }}段</span>
      </div>
      <div class="tts-capsule-controls">
        <button class="icon-button" @click="prevTtsParagraph" title="上一段" aria-label="上一段">
          <ChevronLeft :size="15" />
        </button>
        <button
          class="icon-button tts-play-btn"
          @click="ttsPlaying ? pauseTts() : resumeTts()"
          :title="ttsPlaying ? '暂停朗读' : '继续朗读'"
          :aria-label="ttsPlaying ? '暂停朗读' : '继续朗读'"
        >
          <Pause v-if="ttsPlaying" :size="15" />
          <Play v-else :size="15" />
        </button>
        <button class="icon-button" @click="nextTtsParagraph" title="下一段" aria-label="下一段">
          <ChevronRight :size="15" />
        </button>
        <button class="icon-button" @click="stopTts" title="停止听书" aria-label="停止听书">
          <Square :size="13" />
        </button>
      </div>
      <div class="tts-speed-selector">
        <button
          v-for="r in [0.8, 1.0, 1.2, 1.5]"
          :key="r"
          :class="['tts-rate-tag', { active: ttsRate === r }]"
          @click="setTtsRate(r)"
        >
          {{ r }}x
        </button>
      </div>
    </div>
  </div>
</template>