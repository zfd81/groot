<script setup lang="ts">
import { ref, nextTick, onMounted } from 'vue'
// ElMessageBox 不在 unplugin 自动导入范围内，需显式引入（含样式）
import { ElMessageBox } from 'element-plus'
import 'element-plus/es/components/message-box/style/css'
import { MoreFilled } from '@element-plus/icons-vue'
import { api, ApiError } from '../../api/client'
import type { ApiKeyInfo, ApiKeysResp, ApiKeyCreateResp, ApiKeyTokenResp } from '../../api/types'

const { t } = useI18n()

const keys = ref<ApiKeyInfo[]>([])
const loading = ref(false)

// 行内创建表单：点「创建」在列表末尾追加一张表单卡片（API Key 创建后
// 不可编辑，故无编辑态）。formCardEl 用于展开后滚动到可见。
const formVisible = ref(false)
const creating = ref(false)
const form = ref({ name: '', expires_in: '1y', permissions: ['all'] as string[] })
const formCardEl = ref<HTMLElement | null>(null)

const expiresOptions = ['1d', '7d', '1mo', '6mo', '1y', '10y'] as const
const permissionOptions = ['chat', 'status', 'detail', 'history', 'session', 'schedule', 'all'] as const

// 行内查看面板：openKeyId 为当前展开的 Key id（空串 = 未展开），
// 同一时刻最多展开一个，且与创建表单互斥。处于 v-for 内，用函数 ref。
// 面板只展示创建时填写的内容（名称/有效期/权限），不展示 Key 本身。
const openKeyId = ref('')
const viewPanelEl = ref<HTMLElement | null>(null)
// 正在请求 token 的 key id：请求期间忽略重复命令（下拉菜单项无 loading 态）
const viewing = ref('')

function fmtTime(ms: number): string {
  return new Date(ms).toLocaleString()
}

// 按后端 expiresInOptions 的 AddDate 日历规则，从创建时间推算某档位的过期时间：
// 天数用 setDate、月数用 setMonth、年数用 setFullYear（与 Go AddDate 一致，
// 均含月末溢出归一化，如 1/31 + 1mo = 3/2 或 3/3）。纯函数。
function addExpiry(createdMs: number, option: string): number {
  const d = new Date(createdMs)
  switch (option) {
    case '1d':
      d.setDate(d.getDate() + 1)
      break
    case '7d':
      d.setDate(d.getDate() + 7)
      break
    case '1mo':
      d.setMonth(d.getMonth() + 1)
      break
    case '6mo':
      d.setMonth(d.getMonth() + 6)
      break
    case '1y':
      d.setFullYear(d.getFullYear() + 1)
      break
    case '10y':
      d.setFullYear(d.getFullYear() + 10)
      break
  }
  return d.getTime()
}

// 反推创建时选择的有效期档位：数据里只有 created_at/expires_at，对六个枚举
// 逐个用 addExpiry 推算并与 expires_at 毫秒值精确比对（后端创建时按整秒
// AddDate，正常可精确命中）。时区/夏令时差异导致无法命中时返回 null。纯函数。
function inferValidity(k: ApiKeyInfo): string | null {
  for (const o of expiresOptions) {
    if (addExpiry(k.created_at, o) === k.expires_at) return o
  }
  return null
}

function notifyError(e: unknown) {
  const message = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e)
  ElNotification.error({ title: t('apikeys.title'), message })
}

