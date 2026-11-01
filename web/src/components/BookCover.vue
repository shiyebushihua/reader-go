<script setup lang="ts">
import { BookOpen } from 'lucide-vue-next'
defineProps<{title:string;kind?:string;index?:number}>()
</script>
<template><div :class="['book-cover',`cover-${(index||0)%5}`]"><span class="cover-kind-tag">{{kind==='online'?'网络':'本地'}}</span><strong class="cover-title">{{title}}</strong><span class="cover-accent-line"></span></div></template>
