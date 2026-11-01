export interface User { id:string; username:string; role:'admin'|'user'; status:'active'|'disabled'; mustChangePassword:boolean; createdAt:string }
export interface Source { id:string; name:string; url:string; group:string; enabled:boolean; rule?:Record<string,unknown>; updatedAt:string }
export interface Progress { chapterId:string; position:number; version:number }
export interface Book { id:string; title:string; author:string; cover:string; intro:string; kind:'online'|'txt'|'epub'|'local'; group:string; sourceId:string; sourceName?:string; url:string; chapterCount:number; progress:Progress|null; createdAt:string }
export interface Chapter { id:string; title:string; index:number; cached?:boolean }
export interface ChapterContent { id:string; title:string; content:string; format:'text'|'html' }
export interface Bookmark { id:string; chapterId:string; chapterTitle?:string; position:number; note:string; createdAt?:string }
export interface SearchResult { sourceId:string; sourceName?:string; url:string; title:string; author:string; cover:string; intro:string; latestChapter?:string }
export interface AggregatedWork { workKey:string; title:string; author:string; normalizedAuthor:string; cover:string; intro:string; latestChapter?:string; sources:SearchResult[]; primarySource:SearchResult }
export interface Settings { theme:string; fontSize:number; lineHeight:number; fontFamily:string; width:number }
export interface Audit { id:string; actor:string; action:string; target:string; createdAt:string }
export interface BookSourceCandidate { sourceId:string; sourceName:string; bookUrl:string; title:string; author:string; latestChapter?:string; cover?:string; isCurrent:boolean }
export interface PurifyRule { id:string; name:string; pattern:string; replacement:string; enabled:boolean; isRegex?:boolean }
export interface BookCacheStatus { bookId:string; totalChapters:number; cachedChapters:number }
