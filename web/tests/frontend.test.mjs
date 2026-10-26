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
  assert.match(app, /我的书源/)
  assert.match(adminView, /\/sources\/import/)
  assert.match(adminView, /\/sources\/batch/)
  assert.match(adminView, /\/sources\/deduplicate/)
})
