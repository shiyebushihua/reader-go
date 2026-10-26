<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import {
  Users,
  Library,
  Globe2,
  Activity,
  Plus,
  Download,
  Upload,
  RefreshCw,
  Trash2,
  FileJson,
  ShieldCheck,
  X,
  Search,
  ChevronLeft,
  ChevronRight,
  CheckSquare,
  Square,
  Layers,
  Filter,
  ToggleLeft,
  ToggleRight,
  Sparkles
} from 'lucide-vue-next'
import { api, id, message, date } from '../api'
import type { User, Source, Audit, SearchResult } from '../types'
import { usernamePattern, usernameError, passwordError } from '../validation'

const props = defineProps<{ currentUser: User; sourcesOnly?: boolean }>()
const tab = ref(props.sourcesOnly ? 'sources' : 'overview')
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const overview = ref<{ users: number; books: number; sources: number; version: string } | null>(null)
const users = ref<User[]>([])
const sources = ref<Source[]>([])
const audit = ref<Audit[]>([])

// 操作日志的分页状态
const auditPage = ref(1)
const auditPageSize = ref(5)
const auditTotal = ref(0)
const auditTotalPages = ref(1)
// 书源管理的分页、过滤与批量管理状态
const sourcePage = ref(1)
const sourcePageSize = ref(5)
const sourceTotal = ref(0)
const sourceTotalPages = ref(1)
const sourceGroup = ref('')
const sourceQuery = ref('')
const sourceEnabledFilter = ref('all')
const sourceGroups = ref<string[]>([])
const selectedSourceIds = ref<string[]>([])

const createOpen = ref(false)
const newUser = reactive({ username: '', password: '', role: 'user' })
const resetId = ref('')
const resetPassword = ref('')

const importOpen = ref(false)
const raw = ref('')
const overwrite = ref(false)
const remoteUrl = ref('')
const editing = ref<Source | null>(null)
const editName = ref('')
const editGroup = ref('')
const editEnabled = ref(true)
const editRule = ref('')
const checkId = ref('')
const checkKeyword = ref('')
const checkResults = ref<SearchResult[] | null>(null)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (tab.value === 'overview') {
      overview.value = await api('/admin/overview')
    }
    if (tab.value === 'users') {
      users.value = (await api<User[]>('/admin/users')) || []
    }
    if (tab.value === 'sources') {
      await loadSources()
    }
    if (tab.value === 'audit') {
      await loadAudit()
    }
  } catch (e) {
    error.value = message(e)
  } finally {
    loading.value = false
  }
}

async function loadSources() {
  try {
    // 异步拉取书源分组
    api<string[]>('/sources/groups').then(res => {
      sourceGroups.value = res || []
    }).catch(() => {})

    let metaInfo: any = null
    const queryParams = new URLSearchParams()
    queryParams.set('page', String(sourcePage.value))
    queryParams.set('pageSize', String(sourcePageSize.value))
    if (sourceGroup.value) queryParams.set('group', sourceGroup.value)
    if (sourceQuery.value.trim()) queryParams.set('query', sourceQuery.value.trim())
    if (sourceEnabledFilter.value !== 'all') queryParams.set('enabled', sourceEnabledFilter.value)

    const data = await api<Source[]>(`/sources?${queryParams.toString()}`, 'GET', undefined, (m) => {
      metaInfo = m
    })
    sources.value = data || []
    if (metaInfo) {
      sourceTotal.value = metaInfo.total ?? sources.value.length
      sourceTotalPages.value = metaInfo.totalPages ?? Math.max(1, Math.ceil(sourceTotal.value / sourcePageSize.value))
      sourcePage.value = metaInfo.page ?? sourcePage.value
    } else {
      sourceTotal.value = sources.value.length
      sourceTotalPages.value = 1
    }
  } catch (e) {
    error.value = message(e)
  }
}

