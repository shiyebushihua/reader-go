<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { BookOpen, Library, Search, Upload, Globe2, ShieldCheck, LogOut, KeyRound, ArrowRight, AlertCircle, LoaderCircle } from 'lucide-vue-next'
import { api, ApiError, message } from './api'
import type { User } from './types'
import { usernamePattern, usernameError, passwordError } from './validation'
import LibraryView from './components/LibraryView.vue'
import SearchView from './components/SearchView.vue'
import BookView from './components/BookView.vue'
import ReaderView from './components/ReaderView.vue'
import AdminView from './components/AdminView.vue'
const user=ref<User|null>(null), starting=ref(true), setup=ref(false), startupError=ref(''), error=ref(''), notice=ref(''), busy=ref(false)
const appVersion=ref('0.0.9'), adminUnlocked=ref(sessionStorage.getItem('reader_admin_unlocked')==='true')
let versionClicks: number[] = []
function onVersionClick(e?: KeyboardEvent | MouseEvent){if(e && 'repeat' in e && (e as KeyboardEvent).repeat)return;const now=Date.now();versionClicks=versionClicks.filter(t=>now-t<2000);versionClicks.push(now);if(versionClicks.length>=5){versionClicks=[];if(user.value?.role==='admin'){adminUnlocked.value=true;sessionStorage.setItem('reader_admin_unlocked','true');notice.value='已解锁管理控制台权限'}else if(user.value){error.value='当前账号非管理员，无权开启管理控制台'}}}
const username=ref(''), password=ref(''), setupToken=ref(''), currentPassword=ref(''), newPassword=ref(''), confirmPassword=ref('')
const route=ref(location.hash.slice(1)||'/library'), reader=ref<InstanceType<typeof ReaderView>|null>(null)
const parts=computed(()=>route.value.split('/').filter(Boolean)), isReader=computed(()=>parts.value[0]==='read'), changingPassword=computed(()=>user.value?.mustChangePassword || parts.value[0]==='password')
let routing=false
async function syncRoute(){ if(routing)return; const next=location.hash.slice(1)||'/library'; if(next===route.value)return; if(reader.value && !await reader.value.canLeave()){ routing=true; location.hash=route.value; setTimeout(()=>routing=false,0); return } route.value=next; error.value=''; notice.value=''; window.scrollTo(0,0) }
function expired(){user.value=null;adminUnlocked.value=false;sessionStorage.removeItem('reader_admin_unlocked');error.value='登录已过期，请重新登录。'}
async function initialize(){starting.value=true;startupError.value='';try{setup.value=(await api<{required:boolean}>('/setup')).required;if(!setup.value){try{user.value=await api<User>('/auth/me')}catch(e){if(!(e instanceof ApiError&&e.status===401))throw e}}try{const sys=await api<{version?:string}>('/system/info');if(sys?.version)appVersion.value=sys.version}catch(_){}}catch(e){startupError.value=message(e)}finally{starting.value=false}}
async function authenticate(){
  error.value=usernameError(username.value)||(setup.value?passwordError(password.value):'')
  if(error.value)return
  busy.value=true;notice.value=''
  try{
    if(setup.value){
      await api('/setup','POST',{username:username.value,password:password.value,setupToken:setupToken.value})
      setup.value=false;setupToken.value='';password.value='';notice.value='管理员已创建，请使用刚设置的密码登录。'
    }else{
      await api('/auth/login','POST',{username:username.value,password:password.value})
      try{user.value=await api<User>('/auth/me')}catch(e){
        if(e instanceof ApiError&&e.status===401)throw new Error('登录成功但浏览器未能保持会话。请确认允许本站 Cookie，并请管理员检查 HTTPS 与安全 Cookie 配置。')
        throw e
      }
      password.value='';location.hash='/library'
    }
  }catch(e){error.value=message(e)}finally{busy.value=false}
}
async function logout(){if(reader.value&&!await reader.value.canLeave())return;busy.value=true;error.value='';try{await api('/auth/logout','POST');user.value=null;adminUnlocked.value=false;sessionStorage.removeItem('reader_admin_unlocked');route.value='/library';location.hash='/library'}catch(e){error.value=message(e)}finally{busy.value=false}}
async function changePassword(){error.value=passwordError(newPassword.value);notice.value='';if(error.value)return;if(newPassword.value!==confirmPassword.value){error.value='两次输入的新密码不一致。';return}busy.value=true;try{await api('/auth/password','PUT',{currentPassword:currentPassword.value,newPassword:newPassword.value});user.value=await api<User>('/auth/me');currentPassword.value='';newPassword.value='';confirmPassword.value='';notice.value='密码已更新。';location.hash='/library'}catch(e){error.value=message(e)}finally{busy.value=false}}
onMounted(()=>{initialize();window.addEventListener('hashchange',syncRoute);window.addEventListener('session-expired',expired)})
onUnmounted(()=>{window.removeEventListener('hashchange',syncRoute);window.removeEventListener('session-expired',expired)})
function skipToMain(){const main=document.getElementById('main-content');main?.focus();main?.scrollIntoView({block:'start'})}
</script>
<template>
  <a class="skip-link" href="#main-content" @click.prevent="skipToMain">跳至主要内容</a>
  <div v-if="starting" class="startup" role="status"><BookOpen :size="40"/><h1>阅间</h1><p><LoaderCircle class="spin"/> 正在开启你的阅读空间…</p></div>
  <div v-else-if="startupError" class="startup"><AlertCircle :size="40"/><h1>暂时无法连接阅间</h1><p class="error" role="alert">{{startupError}}</p><button class="primary" @click="initialize">重新连接</button></div>
  <main v-else-if="!user" id="main-content" tabindex="-1" class="auth-layout">
    <section class="auth-story"><div class="brand-badge-container"><a class="brand" href="#/library"><span class="brand-symbol"><BookOpen/></span><span>阅间 <small>READER GO</small></span></a><span class="app-version-tag" role="button" tabindex="0" :title="'版本号 v'+appVersion+' · 连续点击5次解锁控制台'" @click.stop.prevent="onVersionClick" @keydown.enter.stop.prevent="onVersionClick" @keydown.space.stop.prevent="onVersionClick">v{{appVersion}}</span></div><div class="auth-quote"><span class="eyebrow">A LITTLE ROOM FOR READING</span><h1>书页之间，<br>自有天地。</h1><p>让纷扰停在门外，<br>把这一刻，留给阅读。</p><div class="book-art" aria-hidden="true"><div></div><div></div><div></div><span>閱</span></div></div><p class="auth-footer">你的书架 · 你的节奏 · 你的阅读时光</p></section>
    <section class="auth-form-wrap"><form class="auth-form" @submit.prevent="authenticate"><span class="eyebrow">{{setup?'WELCOME TO READER GO':'YOUR PERSONAL LIBRARY'}}</span><h2>{{setup?'开启你的阅间':'欢迎回来'}}</h2><p class="muted">{{setup?'创建首位管理员，开始搭建私人阅读空间。':'登录，接着读上次未完的故事。'}}</p><div v-if="error" class="error" role="alert">{{error}}</div><div v-if="notice" class="success" role="status">{{notice}}</div><label>用户名<input v-model.trim="username" autocomplete="username" required minlength="3" maxlength="32" :pattern="usernamePattern" title="3–32 位字母、数字、下划线或连字符" placeholder="输入用户名"/></label><label>密码<input v-model="password" type="password" :autocomplete="setup?'new-password':'current-password'" required maxlength="72" placeholder="输入密码"/></label><label v-if="setup">初始化令牌<input v-model="setupToken" type="password" autocomplete="off" required placeholder="READER_SETUP_TOKEN"/><small>由服务器管理员提供，仅首次初始化需要。</small></label><button class="primary wide" :disabled="busy"><LoaderCircle v-if="busy" class="spin"/>{{busy?'请稍候…':setup?'创建管理员':'进入阅间'}}<ArrowRight v-if="!busy" :size="18"/></button><p class="auth-help">{{setup?'密码须为 10–72 字节，请妥善保管管理员凭据。':'此阅读空间不开放注册。需要账号？请联系管理员。'}}</p></form></section>
  </main>
  <div v-else :class="['app-layout',{'reading-layout':isReader&&!changingPassword}]">
    <aside v-if="!isReader||changingPassword" class="sidebar"><div class="brand-badge-container"><a class="brand" href="#/library"><span class="brand-symbol"><BookOpen/></span><span>阅间<small>READER GO</small></span></a><span class="app-version-tag" role="button" tabindex="0" :title="'版本号 v'+appVersion+' · 连续点击5次解锁控制台'" @click.stop.prevent="onVersionClick" @keydown.enter.stop.prevent="onVersionClick" @keydown.space.stop.prevent="onVersionClick">v{{appVersion}}</span></div><p class="nav-caption">我的阅读空间</p><nav aria-label="主要导航"><a href="#/library" :class="{active:parts[0]==='library'}" :aria-current="parts[0]==='library'?'page':undefined"><Library/><span>我的书架</span></a><a href="#/search" :class="{active:parts[0]==='search'}" :aria-current="parts[0]==='search'?'page':undefined"><Search/><span>发现好书</span></a><a href="#/sources" :class="{active:parts[0]==='sources'}" :aria-current="parts[0]==='sources'?'page':undefined" title="我的书源 · 个人书源管理"><Globe2/><span>书源管理</span></a><a href="#/import" :class="{active:parts[0]==='import'}" :aria-current="parts[0]==='import'?'page':undefined"><Upload/><span>导入书籍</span></a><template v-if="user.role==='admin'&&adminUnlocked"><p class="nav-caption">管理空间</p><a href="#/admin" :class="{active:parts[0]==='admin'}" :aria-current="parts[0]==='admin'?'page':undefined"><ShieldCheck/><span>管理控制台</span></a></template></nav><div class="sidebar-bottom"><div class="quiet-note">慢慢读，<br>每一页都算数。</div><div class="account"><span class="avatar">{{user.username.slice(0,1).toUpperCase()}}</span><div><strong>{{user.username}}</strong><small>{{user.role==='admin'?'空间管理员':'阅读者'}}</small></div></div><div class="account-actions"><a href="#/password"><KeyRound :size="16"/>修改密码</a><button :disabled="busy" @click="logout" aria-label="退出登录" title="退出登录"><LogOut :size="17"/></button></div></div></aside>
    <details v-if="!isReader||changingPassword" class="mobile-account-menu">
      <summary aria-label="账户菜单">账户</summary>
      <div class="mobile-account-panel">
        <strong>{{ user.username }}</strong>
        <a href="#/password" @click="($event.currentTarget as HTMLElement).closest('details')?.removeAttribute('open')"><KeyRound :size="16"/>修改密码</a>
        <button :disabled="busy" @click="logout"><LogOut :size="16"/>退出登录</button>
      </div>
    </details>
    <main id="main-content" tabindex="-1" :class="isReader&&!changingPassword?'reader-main':'main-content'">
      <div v-if="error" class="error" role="alert">{{error}}</div><div v-if="notice" class="success" role="status">{{notice}}</div>
      <section v-if="changingPassword" class="narrow"><div class="page-header"><div><span class="eyebrow">ACCOUNT SECURITY</span><h1>更新你的密码</h1><p>{{user.mustChangePassword?'首次登录或密码已被重置，请先设置自己的密码。':'定期更新密码，保护你的私人书架。'}}</p></div></div><form class="panel form-stack" @submit.prevent="changePassword"><label>当前密码<input v-model="currentPassword" type="password" autocomplete="current-password" required/></label><label>新密码<input v-model="newPassword" type="password" autocomplete="new-password" maxlength="72" required/><small>10–72 字节；中文等非 ASCII 字符可能占多个字节。</small></label><label>确认新密码<input v-model="confirmPassword" type="password" autocomplete="new-password" maxlength="72" required/></label><button class="primary" :disabled="busy">{{busy?'保存中…':'更新密码'}}</button></form></section>
      <LibraryView v-else-if="parts[0]==='library'||parts[0]==='import'" :key="parts[0]" :import-mode="parts[0]==='import'"/>
      <SearchView v-else-if="parts[0]==='search'"/>
      <BookView v-else-if="parts[0]==='book'&&parts[1]" :key="parts[1]" :book-id="parts[1]"/>
      <ReaderView v-else-if="parts[0]==='read'&&parts[1]" ref="reader" :key="route" :book-id="parts[1]" :initial-chapter="parts[2]"/>
      <AdminView v-else-if="parts[0]==='sources'||(parts[0]==='admin'&&user.role==='admin'&&adminUnlocked)" :key="parts[0]" :current-user="user" :sources-only="parts[0]==='sources'"/>
      <section v-else class="empty"><BookOpen :size="40"/><h1>这一页还没有内容</h1><p>页面不存在，或你没有访问权限。</p><a class="button primary" href="#/library">回到书架</a></section>
    </main>
  </div>
</template>
