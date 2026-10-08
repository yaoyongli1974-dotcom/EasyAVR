<template>
  <div class="login-page">
    <el-card class="login-card">
      <h1>EasyAVR</h1>
      <p class="subtitle">AI 原生视频融合与智能视频资源管理平台</p>
      <el-form :model="form" @submit.prevent>
        <el-form-item>
          <el-input v-model="form.username" placeholder="用户名" size="large" />
        </el-form-item>
        <el-form-item>
          <el-input v-model="form.password" type="password" placeholder="密码" size="large" show-password @keyup.enter="submit" />
        </el-form-item>
        <el-button type="primary" size="large" style="width: 100%" :loading="loading" @click="submit">
          登录
        </el-button>
      </el-form>
      <p class="hint">默认账号：easyavr / easyavr</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuthStore } from '../stores/auth'

const router = useRouter()
const auth = useAuthStore()
const loading = ref(false)
const form = reactive({ username: 'easyavr', password: 'easyavr' })

async function submit() {
  loading.value = true
  try {
    await auth.login(form.username, form.password)
    router.push({ name: 'dashboard' })
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '登录失败')
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-page {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, #0f172a 0%, #1e3a8a 100%);
}
.login-card {
  width: 380px;
  padding: 12px 8px;
  text-align: center;
}
.login-card h1 {
  margin: 8px 0 4px;
  color: #0f172a;
}
.subtitle {
  color: #64748b;
  font-size: 13px;
  margin-bottom: 20px;
}
.hint {
  color: #94a3b8;
  font-size: 12px;
  margin-top: 14px;
}
</style>