async function loadAudit() {
  try {
    let metaInfo: any = null
    const data = await api<Audit[]>(
      `/admin/audit?page=${auditPage.value}&pageSize=${auditPageSize.value}`,
      'GET',
      undefined,
      (m) => { metaInfo = m }
    )
    audit.value = data || []
    if (metaInfo) {
      auditTotal.value = metaInfo.total ?? audit.value.length
      auditTotalPages.value = metaInfo.totalPages ?? Math.max(1, Math.ceil(auditTotal.value / auditPageSize.value))
      auditPage.value = metaInfo.page ?? auditPage.value
    } else {
      auditTotal.value = audit.value.length
      auditTotalPages.value = 1
    }
  } catch (e) {
    error.value = message(e)
  }
}

function changeAuditPage(p: number) {
  if (p < 1 || p > auditTotalPages.value || p === auditPage.value) return
  auditPage.value = p
  loadAudit()
}

function handleAuditSizeChange() {
  auditPage.value = 1
  loadAudit()
}

async function deduplicateSources() {
  if (!confirm('确定执行智能书源去重吗？系统将自动清理 URL 规范化后重复以及同名冗余的历史书源，并保留最新版本。')) return
  await action(async () => {
    const res = await api<{ cleaned?: number; remaining?: number; removed?: number; retained?: number; total?: number }>('/sources/deduplicate', 'POST')
    const removed = res.cleaned ?? res.removed ?? 0
    const retained = res.remaining ?? res.retained ?? 0
    const total = res.total ?? (removed + retained)
    sourcePage.value = 1
    await loadSources()
    notice.value = `智能去重完成：已自动清理 ${removed} 个重复书源，当前保留 ${retained} 个唯一有效书源（扫描前共 ${total} 个）。`
  }, '')
}
function switchTab(value: string) {
  if (busy.value) return
  tab.value = value
  notice.value = ''
  error.value = ''
  createOpen.value = false
  editing.value = null
  checkId.value = ''
  selectedSourceIds.value = []
  load()
}

async function action(task: () => Promise<void>, success: string) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await task()
    if (success) notice.value = success
  } catch (e) {
    error.value = message(e)
  } finally {
    busy.value = false
  }
}

async function createUser() {
  error.value = usernameError(newUser.username) || passwordError(newUser.password)
  if (error.value) return
  await action(async () => {
    await api('/admin/users', 'POST', { ...newUser })
    newUser.username = ''
    newUser.password = ''
    createOpen.value = false
    await load()
  }, '账号已创建，用户首次登录需要修改密码。')
}

async function updateUser(user: User, patch: Partial<User>) {
  await action(async () => {
    await api(`/admin/users/${id(user.id)}`, 'PATCH', patch)
    await load()
  }, '账号已更新。')
}

async function reset() {
  error.value = passwordError(resetPassword.value)
  if (error.value) return
  await action(async () => {
    await api(`/admin/users/${id(resetId.value)}/reset-password`, 'POST', { password: resetPassword.value })
    resetId.value = ''
    resetPassword.value = ''
    await load()
  }, '密码已重置。请通过安全渠道告知用户临时密码。')
}

function parse(text: string) {
  let value: unknown
  try {
    value = JSON.parse(text)
  } catch {
    throw new Error('JSON 格式不正确，请检查引号、逗号与括号。')
  }
  if (!value || typeof value !== 'object') throw new Error('书源必须是 JSON 对象或对象数组。')
  return value
}

async function importSources() {
  await action(async () => {
    const result = await api<{ imported: number; updated: number; skipped: number; total: number }>(
      '/sources/import',
      'POST',
      { sources: parse(raw.value), overwrite: overwrite.value }
    )
    sourcePage.value = 1
    await loadSources()
    raw.value = ''
    importOpen.value = false
    notice.value = `导入处理完成：新增 ${result.imported} 个，更新 ${result.updated} 个，跳过 ${result.skipped} 个（共检测到 ${result.total} 条）。`
  }, '')
}

