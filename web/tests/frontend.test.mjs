import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import ts from 'typescript'
import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc'

async function moduleFromTS(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8')
  const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } })
  return import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`)
}
const validation = await moduleFromTS('../src/validation.ts')
const client = await moduleFromTS('../src/api.ts')

test('username accepts exact server ASCII alphabet and 3–32 boundaries', () => {
  for (const value of ['abc', 'reader-go_1', 'A'.repeat(32)]) assert.equal(validation.usernameError(value), '')
  for (const value of ['ab', 'a'.repeat(33), '中文用户', 'reader name', 'user@host']) assert.ok(validation.usernameError(value))
  assert.ok(new RegExp(`^(?:${validation.usernamePattern})$`, 'v').test('reader-go'))
})
test('password validates UTF-8 bytes, not JS character count', () => {
  for (const value of ['a'.repeat(10), 'a'.repeat(72), '中文密码', '中'.repeat(24)]) assert.equal(validation.passwordError(value), '')
  for (const value of ['', 'a'.repeat(9), 'a'.repeat(73), '中'.repeat(25), '😀'.repeat(19)]) assert.ok(validation.passwordError(value))
})
test('all themes and settings boundaries match contract', () => {
  for (const theme of ['light','dark','sepia']) {
    assert.equal(validation.settingsError({theme,fontSize:12,lineHeight:1.2,width:320}), '')
    assert.equal(validation.settingsError({theme,fontSize:40,lineHeight:3,width:1200}), '')
  }
  for (const patch of [{theme:'other'},{fontSize:11},{fontSize:41},{lineHeight:1.1},{lineHeight:3.1},{width:319},{width:1201},{width:NaN}]) assert.ok(validation.settingsError({theme:'sepia',fontSize:20,lineHeight:1.9,width:760,...patch}))
})
test('CSRF cookie parsing handles encoding and malformed escape sequences', () => {
  globalThis.document = { cookie:'other=x; reader_csrf=token%2Bvalue; reader_session=secret' }
  assert.equal(client.csrfToken(), 'token+value')
  document.cookie='reader_csrf=%ZZ'; assert.equal(client.csrfToken(), '%ZZ')
  document.cookie='reader_session=secret'; assert.equal(client.csrfToken(), '')
})
test('JSON mutations send same-origin credentials and readable CSRF header', async () => {
  globalThis.document = { cookie:'reader_csrf=test-token' }
  globalThis.fetch = async (url, options) => {
    assert.equal(url, '/api/v1/settings'); assert.equal(options.credentials, 'same-origin')
    assert.equal(options.headers['X-CSRF-Token'], 'test-token')
    assert.equal(options.headers['Content-Type'], 'application/json')
    assert.deepEqual(JSON.parse(options.body), {theme:'sepia'})
    return new Response(JSON.stringify({data:{saved:true}}))
  }
  assert.deepEqual(await client.api('/settings','PUT',{theme:'sepia'}), {saved:true})
})
test('multipart upload preserves browser boundary and file field', async () => {
  const form = new FormData(); form.append('file', new Blob(['第一章\n正文']), 'book.txt')
  globalThis.fetch = async (_url, options) => {
    assert.equal(options.body, form); assert.equal(options.headers['Content-Type'], undefined)
    assert.equal(options.headers['X-CSRF-Token'], 'test-token')
    return new Response(JSON.stringify({data:{id:'book-id'}}))
  }
  assert.equal((await client.api('/imports','POST',form)).id, 'book-id')
})
test('search metadata warnings are not discarded', async () => {
  const warnings=[{sourceId:'source-1',message:'搜索已超时'}]
  globalThis.fetch=async()=>new Response(JSON.stringify({data:[],meta:{warnings}}))
  let meta
  assert.deepEqual(await client.api('/search','POST',{keyword:'书'}, value=>meta=value), [])
  assert.deepEqual(meta.warnings,warnings)
})
test('GET does not attach mutation-only headers', async () => {
  globalThis.fetch=async(_url, options)=>{
    assert.equal(options.headers['X-CSRF-Token'], undefined)
    assert.equal(options.headers['Content-Type'], undefined)
    return new Response(JSON.stringify({data:[]}))
  }
  assert.deepEqual(await client.api('/books'), [])
})
test('progress conflict preserves status/code/message for explicit resolution', async () => {
  globalThis.fetch=async()=>new Response(JSON.stringify({error:{code:'CONFLICT',message:'进度冲突'}}),{status:409})
  await assert.rejects(client.api('/books/1/progress','PUT',{}), e=>e instanceof client.ApiError && e.status===409 && e.code==='CONFLICT' && e.message==='进度冲突')
})
test('expired authenticated requests dispatch session event; login errors do not', async () => {
  let expired=0; globalThis.window={dispatchEvent(event){assert.equal(event.type,'session-expired'); expired++}}
  globalThis.fetch=async()=>new Response(JSON.stringify({error:{message:'请登录'}}),{status:401})
  await assert.rejects(client.api('/books')); assert.equal(expired,1)
  await assert.rejects(client.api('/auth/login','POST',{})); assert.equal(expired,1)
})
test('network and non-JSON errors remain human-readable', async () => {
  globalThis.fetch=async()=>{throw new TypeError('fetch failed')}
  await assert.rejects(client.api('/books'), e=>e.status===0 && e.code==='NETWORK')
  globalThis.fetch=async()=>new Response('Bad gateway',{status:502})
  await assert.rejects(client.api('/books'), e=>e.status===502 && e.message.includes('502'))
})
test('all existing Vue components compile, without unsafe HTML or unresolved placeholder', async () => {
  const root=new URL('../src/',import.meta.url)
  const files=['App.vue',...(await readdir(new URL('components/',root))).filter(f=>f.endsWith('.vue')).map(f=>'components/'+f)]
  for (const filename of files) {
    const source=await readFile(new URL(filename,root),'utf8')
    const {descriptor,errors}=parse(source,{filename})
    assert.deepEqual(errors,[],filename)
    const script=compileScript(descriptor,{id:filename})
    const result=compileTemplate({source:descriptor.template.content,filename,id:filename,compilerOptions:{bindingMetadata:script.bindings}})
    assert.deepEqual(result.errors,[],filename)
    assert.ok(!source.includes('v-html'),filename)
    assert.ok(!source.includes('BookOpenPlaceholder'),filename)
  }
})
test('reader restores only after chapter is rendered and protects optimistic saves', async () => {
  const source=await readFile(new URL('../src/components/ReaderView.vue',import.meta.url),'utf8')
  assert.match(source,/chapterLoading\.value=false;loading\.value=false;await restore\(ratio\)/)
  assert.match(source,/e\.status===409/)
  assert.match(source,/version:version\.value/)
  assert.match(source,/defineExpose\(\{canLeave\}\)/)
})
test('responsive styles include focus and reduced motion, without global overflow clipping', async () => {
  const css=await readFile(new URL('../src/style.css',import.meta.url),'utf8')
  assert.match(css,/:focus-visible/);assert.match(css,/prefers-reduced-motion:reduce/)
  assert.match(css,/@media\(max-width:480px\)/);assert.match(css,/minmax\(0,1fr\)/)
  assert.doesNotMatch(css,/transition:\s*all|overflow-x:\s*hidden/)
})
test('offline caching uses browser-native indexedDB reader_offline_v1 and provides required operations', async () => {
  const offlineSource = await readFile(new URL('../src/offline.ts', import.meta.url), 'utf8')
  assert.match(offlineSource, /reader_offline_v1/)
  assert.match(offlineSource, /openOfflineDB/)
  assert.match(offlineSource, /saveBook/)
  assert.match(offlineSource, /getBook/)
  assert.match(offlineSource, /saveChapters/)
  assert.match(offlineSource, /getChapters/)
  assert.match(offlineSource, /saveChapterContent/)
  assert.match(offlineSource, /getChapterContent/)
  assert.match(offlineSource, /getCachedChapterIds/)
  assert.match(offlineSource, /batchCacheChapters/)
  assert.match(offlineSource, /removeBookCache/)
  assert.match(offlineSource, /books/)
  assert.match(offlineSource, /chapters/)
  assert.match(offlineSource, /chapter_contents/)
  assert.match(offlineSource, /indexedDB\.open/)
})
test('offline badges and cache operations are integrated without v-html or blank screen issues', async () => {
  const bookView = await readFile(new URL('../src/components/BookView.vue', import.meta.url), 'utf8')
  assert.match(bookView, /离线缓存就绪/)
  assert.match(bookView, /离线缓存后续章节/)
  assert.match(bookView, /离线全书/)
  assert.match(bookView, /getBook/)
  assert.match(bookView, /getChapters/)

  const readerView = await readFile(new URL('../src/components/ReaderView.vue', import.meta.url), 'utf8')
  assert.match(readerView, /离线已缓存/)
  assert.match(readerView, /getChapterContent/)
  assert.match(readerView, /saveChapterContent/)
  assert.match(readerView, /prefetchNextChapter/)
})

test('mobile authentication has a single column and readable non-zooming inputs', async () => {
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  const mobile = css.slice(css.indexOf('@media (max-width: 768px)'))
  assert.match(mobile, /\.auth-layout\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/)
  assert.match(mobile, /\.auth-form input\s*\{[^}]*font-size:\s*16px/)
  assert.match(mobile, /\.auth-quote,\s*\.auth-footer\s*\{\s*display:\s*none/)
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(app, /登录成功但浏览器未能保持会话/)
})

test('slide columns use the viewport stride without vw width or column gaps', async () => {
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  const reader = await readFile(new URL('../src/components/ReaderView.vue', import.meta.url), 'utf8')
  assert.match(css, /\.reading-scroll\.mode-slide \.reading-paper\s*\{[^}]*column-count:\s*1;[^}]*column-gap:\s*0;/)
  assert.doesNotMatch(css, /column-width:\s*100vw/)
  assert.match(reader, /--slide-height/)
  assert.match(reader, /const scrollTarget = p \* scroller.value.clientWidth/)
  assert.match(css, /\.continue-card \.button\s*\{[^}]*min-height:\s*44px/)
})

test('mobile shelf keeps resume action and filters in compact rows', async () => {
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(css, /\.continue-card \.continue-action\s*\{[^}]*grid-column: 2;[^}]*grid-row: 1 \/ 4/)
  assert.match(css, /\.library-toolbar \.icon-button\s*\{[^}]*grid-column: 3;[^}]*grid-row: 2/)
  assert.match(css, /\.library-toolbar \.tabs\s*\{[^}]*overflow-x: auto/)
  assert.match(app, /<details[^>]*class="mobile-account-menu"/)
  assert.match(app, /<summary aria-label="账户菜单">/)
})

test('shelf supports direct book removal with offline cache cleanup and preserves pagination', async () => {
  const library = await readFile(new URL('../src/components/LibraryView.vue', import.meta.url), 'utf8')
  assert.match(library, /const pageSize = ref\(5\)/)
  assert.match(library, /removeBookCache/)
  assert.match(library, /book-remove-btn/)
  assert.match(library, /@click\.stop\.prevent="removeBook\(book\)"/)
})

test('horizontal slide pagination prevents mid-page overlap and scroll step advances a full page', async () => {
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  const reader = await readFile(new URL('../src/components/ReaderView.vue', import.meta.url), 'utf8')
  assert.match(css, /\.reading-scroll\.mode-slide\s*\{[^}]*touch-action:\s*pan-y/)
  assert.match(reader, /slideAnimating/)
  assert.match(reader, /getScrollPageStep/)
  assert.doesNotMatch(reader, /scroller\.value\.clientHeight \* 0\.8/)
})

test('every user has access to personal book sources in main navigation and user-scoped endpoints', async () => {
  const app = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  const adminView = await readFile(new URL('../src/components/AdminView.vue', import.meta.url), 'utf8')
  assert.match(app, /href="#\/sources"/)
  assert.match(app, /书源管理/)
  assert.match(app, /我的书源/)
  assert.match(adminView, /\/sources\/import/)
  assert.match(adminView, /\/sources\/batch/)
  assert.match(adminView, /\/sources\/deduplicate/)
  assert.match(adminView, /\/sources\/probe/)
  assert.match(adminView, /source-control-col/)
})

test('search view uses result-centric relevance ranking, collapsible scope card, and mobile import layout', async () => {
  const searchView = await readFile(new URL('../src/components/SearchView.vue', import.meta.url), 'utf8')
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')
  assert.match(searchView, /search-input-box/)
  assert.match(searchView, /scope-collapse-card/)
  assert.match(searchView, /scoreResult/)
  assert.match(searchView, /sortResultsByRelevance/)
  assert.doesNotMatch(searchView, /大家都在搜/)
  assert.doesNotMatch(searchView, /部分书源未能完成搜索/)
  assert.match(css, /\.import-grid\s*\{[^}]*grid-template-columns:\s*minmax\(0,\s*1fr\)/)
  assert.match(css, /\.file-picker input\[type="file"\]/)
})

test('reader, book detail, search, and shelf support human-centric reading flow, TOC sorting, and source recovery', async () => {
  const readerView = await readFile(new URL('../src/components/ReaderView.vue', import.meta.url), 'utf8')
  const bookView = await readFile(new URL('../src/components/BookView.vue', import.meta.url), 'utf8')
  const searchView = await readFile(new URL('../src/components/SearchView.vue', import.meta.url), 'utf8')
  const libraryView = await readFile(new URL('../src/components/LibraryView.vue', import.meta.url), 'utf8')
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')

  assert.match(readerView, /scrollToActiveTocItem/)
  assert.match(readerView, /tocReversed/)
  assert.match(readerView, /retryFailedChapter/)
  assert.match(readerView, /reader-micro-status/)
  assert.match(bookView, /openChangeSourceModal/)
  assert.match(bookView, /scrollToCurrentChapter/)
  assert.match(bookView, /tocReversed/)
  assert.match(searchView, /readNow/)
  assert.match(libraryView, /openReaderDirect/)
  assert.match(css, /\.mode-selector-grid/)
  assert.match(css, /\.toggle-option-card/)
})

test('search cache preserves state across navigation and resets on logout', async () => {
  const { createRenderer, defineComponent, h, KeepAlive, ref, nextTick, onUnmounted } = await import('@vue/runtime-core')
  const appSource = await readFile(new URL('../src/App.vue', import.meta.url), 'utf8')
  assert.match(appSource, /<KeepAlive include="SearchView" :max="1">/)
  assert.match(appSource, /<SearchView v-else-if="parts\[0\]==='search'"\/>/)
  const searchSource = await readFile(new URL('../src/components/SearchView.vue', import.meta.url), 'utf8')
  assert.match(searchSource, /onDeactivated\(\(\) => \{\s*window.removeEventListener\('keydown', handleKeydown\)/)
  assert.match(searchSource, /onUnmounted\(\(\) => \{[\s\S]*?abortCtrl.abort\(\)/)
  const node = () => ({ children: [], parent: null })
  const renderer = createRenderer({
    createElement: node, createText: node, createComment: node,
    setText() {}, setElementText() {}, patchProp() {},
    parentNode: n => n.parent,
    nextSibling: n => n.parent?.children[n.parent.children.indexOf(n) + 1] || null,
    insert(n, parent, anchor = null) {
      if (n.parent) n.parent.children.splice(n.parent.children.indexOf(n), 1)
      n.parent = parent
      const index = anchor ? parent.children.indexOf(anchor) : -1
      if (index < 0) parent.children.push(n); else parent.children.splice(index, 0, n)
    },
    remove(n) { if (n.parent) n.parent.children.splice(n.parent.children.indexOf(n), 1); n.parent = null }
  })
  let mounts = 0, disposals = 0, state
  const Search = defineComponent({ name: 'SearchView', setup() {
    mounts++
    state = ref({ keyword: '', results: [], page: 1, selected: [], expanded: false })
    onUnmounted(() => disposals++)
    return () => h('div', JSON.stringify(state.value))
  } })
  const Library = defineComponent({ name: 'LibraryView', setup: () => () => h('div', 'library') })
  const route = ref('search'), loggedIn = ref(true)
  const app = renderer.createApp({ setup: () => () => loggedIn.value
    ? h(KeepAlive, { include: 'SearchView', max: 1 }, { default: () => h(route.value === 'search' ? Search : Library) })
    : h('div', 'login') })
  app.mount(node())
  const expected = { keyword: '测试小说', results: ['结果一', '结果二'], page: 2, selected: ['source-a'], expanded: true }
  state.value = expected
  route.value = 'library'; await nextTick()
  assert.equal(disposals, 0)
  route.value = 'search'; await nextTick()
  assert.equal(mounts, 1)
  assert.deepEqual(state.value, expected)
  loggedIn.value = false; await nextTick()
  assert.equal(disposals, 1)
  loggedIn.value = true; await nextTick()
  assert.equal(mounts, 2)
  assert.equal(state.value.keyword, '')
  app.unmount()
})

test('library shelf prevents white-screen flash with module-level cache and non-blocking background refresh', async () => {
  const librarySource = await readFile(new URL('../src/components/LibraryView.vue', import.meta.url), 'utf8')
  const css = await readFile(new URL('../src/style.css', import.meta.url), 'utf8')

  // Verify module-level cache to ensure instantaneous rendering on tab return
  assert.match(librarySource, /let cachedBooks:\s*Book\[\]\s*=\s*\[\]/)
  assert.match(librarySource, /const books = ref<Book\[\]>\(cachedBooks\)/)
  assert.match(librarySource, /const loading = ref\(cachedBooks\.length === 0\)/)

  // Verify skeleton placeholder is hidden when cached books exist
  assert.match(librarySource, /v-if="loading && !books\.length"/)

  // Verify continue card does not vanish during background load
  assert.match(librarySource, /v-if="continuing && \(!loading \|\| books\.length\)"/)

  // Verify book cards do not force opacity 0 enter animation
  assert.doesNotMatch(css, /\.book-card\s*\{[^}]*animation:\s*enter/)
})