async function load() {
  loading.value = true
  try {
    const resp = await api.get<ApiKeysResp>('/web/apikeys')
    keys.value = resp.keys || []
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

// 面板展开后滚动到可见
async function revealEl(getEl: () => HTMLElement | null) {
  await nextTick()
  getEl()?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
}

function closeView() {
  openKeyId.value = ''
}

function openCreate() {
  closeView() // 查看面板与创建表单互斥
  form.value = { name: '', expires_in: '1y', permissions: ['all'] }
  formVisible.value = true
  void revealEl(() => formCardEl.value)
}

function closeForm() {
  formVisible.value = false
}

async function handleCreate() {
  if (!form.value.name.trim()) {
    ElNotification.warning({ title: t('apikeys.title'), message: t('apikeys.nameRequired') })
    return
  }
  if (!form.value.permissions.length) {
    ElNotification.warning({ title: t('apikeys.title'), message: t('apikeys.permissionsRequired') })
    return
  }
  creating.value = true
  try {
    const resp = await api.post<ApiKeyCreateResp>('/web/apikeys', {
      name: form.value.name.trim(),
      expires_in: form.value.expires_in,
      permissions: form.value.permissions,
    })
    closeForm()
    await load()
    // 自动展开新建 Key 的查看面板；完整 Key 通过菜单「复制 Key」获取
    openKeyId.value = resp.id
    void revealEl(() => viewPanelEl.value)
  } catch (e) {
    notifyError(e)
  } finally {
    creating.value = false
  }
}

// 请求某个 Key 的完整 token；请求进行中忽略新的命令。失败或忽略返回 null。
async function fetchToken(k: ApiKeyInfo): Promise<string | null> {
  if (viewing.value) return null
  viewing.value = k.id
  try {
    const resp = await api.get<ApiKeyTokenResp>(`/web/apikeys/${encodeURIComponent(k.id)}/token`)
    return resp.token
  } catch (e) {
    notifyError(e)
    return null
  } finally {
    viewing.value = ''
  }
}

// 在指定 Key 的卡片内展开/收起查看面板（与创建表单互斥）。
// 面板只读展示元数据，无需请求 token。
function toggleView(k: ApiKeyInfo) {
  if (openKeyId.value === k.id) {
    closeView()
    return
  }
  formVisible.value = false
  openKeyId.value = k.id
  void revealEl(() => viewPanelEl.value)
}

// 「复制 Key」：取 token 后直接写剪贴板；剪贴板不可用（如非 HTTPS 环境）时
// 弹窗展示完整 Key 供手动选择复制
async function copyKey(k: ApiKeyInfo) {
  const token = await fetchToken(k)
  if (token === null) return
  try {
    await navigator.clipboard.writeText(token)
    ElNotification.success({ title: t('apikeys.title'), message: t('apikeys.copied') })
  } catch {
    await ElMessageBox.alert(token, t('apikeys.tokenTitle', { name: k.name }), {
      confirmButtonText: t('common.close'),
      customClass: 'apikey-token-alert',
    }).catch(() => {})
  }
}

async function handleDelete(k: ApiKeyInfo) {
  try {
    await ElMessageBox.confirm(t('apikeys.deleteConfirm', { name: k.name }), t('apikeys.deleteTitle'), {
      type: 'warning',
      confirmButtonText: t('common.delete'),
      cancelButtonText: t('common.cancel'),
    })
  } catch {
    return // 用户取消
  }
  try {
    await api.delete(`/web/apikeys/${encodeURIComponent(k.id)}`)
    ElNotification.success({ title: t('apikeys.title'), message: t('apikeys.deleted') })
    // 若被删 Key 的查看面板正展开，一并收起
    if (openKeyId.value === k.id) closeView()
    await load()
  } catch (e) {
    notifyError(e)
  }
}

// "···" 菜单命令分发
function handleMenuCommand(k: ApiKeyInfo, command: string | number | object) {
  switch (command) {
    case 'view':
      toggleView(k)
      break
    case 'copy':
      void copyKey(k)
      break
    case 'delete':
      void handleDelete(k)
      break
  }
}

onMounted(() => void load())
</script>

<template>
  <div v-loading="loading">
    <div class="label-desc panel-desc">{{ t('apikeys.desc') }}</div>
    <div class="panel-toolbar">
      <el-button type="primary" size="small" @click="openCreate">{{ t('apikeys.create') }}</el-button>
    </div>

    <div v-for="k in keys" :key="k.id" class="list-item">
      <div class="item-header">
        <span class="key-name" :class="{ 'is-off': k.expired }">{{ k.name }}</span>
        <el-tag size="small" :type="k.expired ? 'danger' : 'success'" effect="light" class="status-tag">
          {{ k.expired ? t('apikeys.expired') : t('apikeys.valid') }}
        </el-tag>
        <div class="item-actions">
          <el-dropdown trigger="click" @command="(cmd: string | number | object) => handleMenuCommand(k, cmd)">
            <el-button size="small" text :icon="MoreFilled" />
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="view">{{ t('apikeys.view') }}</el-dropdown-item>
                <el-dropdown-item command="copy">{{ t('apikeys.copyKey') }}</el-dropdown-item>
                <el-dropdown-item command="delete" divided class="menu-danger">
                  {{ t('common.delete') }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
      <div class="item-meta">
        <span>{{ t('apikeys.createdAt') }}: {{ fmtTime(k.created_at) }}</span>
        <span>{{ t('apikeys.expiresAt') }}: {{ fmtTime(k.expires_at) }}</span>
      </div>

      <!-- 行内查看面板：与创建表单同一套控件与布局，仅控件禁用（只读）。
           处于 v-for 内，字符串 ref 会被收集为数组，故用函数 ref。 -->
      <div v-if="openKeyId === k.id" :ref="(el) => (viewPanelEl = el as HTMLElement | null)"
        class="form-panel">
        <div class="form-title">{{ k.name }}</div>
        <el-form label-position="top">
          <el-form-item :label="t('apikeys.name')">
            <el-input :model-value="k.name" disabled />
          </el-form-item>
          <el-form-item :label="t('apikeys.expiresIn')">
            <el-select :model-value="inferValidity(k) ?? undefined" disabled style="width: 100%">
              <el-option v-for="o in expiresOptions" :key="o" :value="o" :label="t(`apikeys.expires_${o}`)" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('apikeys.permissions')">
            <el-select :model-value="k.permissions" multiple disabled style="width: 100%">
              <el-option v-for="p in permissionOptions" :key="p" :value="p" :label="p" />
            </el-select>
          </el-form-item>
        </el-form>
        <div class="form-footer">
          <el-button @click="closeView">{{ t('common.close') }}</el-button>
          <el-button type="primary" @click="copyKey(k)">{{ t('apikeys.copyKey') }}</el-button>
        </div>
      </div>
    </div>

    <!-- 行内创建表单：列表末尾追加一张卡片，内嵌灰底表单面板（同 ModelsPanel） -->
    <div v-if="formVisible" class="list-item">
      <div ref="formCardEl" class="form-panel">
        <div class="form-title">{{ t('apikeys.createTitle') }}</div>
        <el-form label-position="top" @submit.prevent>
          <el-form-item :label="t('apikeys.name')" required>
            <el-input v-model="form.name" :placeholder="t('apikeys.namePlaceholder')" maxlength="64" />
          </el-form-item>
          <el-form-item :label="t('apikeys.expiresIn')">
            <el-select v-model="form.expires_in" style="width: 100%">
              <el-option v-for="o in expiresOptions" :key="o" :value="o" :label="t(`apikeys.expires_${o}`)" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('apikeys.permissions')" required>
            <el-select v-model="form.permissions" multiple style="width: 100%">
              <el-option v-for="p in permissionOptions" :key="p" :value="p" :label="p" />
            </el-select>
          </el-form-item>
        </el-form>
        <div class="form-footer">
          <el-button @click="closeForm">{{ t('common.cancel') }}</el-button>
          <el-button type="primary" :loading="creating" @click="handleCreate">{{ t('apikeys.create') }}</el-button>
        </div>
      </div>
    </div>

    <el-empty v-if="!loading && !keys.length && !formVisible" :description="t('apikeys.empty')" :image-size="60" />
  </div>
</template>

<style scoped>
.panel-desc {
  margin-bottom: 8px;
}

.panel-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.label-desc {
  font-size: 0.82em;
  opacity: 0.6;
}

.list-item {
  padding: 16px 20px;
  border: 1px solid var(--el-border-color-light, rgba(127, 127, 127, 0.2));
  border-radius: 12px;
  margin-bottom: 12px;
}

.item-header {
  display: flex;
  align-items: center;
}

.item-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 8px;
}

.key-name {
  font-weight: 600;
}

.key-name.is-off {
  opacity: 0.5;
}

.status-tag {
  margin-left: 8px;
  flex-shrink: 0;
}

/* 卡片副信息行：权限 tag + 创建/过期时间，紧凑小字 */
.item-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 16px;
  margin-top: 6px;
  font-size: 0.85em;
  opacity: 0.65;
}

/* 行内面板：嵌在卡片内部，用填充底色与卡片本体区分
   （深浅色主题下 fill-color 自动适配） */
.form-panel {
  background: var(--el-fill-color-light, rgba(127, 127, 127, 0.08));
  border-radius: 10px;
  padding: 16px 20px;
}

/* 查看面板挂在副信息行之后，需要上间距（同 ModelsPanel 头部行之于表单面板） */
.item-meta+.form-panel {
  margin-top: 14px;
}

.form-title {
  font-weight: 600;
  margin-bottom: 12px;
}

.form-footer {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}

/* el-button 相邻默认带 margin-left，与 gap 叠加会不均匀，统一交给 gap */
.form-footer .el-button+.el-button {
  margin-left: 0;
}

</style>

<!-- 复制降级弹窗在组件作用域外（挂到 body），用非 scoped 规则：
     JWT 无空格，需 break-all 才能换行完整展示 -->
<style>
.apikey-token-alert .el-message-box__message {
  word-break: break-all;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.85em;
}
</style>