async function previewRemote() {
  await action(async () => {
    const data = await api<unknown>('/sources/remote', 'POST', { url: remoteUrl.value })
    raw.value = JSON.stringify(data, null, 2)
  }, '已获取远程 JSON 预览。请检查内容，再确认导入。')
}

async function readFile(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  try {
    raw.value = await file.text()
    parse(raw.value)
    notice.value = '文件已读取，请检查 JSON 后确认导入。'
    error.value = ''
  } catch (e) {
    error.value = message(e)
  }
}

function edit(source: Source) {
  editing.value = source
  editName.value = source.name
  editGroup.value = source.group
  editEnabled.value = source.enabled
  editRule.value = JSON.stringify(source.rule || {}, null, 2)
  error.value = ''
  notice.value = ''
}

async function saveSource() {
  if (!editing.value) return
  await action(async () => {
    const rule = parse(editRule.value)
    if (Array.isArray(rule)) throw new Error('单个书源规则必须为 JSON 对象。')
    await api(`/sources/${id(editing.value!.id)}`, 'PUT', {
      name: editName.value,
      group: editGroup.value,
      enabled: editEnabled.value,
      rule
    })
    editing.value = null
    await loadSources()
  }, '书源规则已保存。')
}

async function toggleSource(source: Source) {
  await action(async () => {
    await api(`/sources/${id(source.id)}`, 'PUT', {
      name: source.name,
      group: source.group,
      enabled: !source.enabled,
      rule: source.rule || {}
    })
    await loadSources()
  }, '书源状态已更新。')
}

async function deleteSource(source: Source) {
  if (!confirm(`确定删除书源“${source.name}”吗？`)) return
  await action(async () => {
    await api(`/sources/${id(source.id)}`, 'DELETE')
    if (sources.value.length === 1 && sourcePage.value > 1) {
      sourcePage.value--
    }
    await loadSources()
  }, '书源已删除。')
}

async function batchAction(actionType: 'enable' | 'disable' | 'delete') {
  if (!selectedSourceIds.value.length) return
  const actionText = actionType === 'enable' ? '启用' : actionType === 'disable' ? '停用' : '删除'
  if (actionType === 'delete' && !confirm(`确定要批量删除所选的 ${selectedSourceIds.value.length} 个书源吗？`)) {
    return
  }
  await action(async () => {
    const res = await api<{ affected: number }>('/sources/batch', 'POST', {
      ids: selectedSourceIds.value,
      action: actionType
    })
    selectedSourceIds.value = []
    await loadSources()
    notice.value = `批量${actionText}成功，已更新 ${res.affected} 个书源。`
  }, '')
}

function toggleSelectAllCurrentPage() {
  const currentIds = sources.value.map(s => s.id)
  const isAllSelected = currentIds.every(sid => selectedSourceIds.value.includes(sid))
  if (isAllSelected) {
    selectedSourceIds.value = selectedSourceIds.value.filter(sid => !currentIds.includes(sid))
  } else {
    const next = new Set([...selectedSourceIds.value, ...currentIds])
    selectedSourceIds.value = Array.from(next)
  }
}

function isCurrentPageAllSelected() {
  if (!sources.value.length) return false
  return sources.value.every(s => selectedSourceIds.value.includes(s.id))
}

function changePage(p: number) {
  if (p < 1 || p > sourceTotalPages.value || p === sourcePage.value) return
  sourcePage.value = p
  loadSources()
}

function handleFilterChange() {
  sourcePage.value = 1
  loadSources()
}

