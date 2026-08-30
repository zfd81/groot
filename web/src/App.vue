<script setup lang="ts">
import { computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import { useThemeStore } from './stores/theme'
import { useLanguageStore } from './stores/language'
import i18n from './i18n'

// 引用一次主题 store，确保应用启动即建立「明暗 → <html>.dark 类」的同步。
useThemeStore()

// 语言：以 language store 为单一数据源，同步到 vue-i18n 与 Element Plus 组件文案。
const lang = useLanguageStore()
const { locale } = storeToRefs(lang)
const epLocale = computed(() => (locale.value === 'en' ? en : zhCn))
watch(
  locale,
  (v) => {
    i18n.global.locale.value = v
  },
  { immediate: true }
)
</script>

<template>
  <el-config-provider :locale="epLocale">
    <router-view />
  </el-config-provider>
</template>
