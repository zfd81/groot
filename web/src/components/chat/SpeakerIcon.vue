<!-- 「喇叭」图标：扬声器机身 + 两道声波。Element Plus 图标库只有耳机/话筒，
     无扬声器图形，故用内联 SVG；置于 el-icon 内使用，尺寸与颜色继承自 el-icon。
     图形占满 viewBox（边距约 2），与 Element Plus 图标的视觉大小一致。
     muted 为 true 时去掉声波并加一个叉，表示关闭。 -->
<script setup lang="ts">
defineProps<{ muted?: boolean }>()
</script>

<template>
  <svg
    viewBox="0 0 24 24"
    width="1em"
    height="1em"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <!-- 机身：左侧矩形 + 右侧梯形喇叭口，纵向 3~21，横向 2~13 -->
    <path d="M2 9v6a1 1 0 0 0 1 1h3.2l5.9 4.6a.6.6 0 0 0 .9-.47V3.87a.6.6 0 0 0-.9-.47L6.2 8H3a1 1 0 0 0-1 1z" />
    <template v-if="!muted">
      <path d="M16.5 8.5a5 5 0 0 1 0 7" />
      <path d="M19.5 5.5a9.2 9.2 0 0 1 0 13" />
    </template>
    <template v-else>
      <path d="M16.5 9.5l5 5" />
      <path d="M21.5 9.5l-5 5" />
    </template>
  </svg>
</template>
