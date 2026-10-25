export interface User { id:string; username:string; role:'admin'|'user'; status:'active'|'disabled'; mustChangePassword:boolean; createdAt:string }
export interface Source { id:string; name:string; url:string; group:string; enabled:boolean; rule?:Record<string,unknown>; updatedAt:string }
export interface Progress { chapterId:string; position:number; version:number }
export interface Book { id:string; title:string; author:string; cover:string; intro:string; kind:'online'|'txt'|'epub'; group:string; sourceId:string; url:string; chapterCount:number; progress:Progress|null; createdAt:string }
export interface Chapter { id:string; title:string; index:number }
export interface ChapterContent { id:string; title:string; content:string; format:'text'|'html' }
export interface Bookmark { id:string; chapterId:string; position:number; note:string }
export interface SearchResult { sourceId:string; url:string; title:string; author:string; cover:string; intro:string }
export interface Settings { theme:string; fontSize:number; lineHeight:number; fontFamily:string; width:number }
export interface Audit { id:string; actor:string; action:string; target:string; createdAt:string }
