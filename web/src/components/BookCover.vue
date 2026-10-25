<script setup lang="ts">
import { BookOpen } from 'lucide-vue-next'
defineProps<{title:string;kind?:string;index?:number}>()
</script>
<template><div :class="['book-cover',`cover-${(index||0)%5}`]"><span class="cover-edition">{{kind==='online'?'ONLINE EDITION':kind?kind.toUpperCase()+' EDITION':'READER GO'}}</span><strong>{{title}}</strong><span class="cover-rule"></span><BookOpen :size="22"/><span class="cover-bottom">阅间 · 藏书</span></div></template>
