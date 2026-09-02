<script setup lang="ts">
import { ref, computed, nextTick, onMounted } from 'vue'
// ElMessageBox 不在 unplugin 自动导入范围内，需显式引入（含样式）
import { ElMessageBox } from 'element-plus'
import 'element-plus/es/components/message-box/style/css'
import { MoreFilled } from '@element-plus/icons-vue'
import { api, ApiError } from '../../api/client'
import { useMetaStore } from '../../stores/meta'
import type { ModelInfo, ModelsResp, ModelForm, ModelTestResp } from '../../api/types'

const { t } = useI18n()
const meta = useMetaStore()

const models = ref<ModelInfo[]>([])
const loading = ref(false)

// 行内表单状态：formVisible 控制展开；formAnchor 为空串表示新建（表单挂在
// 列表末尾），否则为被编辑模型的名称（表单挂在该模型行正下方）。
// editingName 非空表示编辑模式（值为原名称，作为更新请求的路径参数）。
const formVisible = ref(false)
const formAnchor = ref('')
const editingName = ref('')
const saving = ref(false)
const testing = ref(false)
// 表单卡片 DOM，用于展开后滚动到可见
const formCardEl = ref<HTMLElement | null>(null)

function emptyForm(): ModelForm {
  return {
    name: '', model: '', base_url: '', api_key: '',
    max_completion_tokens: 4096, max_context_tokens: 0,
    temperature: 0.7, top_p: 1.0, frequency_penalty: 0, presence_penalty: 0,
    seed: 0, stop: [], thinking: false, enabled: true,
  }
}
const form = ref<ModelForm>(emptyForm())
// stop 在表单里用多行文本编辑，提交时按行拆分
const stopText = ref('')

