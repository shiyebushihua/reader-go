# Initial implementation contract
Module: readergo. Go 1.24+. All JSON success responses {"data":...}; errors {"error":{"code":"...","message":"..."}}. Session cookie reader_session, CSRF cookie reader_csrf (readable); all authenticated mutations send X-CSRF-Token cookie value. Login/bootstrap exempt but same-origin checked. Fetch credentials same-origin.

GET /api/v1/setup -> {required:bool}; POST /api/v1/setup {username,password,setupToken} -> creates first admin only, token from READER_SETUP_TOKEN; no public registration.
POST /api/v1/auth/login {username,password}; GET /api/v1/auth/me -> User; POST /api/v1/auth/logout; PUT /api/v1/auth/password {currentPassword,newPassword}. User {id,username,role:'admin'|'user',status:'active'|'disabled',mustChangePassword:bool,createdAt}.
GET /api/v1/admin/users -> User[]; POST {username,password,role}; PATCH /admin/users/{id} {status?,role?}; POST /admin/users/{id}/reset-password {password}. Last active admin guarded.
GET /api/v1/sources -> Source[] (ordinary users only enabled sanitized metadata; admin raw rule). Source {id,name,url,group,enabled,rule:object,updatedAt}. POST /admin/sources/import {sources:object|array, overwrite:bool} -> {imported,skipped}; PUT /admin/sources/{id} {name,group,enabled,rule}; DELETE /admin/sources/{id}; POST /admin/sources/remote {url} -> preview source JSON only; POST /admin/sources/{id}/check {keyword} -> SearchResult[]. Admin may export via frontend using fetched rules.
POST /api/v1/search {keyword,sourceIds:string[]} -> SearchResult[] synchronous bounded search (first build; no fake SSE). SearchResult {sourceId,url,title,author,cover,intro}. POST /api/v1/books/online {sourceId,url,title,author,cover,intro} -> Book (fetch details and chapters).
GET /api/v1/books -> Book[]; GET /books/{id} -> Book; PATCH /books/{id} {group}; DELETE /books/{id} removes user's book/files. Book {id,title,author,cover,intro,kind:'online'|'txt'|'epub',group,sourceId,url,chapterCount,progress:Progress|null,createdAt}. GET /books/{id}/chapters -> Chapter[] {id,title,index}; GET /books/{id}/chapters/{chapterId} -> {id,title,content:string,format:'text'|'html'}. Content local EPUB normalized safe text initially, no unsafe v-html. POST /books/{id}/refresh -> updated chapter count.
GET /books/{id}/progress -> Progress {chapterId,position:number [0,1],version:number}; PUT {chapterId,position,version} optimistic lock, 409 on conflict. Chapter position ratio first build, explicitly documented limitation.
GET /books/{id}/bookmarks -> Bookmark[] {id,chapterId,position,note}; POST same minus id; DELETE /bookmarks/{id}.
POST /api/v1/imports multipart 'file' TXT/EPUB -> imported Book, directly into shelf first build (no fake preview).
GET /api/v1/settings -> object; PUT /settings {theme,fontSize,lineHeight,fontFamily,width} validated.
GET /api/v1/admin/overview -> {users,books,sources,version}; GET /api/v1/admin/audit -> [{id,actor,action,target,createdAt}]. GET /health/live; GET /health/ready.
No mock data in production. Frontend use API routes only above; unsupported features disabled with explanation, not fake buttons. Errors visible. API list shape arrays, not paged first build (server hard limits). IDs strings.

Engine package internal/engine owns exported:
type Source struct {ID string; Rule json.RawMessage}
type Result struct {SourceID string `json:"sourceId"`; URL string `json:"url"`; Title string `json:"title"`; Author string `json:"author"`; Cover string `json:"cover"`; Intro string `json:"intro"`}
type Chapter struct {Title string; URL string}
type Detail struct {Title,Author,Cover,Intro,URL string; Chapters []Chapter}
func New() *Engine
func (e *Engine) Search(ctx context.Context,s Source,keyword string)([]Result,error)
func (e *Engine) Details(ctx context.Context,s Source,url string)(Detail,error)
func (e *Engine) Content(ctx context.Context,s Source,url string)(string,error)
func (e *Engine) FetchSource(ctx context.Context,url string)(json.RawMessage,error) // SSRF protected
All extraction/network limitations explicitly documented, unit tests no real internet. Private IP blocked by default. Engine no DB access. If interface changes coordinate via own ENGINE_NOTES.md not other module changes.

Importer package internal/importer owns:
type Chapter struct {Title string `json:"title"`; Content string `json:"content"`}
type Book struct {Title string; Author string; Kind string; Chapters []Chapter}
func Parse(ctx context.Context,path string,originalName string)(Book,error)
Read local controlled temp path, TXT UTF8/GBK, EPUB safe normalized text. Limits bounded, errors explicit. No arbitrary unzip to filesystem. Unit tests fixtures temporary t.TempDir. Importer does not own uploads/API/DB.