async function exportSources() {
  await action(async () => {
    const queryParams = new URLSearchParams()
    if (sourceGroup.value) queryParams.set('group', sourceGroup.value)
    if (sourceQuery.value.trim()) queryParams.set('query', sourceQuery.value.trim())
    if (sourceEnabledFilter.value !== 'all') queryParams.set('enabled', sourceEnabledFilter.value)
    const qs = queryParams.toString()
    const allSources = (await api<Source[]>(qs ? `/sources?${qs}` : '/sources')) || sources.value
    const payload = allSources.map(s => s.rule || {})
    const url = URL.createObjectURL(new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `readergo-sources-${payload.length}.json`
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }, '书源 JSON 已导出，请妥善保管规则。')
}

async function checkSource() {
  await action(async () => {
    checkResults.value =
      (await api<SearchResult[]>(`/sources/${id(checkId.value)}/check`, 'POST', {
        keyword: checkKeyword.value
      })) || []
  }, '检查请求已完成。无结果并不代表书源一定失效。')
}

onMounted(load)
</script>

<template>
  <header class="page-header">
    <div>
      <span class="eyebrow">{{ props.sourcesOnly ? 'PERSONAL BOOK SOURCES' : 'A WELL-KEPT READING ROOM' }}</span>
      <h1>{{ props.sourcesOnly ? '我的书源' : '管理控制台' }}</h1>
      <p>{{ props.sourcesOnly ? '独立维护属于你自己的书源规则，不同账号之间互不干扰。' : '照料好这个空间，让每一次阅读都安心。' }}</p>
    </div>
    <span v-if="!props.sourcesOnly" class="admin-badge"><ShieldCheck :size="16" />管理员专属</span>
  </header>

  <div v-if="!props.sourcesOnly" class="admin-tabs" aria-label="管理分区">
    <button
      v-for="item in [
        { id: 'overview', label: '空间概览', icon: Activity },
        { id: 'users', label: '用户管理', icon: Users },
        { id: 'sources', label: '我的书源', icon: Globe2 },
        { id: 'audit', label: '操作日志', icon: FileJson }
      ]"
      :key="item.id"
      :class="{ selected: tab === item.id }"
      :aria-pressed="tab === item.id"
      :disabled="busy"
      @click="switchTab(item.id)"
    >
      <component :is="item.icon" :size="17" />
      {{ item.label }}
    </button>
  </div>

  <div v-if="error" class="error" role="alert">
    {{ error }}
    <button class="text-button" :disabled="busy" @click="load">刷新数据</button>
  </div>
  <div v-if="notice" class="success" role="status">{{ notice }}</div>
  <div v-if="loading && tab !== 'sources'" class="empty" role="status">正在读取管理数据…</div>

  <template v-else>
    <!-- 空间概览 -->
    <section v-if="tab === 'overview'">
      <div v-if="overview" class="stats-grid">
        <article class="panel stat">
          <span><Users />空间用户</span>
          <strong>{{ overview.users }}</strong>
          <small>已创建的阅读账号</small>
        </article>
        <article class="panel stat">
          <span><Library />馆藏书籍</span>
          <strong>{{ overview.books }}</strong>
          <small>所有用户的藏书</small>
        </article>
        <article class="panel stat">
          <span><Globe2 />接入书源</span>
          <strong>{{ overview.sources }}</strong>
          <small>已导入的书源配置</small>
        </article>
      </div>
      <section v-if="overview" class="panel system-card">
        <span class="round-icon"><ShieldCheck :size="26" /></span>
        <div>
          <h2>简单、私密、有序</h2>
          <p>账号由管理员创建；书源由每位用户各自导入与独立使用，互不干扰。请留意操作日志。</p>
          <span class="tag">ReaderGo · {{ overview.version }}</span>
        </div>
      </section>
      <div class="admin-shortcuts">
        <button @click="switchTab('users')">
          <Users :size="21" />
          <strong>管理阅读账号</strong>
          <span>创建账号、调整角色与重置密码</span>
        </button>
        <button @click="switchTab('sources')">
          <Globe2 :size="21" />
          <strong>维护书源</strong>
          <span>导入规则、分页管理与批量治理</span>
        </button>
      </div>
    </section>

    <!-- 用户管理 -->
    <section v-if="tab === 'users'">
      <div class="section-heading">
        <h2>阅读账号 <span class="count">{{ users.length }}</span></h2>
        <button class="primary" :disabled="busy" @click="createOpen = !createOpen">
          <Plus :size="17" />创建用户
        </button>
      </div>

      <form v-if="createOpen" class="panel admin-form" @submit.prevent="createUser">
        <h3>创建新用户</h3>
        <div class="form-grid">
          <label>
            用户名
            <input
              v-model.trim="newUser.username"
              required
              minlength="3"
              maxlength="32"
              :pattern="usernamePattern"
              title="3–32 位字母、数字、下划线或连字符"
              autocomplete="off"
            />
          </label>
          <label>
            临时密码
            <input
              v-model="newUser.password"
              required
              maxlength="72"
              type="password"
              autocomplete="new-password"
            />
          </label>
          <label>
            角色
            <select v-model="newUser.role">
              <option value="user">阅读者</option>
              <option value="admin">管理员</option>
            </select>
          </label>
        </div>
        <p class="fine-print">密码须为 10–72 字节（中文等字符占多个字节）。新账号首次登录须修改密码。</p>
        <div class="actions">
          <button class="primary" :disabled="busy">创建账号</button>
          <button type="button" @click="createOpen = false" :disabled="busy">取消</button>
        </div>
      </form>

      <form v-if="resetId" class="panel admin-form" @submit.prevent="reset">
        <h3>重置 {{ users.find(u => u.id === resetId)?.username }} 的密码</h3>
        <label>
          新临时密码
          <input
            v-model="resetPassword"
            type="password"
            required
            maxlength="72"
            autocomplete="new-password"
          />
        </label>
        <div class="actions">
          <button class="primary" :disabled="busy">确认重置</button>
          <button type="button" :disabled="busy" @click="resetId = ''; resetPassword = ''">取消</button>
        </div>
      </form>

      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>用户</th>
              <th>角色</th>
              <th>状态</th>
              <th>创建时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id">
              <td>
                <strong>{{ u.username }}</strong>
                <small v-if="u.id === props.currentUser.id" class="self-label">当前账号</small>
                <small v-if="u.mustChangePassword">待修改密码</small>
              </td>
              <td>
                <select
                  :value="u.role"
                  :disabled="busy || u.id === props.currentUser.id"
                  :aria-label="`${u.username} 的角色`"
                  @change="updateUser(u, { role: ($event.target as HTMLSelectElement).value as User['role'] })"
                >
                  <option value="user">阅读者</option>
                  <option value="admin">管理员</option>
                </select>
              </td>
              <td>
                <span :class="['status-dot', u.status === 'active' ? 'enabled' : 'disabled']">
                  {{ u.status === 'active' ? '正常' : '已停用' }}
                </span>
              </td>
              <td>{{ date(u.createdAt) }}</td>
              <td>
                <div class="actions">
                  <button
                    class="text-button"
                    :disabled="busy || u.id === props.currentUser.id"
                    @click="updateUser(u, { status: u.status === 'active' ? 'disabled' : 'active' })"
                  >
                    {{ u.status === 'active' ? '停用' : '启用' }}
                  </button>
                  <button
                    class="text-button"
                    :disabled="busy || u.id === props.currentUser.id"
                    @click="resetId = u.id; resetPassword = ''"
                  >
                    重置密码
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="fine-print">为避免误操作，当前账号的角色、状态和密码请通过其他管理员维护；个人密码请前往“修改密码”。服务器保护最后一位有效管理员。</p>
      <div v-if="!users.length" class="empty">暂无用户数据。</div>
    </section>

    <!-- 书源管理（现代化分页 + 过滤 + 批量操作） -->
    <section v-if="tab === 'sources'">
      <div class="section-heading">
        <div>
          <h2>书源管理 <span class="count">{{ sourceTotal }}</span></h2>
          <small class="muted">已接入书源支持按分组筛选、全局去重覆盖与分页呈现</small>
        </div>
        <div class="actions">
          <button class="secondary" :disabled="busy" @click="deduplicateSources" title="自动规范化 URL 并清理同域名/同名冗余书源">
            <Sparkles :size="16" />智能去重
          </button>
          <button :disabled="busy || !sources.length" @click="exportSources">
            <Download :size="16" />导出书源
          </button>
          <button class="primary" :disabled="busy" @click="importOpen = !importOpen">
            <Plus :size="17" />导入书源
          </button>
        </div>
      </div>

      <!-- 筛选与治理工具栏 -->
      <div class="sources-filter-bar">
        <div class="filter-left">
          <div class="search-input compact">
            <Search :size="16" />
            <input
              v-model="sourceQuery"
              placeholder="搜索书源名称或域名…"
              @keyup.enter="handleFilterChange"
            />
          </div>
          <select v-model="sourceGroup" @change="handleFilterChange" aria-label="书源分组">
            <option value="">全部分组 ({{ sourceGroups.length }})</option>
            <option value="__none__">未分组</option>
            <option v-for="g in sourceGroups" :key="g" :value="g">{{ g }}</option>
          </select>
          <select v-model="sourceEnabledFilter" @change="handleFilterChange" aria-label="书源状态">
            <option value="all">全部状态</option>
            <option value="true">仅启用</option>
            <option value="false">仅停用</option>
          </select>
          <button class="secondary compact-btn" @click="handleFilterChange" :disabled="busy">
            筛选
          </button>
        </div>

        <div class="filter-right" v-if="sources.length">
          <button
            type="button"
            class="text-button compact-btn"
            @click="toggleSelectAllCurrentPage"
          >
            <component :is="isCurrentPageAllSelected() ? CheckSquare : Square" :size="14" />
            {{ isCurrentPageAllSelected() ? '取消本页全选' : '本页全选' }}
          </button>
          <template v-if="selectedSourceIds.length > 0">
            <span class="muted" style="font-size: 13px;">已选 {{ selectedSourceIds.length }} 项：</span>
            <button class="text-button compact-btn" :disabled="busy" @click="batchAction('enable')">
              批量启用
            </button>
            <button class="text-button compact-btn" :disabled="busy" @click="batchAction('disable')">
              批量停用
            </button>
            <button class="text-button compact-btn danger" :disabled="busy" @click="batchAction('delete')">
              批量删除
            </button>
          </template>
        </div>
      </div>

      <!-- 导入书源面板 -->
      <div v-if="importOpen" class="panel admin-form">
        <div class="section-heading">
          <h3>导入书源 JSON (自动规范化 URL 并去重)</h3>
          <button class="icon-button" :disabled="busy" @click="importOpen = false" aria-label="关闭导入">
            <X :size="18" />
          </button>
        </div>
        <form class="inline-form" @submit.prevent="previewRemote">
          <label>
            远程订阅地址
            <input v-model.trim="remoteUrl" type="url" required placeholder="https://…" />
          </label>
          <button :disabled="busy || !remoteUrl">获取预览</button>
        </form>
        <p class="fine-print">远程地址只获取预览，不自动导入。服务器已配置 URL 规范化与重复自动识别机制。</p>
        <label class="file-label">
          <Upload :size="16" />从 JSON 文件读取
          <input type="file" accept=".json,application/json" :disabled="busy" @change="readFile" />
        </label>
        <form class="form-stack" @submit.prevent="importSources">
          <label>
            原始 JSON（单个对象或数组）
            <textarea
              class="code-input"
              v-model="raw"
              rows="10"
              required
              spellcheck="false"
              placeholder="粘贴合法的书源 JSON"
            />
          </label>
          <label class="checkbox-label">
            <input v-model="overwrite" type="checkbox" />覆盖已存在的同源配置（若不勾选则自动去重跳过重复项）
          </label>
          <button class="primary" :disabled="busy || !raw.trim()">
            {{ busy ? '处理中…' : '确认导入' }}
          </button>
        </form>
      </div>

      <!-- 编辑书源弹窗 -->
      <form v-if="editing" class="panel admin-form form-stack" @submit.prevent="saveSource">
        <div class="section-heading">
          <h3>编辑书源 · {{ editing.name }}</h3>
          <button type="button" class="icon-button" :disabled="busy" @click="editing = null" aria-label="关闭编辑">
            <X :size="18" />
          </button>
        </div>
        <div class="form-grid">
          <label>
            书源名称
            <input v-model="editName" required />
          </label>
          <label>
            分组
            <input v-model="editGroup" />
          </label>
          <label class="checkbox-label">
            <input v-model="editEnabled" type="checkbox" />启用此书源
          </label>
        </div>
        <label>
          原始规则 JSON
          <textarea class="code-input" v-model="editRule" rows="14" required spellcheck="false" />
        </label>
        <button class="primary" :disabled="busy">保存书源</button>
      </form>

      <!-- 搜索检查弹窗 -->
      <div v-if="checkId" class="panel admin-form">
        <form @submit.prevent="checkSource">
          <div class="section-heading">
            <h3>检查 · {{ sources.find(s => s.id === checkId)?.name }}</h3>
            <button type="button" class="icon-button" :disabled="busy" @click="checkId = ''" aria-label="关闭检查">
              <X :size="18" />
            </button>
          </div>
          <div class="inline-form">
            <label>
              测试关键词
              <input v-model.trim="checkKeyword" required placeholder="输入一个书名" />
            </label>
            <button :disabled="busy || !checkKeyword">
              <Search :size="16" />{{ busy ? '检查中…' : '运行搜索检查' }}
            </button>
          </div>
        </form>
        <div v-if="checkResults">
          <p>返回 {{ checkResults.length }} 条结果。</p>
          <ul class="check-results">
            <li v-for="r in checkResults" :key="r.url">
              <strong>{{ r.title }}</strong>
              <span>{{ r.author || '佚名' }}</span>
              <small>{{ r.url }}</small>
            </li>
          </ul>
        </div>
      </div>

      <!-- 书源卡片列表 -->
      <div class="source-list" v-if="sources.length">
        <article
          v-for="source in sources"
          :key="source.id"
          :class="['panel source-row', { 'source-selected': selectedSourceIds.includes(source.id) }]"
        >
          <label class="source-select-box" :aria-label="`选择 ${source.name}`">
            <input
              type="checkbox"
              :value="source.id"
              v-model="selectedSourceIds"
            />
          </label>
          <span class="source-icon"><Globe2 :size="22" /></span>
          <div class="source-info">
            <h3>
              {{ source.name }}
              <span class="tag" v-if="source.group">{{ source.group }}</span>
            </h3>
            <p>{{ source.url || '未提供网址' }}</p>
            <small>更新于 {{ date(source.updatedAt) }}</small>
          </div>
          <div class="actions">
            <button
              :class="['status-toggle', { enabled: source.enabled }]"
              :aria-pressed="source.enabled"
              :disabled="busy"
              @click="toggleSource(source)"
            >
              {{ source.enabled ? '已启用' : '已停用' }}
            </button>
            <button :disabled="busy" @click="edit(source)">编辑规则</button>
            <button :disabled="busy" @click="checkId = source.id; checkResults = null; checkKeyword = ''">
              检查
            </button>
            <button
              class="icon-button danger"
              :disabled="busy"
              @click="deleteSource(source)"
              :aria-label="`删除 ${source.name}`"
            >
              <Trash2 :size="16" />
            </button>
          </div>
        </article>
      </div>

      <!-- 分页栏组件 -->
      <div class="pagination-bar" v-if="sourceTotal > 0">
        <div class="pagination-info">
          <span>第 {{ sourcePage }} / {{ sourceTotalPages }} 页 (共 {{ sourceTotal }} 个书源)</span>
          <select v-model="sourcePageSize" @change="handleFilterChange" aria-label="书源每页条数" class="page-size-select">
            <option :value="3">3 条/页</option>
            <option :value="5">5 条/页</option>
            <option :value="10">10 条/页</option>
            <option :value="20">20 条/页</option>
          </select>
        </div>
        <div class="pagination-controls">
          <button
            class="secondary pagination-btn"
            :disabled="sourcePage <= 1 || busy"
            @click="changePage(sourcePage - 1)"
          >
            <ChevronLeft :size="16" /> 上一页
          </button>
          <span class="pagination-current">{{ sourcePage }}</span>
          <button
            class="secondary pagination-btn"
            :disabled="sourcePage >= sourceTotalPages || busy"
            @click="changePage(sourcePage + 1)"
          >
            下一页 <ChevronRight :size="16" />
          </button>
        </div>
      </div>

      <section v-if="!sources.length && !loading" class="empty">
        <Globe2 :size="36" />
        <h2>未找到符合条件的书源</h2>
        <p v-if="sourceQuery || sourceGroup || sourceEnabledFilter !== 'all'">
          尝试清除筛选条件或重置搜索词。
        </p>
        <p v-else>导入合法的书源 JSON，检查可用性后再启用。</p>
        <div class="actions">
          <button v-if="sourceQuery || sourceGroup || sourceEnabledFilter !== 'all'" class="secondary" @click="sourceQuery = ''; sourceGroup = ''; sourceEnabledFilter = 'all'; handleFilterChange()">
            重置筛选
          </button>
          <button class="primary" @click="importOpen = true">
            <Plus :size="17" />导入首个书源
          </button>
        </div>
      </section>
    </section>

    <!-- 操作日志 -->
    <section v-if="tab === 'audit'">
      <div class="section-heading">
        <div>
          <h2>操作日志</h2>
          <p class="muted">记录管理操作，帮助你了解空间里的变化。</p>
        </div>
        <button :disabled="busy" @click="load">
          <RefreshCw :size="16" />刷新
        </button>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>时间</th>
              <th>操作者</th>
              <th>操作</th>
              <th>目标</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in audit" :key="entry.id">
              <td>{{ date(entry.createdAt) }}</td>
              <td>{{ entry.actor || '系统' }}</td>
              <td><code>{{ entry.action }}</code></td>
              <td>{{ entry.target || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-if="!audit.length" class="empty">
        <Activity :size="32" />
        <h2>暂无操作日志</h2>
        <p>管理操作将在这里留下记录。</p>
      </div>
      <!-- 操作日志分页栏 -->
      <div class="pagination-bar" v-if="auditTotal > 0">
        <div class="pagination-info">
          <span>第 {{ auditPage }} / {{ auditTotalPages }} 页 (共 {{ auditTotal }} 条记录)</span>
          <select v-model="auditPageSize" @change="handleAuditSizeChange" aria-label="日志每页条数" class="page-size-select">
            <option :value="3">3 条/页</option>
            <option :value="5">5 条/页</option>
            <option :value="10">10 条/页</option>
            <option :value="20">20 条/页</option>
          </select>
        </div>
        <div class="pagination-controls">
          <button
            class="secondary pagination-btn"
            :disabled="auditPage <= 1 || busy"
            @click="changeAuditPage(auditPage - 1)"
          >
            <ChevronLeft :size="16" /> 上一页
          </button>
          <span class="pagination-current">{{ auditPage }}</span>
          <button
            class="secondary pagination-btn"
            :disabled="auditPage >= auditTotalPages || busy"
            @click="changeAuditPage(auditPage + 1)"
          >
            下一页 <ChevronRight :size="16" />
          </button>
        </div>
      </div>
      <p class="fine-print">记录服务器管理与重要操作流水，支持翻页回溯与审计。</p>
    </section>
  </template>
</template>