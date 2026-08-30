<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '../stores/auth'
import { ApiError } from '../api/client'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()

const username = ref('')
const password = ref('')
const loading = ref(false)

async function handleLogin() {
  if (!username.value || !password.value) {
    ElMessage.warning(t('login.needCredentials'))
    return
  }
  loading.value = true
  try {
    await auth.login(username.value, password.value)
    router.push({ name: 'chat' })
  } catch (e) {
    if (e instanceof ApiError && e.status === 429) {
      ElMessage.error(t('login.tooManyAttempts'))
    } else {
      ElMessage.error(e instanceof Error ? e.message : t('login.failed'))
    }
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-wrap">
    <el-card class="login-card" :header="t('login.header')">
      <el-form label-position="top" @submit.prevent="handleLogin">
        <el-form-item :label="t('login.username')">
          <el-input v-model="username" :placeholder="t('login.username')" />
        </el-form-item>
        <el-form-item :label="t('login.password')">
          <el-input
            v-model="password"
            type="password"
            :placeholder="t('login.password')"
            show-password
            @keyup.enter="handleLogin"
          />
        </el-form-item>
        <el-button
          type="primary"
          style="width: 100%"
          :loading="loading"
          @click="handleLogin"
        >
          {{ t('login.submit') }}
        </el-button>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.login-wrap {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.login-card {
  width: 360px;
  max-width: 90vw;
}
</style>
