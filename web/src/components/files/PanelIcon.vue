<!-- 「右侧面板」图标：方框 + 右侧分栏填充，模拟 IDE 的 toggle panel 图标。
     Element Plus 图标库无此图形，故用内联 SVG；置于 el-icon 内使用，
     尺寸与颜色继承自 el-icon（1em / currentColor）。 -->
<template>
  <svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" aria-hidden="true">
    <rect
      x="3.2"
      y="4.2"
      width="17.6"
      height="15.6"
      rx="3"
      stroke="currentColor"
      stroke-width="1.7"
    />
    <path d="M14.6 4.2v15.6" stroke="currentColor" stroke-width="1.7" />
    <rect x="16.1" y="6.4" width="3.4" height="11.2" rx="1" fill="currentColor" />
  </svg>
</template>