async function loadModels() {
  loading.value = true
  try {
    const resp = await api.get<ModelsResp>('/web/models')
    models.value = resp.models || []
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

// 列表渲染卡片：每个模型一张卡片；编辑时表单面板嵌在该模型卡片内部（灰底区分），
// 新建时在列表末尾追加一张无头部的卡片承载表单。m 为 null 表示新建卡片。
// 表单在模板中只渲染一次（位于统一的 v-for 内），同一时刻最多显示一个。
type Card = { m: ModelInfo | null }
const cards = computed<Card[]>(() => {
  const out: Card[] = models.value.map((m) => ({ m }))
  // 新建表单卡片挂在列表末尾（空列表时单独显示）
  if (formVisible.value && formAnchor.value === '') out.push({ m: null })
  return out
})

// 该卡片当前是否展开表单面板：新建卡片恒展开；模型卡片看锚点是否命中
function isFormOpen(c: Card): boolean {
  return formVisible.value && (c.m ? formAnchor.value === c.m.name : true)
}

// 表单展开后滚动到可见
async function revealForm() {
  await nextTick()
  formCardEl.value?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
}

function closeForm() {
  formVisible.value = false
  formAnchor.value = ''
  editingName.value = ''
}

function openCreate() {
  editingName.value = ''
  formAnchor.value = ''
  form.value = emptyForm()
  stopText.value = ''
  formVisible.value = true
  void revealForm()
}

function openEdit(m: ModelInfo) {
  // 再次点击同一模型的「编辑」收起表单
  if (formVisible.value && editingName.value === m.name) {
    closeForm()
    return
  }
  editingName.value = m.name
  formAnchor.value = m.name
  form.value = {
    name: m.name, model: m.model, base_url: m.base_url,
    api_key: '', // 编辑时留空 = 不修改（后端语义）
    max_completion_tokens: m.max_completion_tokens,
    max_context_tokens: m.max_context_tokens,
    temperature: m.temperature, top_p: m.top_p,
    frequency_penalty: m.frequency_penalty, presence_penalty: m.presence_penalty,
    seed: m.seed, stop: m.stop || [], thinking: m.thinking, enabled: m.enabled,
  }
  stopText.value = (m.stop || []).join('\n')
  formVisible.value = true
  void revealForm()
}

function buildPayload(): ModelForm {
  return {
    ...form.value,
    stop: stopText.value.split('\n').map((s) => s.trim()).filter((s) => s.length > 0),
  }
}

async function handleSave() {
  saving.value = true
  try {
    const payload = buildPayload()
    if (editingName.value) {
      await api.put(`/web/models/${encodeURIComponent(editingName.value)}`, payload)
    } else {
      await api.post('/web/models', payload)
    }
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.modelSaved') })
    closeForm()
    await refreshAll()
  } catch (e) {
    notifyError(e)
  } finally {
    saving.value = false
  }
}

async function handleDelete(m: ModelInfo) {
  try {
    await ElMessageBox.confirm(
      t('settings.deleteModelConfirm', { name: m.name }),
      t('settings.menuModels'),
      { type: 'warning' }
    )
  } catch {
    return // 用户取消
  }
  try {
    await api.delete(`/web/models/${encodeURIComponent(m.name)}`)
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.modelDeleted') })
    // 若被删模型正处于编辑中，一并收起表单
    if (formVisible.value && editingName.value === m.name) closeForm()
    await refreshAll()
  } catch (e) {
    notifyError(e)
  }
}

async function handleSetDefault(m: ModelInfo) {
  try {
    await api.put(`/web/models/${encodeURIComponent(m.name)}/default`)
    ElNotification.success({ title: t('settings.menuModels'), message: t('settings.defaultChanged') })
    await refreshAll()
  } catch (e) {
    notifyError(e)
  }
}

async function handleToggleEnabled(m: ModelInfo, enabled: boolean) {
  try {
    // 启用开关复用更新端点：api_key 传空 = 保持原值
    await api.put(`/web/models/${encodeURIComponent(m.name)}`, {
      name: m.name, model: m.model, base_url: m.base_url, api_key: '',
      max_completion_tokens: m.max_completion_tokens,
      max_context_tokens: m.max_context_tokens,
      temperature: m.temperature, top_p: m.top_p,
      frequency_penalty: m.frequency_penalty, presence_penalty: m.presence_penalty,
      seed: m.seed, stop: m.stop || [], thinking: m.thinking, enabled,
    })
    await refreshAll()
  } catch (e) {
    notifyError(e)
    await loadModels() // 失败时回滚开关显示
  }
}

async function handleTest() {
  testing.value = true
  try {
    const payload = buildPayload()
    const resp = await api.post<ModelTestResp>('/web/models/test', {
      // 编辑模式且未填新 key 时带上 name，后端取库中已存 key
      name: editingName.value || undefined,
      base_url: payload.base_url,
      api_key: payload.api_key,
      model: payload.model,
    })
    if (resp.status === 'healthy') {
      ElNotification.success({ title: t('settings.testConnection'), message: t('settings.testOk') })
    } else {
      ElNotification.error({ title: t('settings.testConnection'), message: resp.message || t('settings.testFail') })
    }
  } catch (e) {
    notifyError(e)
  } finally {
    testing.value = false
  }
}

async function refreshAll() {
  await loadModels()
  await meta.reload() // 同步刷新聊天下拉框的模型列表
}

// "···" 菜单命令分发
function handleMenuCommand(m: ModelInfo, command: string | number | object) {
  switch (command) {
    case 'setDefault':
      void handleSetDefault(m)
      break
    case 'edit':
      openEdit(m)
      break
    case 'toggle':
      void handleToggleEnabled(m, !m.enabled)
      break
    case 'delete':
      void handleDelete(m)
      break
  }
}

function notifyError(e: unknown) {
  const message = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e)
  ElNotification.error({ title: t('settings.menuModels'), message })
}

onMounted(() => void loadModels())
</script>

<template>
  <div v-loading="loading">
    <div class="panel-toolbar">
      <el-button type="primary" size="small" @click="openCreate">{{ t('settings.addModel') }}</el-button>
    </div>

    <template v-for="c in cards" :key="c.m ? c.m.name : '__create__'">
      <div class="list-item" :class="{ 'is-editing': isFormOpen(c) }">
        <div v-if="c.m" class="item-header">
          <span class="model-name" :class="{ 'is-off': !c.m.enabled }">{{ c.m.name }}</span>
          <span class="status-dot" :class="c.m.enabled ? 'dot-on' : 'dot-off'" />
          <el-tag v-if="c.m.is_default" size="small" type="primary" effect="light" style="margin-left: 4px">
            {{ t('settings.default') }}
          </el-tag>
          <el-tag v-if="!c.m.enabled" size="small" type="info" effect="plain" style="margin-left: 4px">
            {{ t('settings.disabledTag') }}
          </el-tag>
          <div class="item-actions">
            <el-dropdown trigger="click" @command="(cmd: string | number | object) => handleMenuCommand(c.m!, cmd)">
              <el-button size="small" text :icon="MoreFilled" />
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="setDefault" :disabled="c.m.is_default || !c.m.enabled">
                    {{ t('settings.setDefault') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="edit">{{ t('common.edit') }}</el-dropdown-item>
                  <el-dropdown-item command="toggle" :disabled="c.m.is_default && c.m.enabled">
                    {{ c.m.enabled ? t('settings.disable') : t('settings.enable') }}
                  </el-dropdown-item>
                  <el-dropdown-item command="delete" divided :disabled="c.m.is_default" class="menu-danger">
                    {{ t('common.delete') }}
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </div>
        </div>

        <!-- 行内创建/编辑表单面板：嵌在该模型卡片内部，用灰底与卡片本体区分；
             新建时挂在列表末尾的独立卡片里。处于 v-for 内，字符串 ref 会被收集
             为数组，故用函数 ref。 -->
        <div v-if="isFormOpen(c)" :ref="(el) => (formCardEl = el as HTMLElement | null)" class="form-panel">
          <div class="form-title">{{ editingName ? t('settings.editModel') : t('settings.addModel') }}</div>
          <el-form label-position="top" @submit.prevent>
            <el-form-item :label="t('settings.modelName')" required>
              <el-input v-model="form.name" />
            </el-form-item>
            <el-form-item :label="t('settings.apiUrl')" required>
              <el-input v-model="form.base_url" placeholder="https://api.openai.com/v1" />
            </el-form-item>
            <el-form-item :label="t('settings.apiKey')" :required="!editingName">
              <el-input v-model="form.api_key" type="password" show-password
                :placeholder="editingName ? t('settings.apiKeyKeepHint') : 'sk-...'" />
            </el-form-item>
            <el-form-item label="Model ID" required>
              <el-input v-model="form.model" placeholder="gpt-4o" />
            </el-form-item>

            <el-collapse>
              <el-collapse-item :title="t('settings.advancedParams')">
                <el-form-item label="temperature (0~2)">
                  <el-input-number v-model="form.temperature" :min="0" :max="2" :step="0.1" />
                </el-form-item>
                <el-form-item label="max_completion_tokens">
                  <el-input-number v-model="form.max_completion_tokens" :min="0" :step="256" />
                </el-form-item>
                <el-form-item label="thinking">
                  <el-switch v-model="form.thinking" />
                </el-form-item>
              </el-collapse-item>
            </el-collapse>
          </el-form>
          <div class="form-footer">
            <el-button :loading="testing" @click="handleTest">{{ t('settings.testConnection') }}</el-button>
            <el-button @click="closeForm">{{ t('common.cancel') }}</el-button>
            <el-button type="primary" :loading="saving" @click="handleSave">{{ t('common.save') }}</el-button>
          </div>
        </div>
      </div>
    </template>
    <el-empty v-if="!loading && !models.length && !formVisible" :description="t('settings.noModels')"
      :image-size="60" />
  </div>
</template>

<style scoped>
.panel-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
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

.model-name {
  font-weight: 600;
}

.model-name.is-off {
  opacity: 0.5;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-left: 8px;
  flex-shrink: 0;
}

.dot-on {
  background: var(--el-color-success);
}

.dot-off {
  background: var(--el-text-color-disabled, #c0c4cc);
}

/* 行内表单面板：嵌在模型卡片内部，用填充底色与卡片本体区分
   （深浅色主题下 fill-color 自动适配）。新建时卡片内没有头部行，不需要上间距 */
.form-panel {
  background: var(--el-fill-color-light, rgba(127, 127, 127, 0.08));
  border-radius: 10px;
  padding: 16px 20px;
}

.item-header+.form-panel {
  margin-top: 14px;
}

.form-title {
  font-weight: 600;
  margin-bottom: 12px;
}

/* 高级参数折叠区：仿参照稿的"自定义设置"段落——上方分割线，
   头部与内容透出表单面板的灰底，展开箭头放在标题左侧 */
.form-panel :deep(.el-collapse) {
  --el-collapse-header-bg-color: transparent;
  --el-collapse-content-bg-color: transparent;
  border-top: 1px solid var(--el-border-color-light, rgba(127, 127, 127, 0.2));
  border-bottom: none;
}

.form-panel :deep(.el-collapse-item__header) {
  font-weight: 600;
  border-bottom: none;
}

.form-panel :deep(.el-collapse-item__wrap) {
  border-bottom: none;
}

.form-panel :deep(.el-collapse-item__arrow) {
  order: -1;
  margin: 0 8px 0 0;
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

<!-- 下拉菜单被 teleport 到 body，scoped 样式无法命中，用全局规则标红删除项 -->
<style>
.menu-danger:not(.is-disabled) {
  color: var(--el-color-danger);
}
</style>
