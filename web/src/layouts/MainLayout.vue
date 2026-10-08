<template>
  <el-container style="height: 100%">
    <el-aside width="220px" class="aside">
      <div class="logo">EasyAVR</div>
      <el-menu :default-active="route.path" router background-color="#0f172a" text-color="#cbd5e1" active-text-color="#fff">
        <el-menu-item index="/dashboard">总览</el-menu-item>

        <el-sub-menu index="access">
          <template #title>设备接入</template>
          <el-menu-item index="/devices">设备管理</el-menu-item>
          <el-menu-item index="/gb">GB28181 接入</el-menu-item>
          <el-menu-item index="/ga1400">GA/T1400 级联</el-menu-item>
          <el-menu-item index="/gb35114">国密 GB35114</el-menu-item>
        </el-sub-menu>

        <el-sub-menu index="video">
          <template #title>视频中心</template>
          <el-menu-item index="/live">实时视频</el-menu-item>
          <el-menu-item index="/multiscreen">多屏播放</el-menu-item>
          <el-menu-item index="/recordings">录像回看</el-menu-item>
          <el-menu-item index="/snapshots">快照中心</el-menu-item>
          <el-menu-item index="/resources">视频资源中心</el-menu-item>
        </el-sub-menu>

        <el-sub-menu index="ai">
          <template #title>AI 智能中台</template>
          <el-menu-item index="/ai/providers">AI 能力接入</el-menu-item>
          <el-menu-item index="/ai/tasks">分析任务</el-menu-item>
          <el-menu-item index="/ai/events">AI 事件中心</el-menu-item>
          <el-menu-item index="/search">智能检索</el-menu-item>
        </el-sub-menu>

        <el-sub-menu index="system">
          <template #title>平台管理</template>
          <el-menu-item index="/notifications">告警通知</el-menu-item>
          <el-menu-item index="/cluster">集群</el-menu-item>
          <el-menu-item index="/apikeys">开放 API</el-menu-item>
        </el-sub-menu>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="header">
        <span>AI 原生视频融合与智能视频资源管理平台</span>
        <div class="user">
          <el-tag size="small" type="info">{{ auth.user?.role || 'user' }}</el-tag>
          <span class="name">{{ auth.user?.nickname || auth.user?.username }}</span>
          <el-button link type="primary" @click="logout">退出</el-button>
        </div>
      </el-header>
      <el-main>
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

onMounted(() => {
  if (!auth.user) auth.loadProfile().catch(() => {})
})

function logout() {
  auth.logout()
  router.push({ name: 'login' })
}
</script>

<style scoped>
.aside {
  background: #0f172a;
}
.logo {
  color: #fff;
  font-size: 20px;
  font-weight: 700;
  padding: 18px 20px;
  letter-spacing: 1px;
}
.aside :deep(.el-menu) {
  border-right: none;
}
.header {
  background: #fff;
  display: flex;
  align-items: center;
  justify-content: space-between;
  box-shadow: 0 1px 3px rgba(15, 23, 42, 0.08);
}
.user {
  display: flex;
  align-items: center;
  gap: 10px;
}
.name {
  font-size: 14px;
  color: #334155;
}
</style>
