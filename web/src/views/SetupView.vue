<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const confirmPassword = ref('')
const loading = ref(false)

async function handleSetup() {
  if (!username.value.trim() || !password.value || !confirmPassword.value) {
    ElMessage.warning(t('setup.needAllFields'))
    return
  }
  if (password.value.length < 8) {
    ElMessage.warning(t('setup.passwordTooShort'))
    return
  }
  if (password.value !== confirmPassword.value) {
    ElMessage.warning(t('setup.passwordMismatch'))
    return
  }
  loading.value = true
  try {
    await auth.setup(username.value.trim(), password.value)
    ElMessage.success(t('setup.success'))
    router.push({ name: 'login' })
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : t('setup.failed'))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="setup-wrap">
    <el-card class="setup-card" :header="t('setup.header')">
      <p class="setup-desc">{{ t('setup.desc') }}</p>
      <el-form label-position="top" @submit.prevent="handleSetup">
        <el-form-item :label="t('setup.username')">
          <el-input v-model="username" :placeholder="t('setup.username')" />
        </el-form-item>
        <el-form-item :label="t('setup.password')">
          <el-input
            v-model="password"
            type="password"
            :placeholder="t('setup.passwordHint')"
            show-password
          />
        </el-form-item>
        <el-form-item :label="t('setup.confirmPassword')">
          <el-input
            v-model="confirmPassword"
            type="password"
            :placeholder="t('setup.confirmPassword')"
            show-password
            @keyup.enter="handleSetup"
          />
        </el-form-item>
        <el-button
          type="primary"
          style="width: 100%"
          :loading="loading"
          @click="handleSetup"
        >
          {{ t('setup.submit') }}
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.setup-wrap {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.setup-card {
  width: 360px;
  max-width: 90vw;
}
.setup-desc {
  margin: 0 0 16px;
  font-size: 0.88em;
  opacity: 0.65;
}
</style>
