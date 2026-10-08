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
          <el-menu-item index="/ai/models">模型管理</el-menu-item>
          <el-menu-item index="/ai/tasks">分析任务</el-menu-item>
          <el-menu-item index="/ai/pipeline">数据与训练</el-menu-item>
          <el-menu-item index="/ai/events">AI 事件中心</el-menu-item>
          <el-menu-item index="/search">智能检索</el-menu-item>
        </el-sub-menu>

        <el-sub-menu index="system">
          <template #title>平台管理</template>
          <el-menu-item index="/notifications">告警通知</el-menu-item>
          <el-menu-item index="/alerts">告警策略</el-menu-item>
          <el-menu-item index="/cluster">集群</el-menu-item>
          <el-menu-item index="/apikeys">开放 API</el-menu-item>
          <el-menu-item index="/users">用户与角色</el-menu-item>
          <el-menu-item index="/policy">权限策略</el-menu-item>
          <el-menu-item index="/config">平台配置</el-menu-item>
          <el-menu-item index="/groups">设备分组</el-menu-item>
          <el-menu-item index="/map">电子地图</el-menu-item>
          <el-menu-item index="/audit">运维审计</el-menu-item>
        </el-sub-menu>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="header">
        <span>AI 原生视频融合与智能视频资源管理平台</span>
        <div class="user">
          <el-input
            v-model="searchQuery"
            class="search"
            placeholder="功能搜索 (Ctrl+K)"
            readonly
            @click="openSearch"
          >
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-tag size="small" type="info">{{ auth.user?.role || 'user' }}</el-tag>
          <span class="name">{{ auth.user?.nickname || auth.user?.username }}</span>
          <el-button link type="primary" @click="pwdVisible = true">修改密码</el-button>
          <el-button link type="primary" @click="logout">退出</el-button>
        </div>
      </el-header>
      <el-main>
        <router-view />
      </el-main>
    </el-container>

    <el-dialog v-model="pwdVisible" title="修改密码" width="420px">
      <el-form label-width="90px">
        <el-form-item label="原密码"><el-input v-model="pwdForm.oldPassword" type="password" show-password /></el-form-item>
        <el-form-item label="新密码"><el-input v-model="pwdForm.newPassword" type="password" show-password /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdVisible = false">取消</el-button>
        <el-button type="primary" :loading="pwdSaving" @click="changePwd">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="searchVisible" title="功能搜索" width="520px" @opened="focusSearch">
      <el-input ref="searchInput" v-model="searchQuery" placeholder="输入功能名称，如 录像 / 级联 / 设备管理" clearable @keyup.enter="runFirst">
        <template #prefix><el-icon><Search /></el-icon></template>
      </el-input>
      <div class="search-list">
        <div v-for="f in searchResults" :key="f.path" class="search-item" @click="goto(f.path)">
          <span>{{ f.name }}</span>
          <span class="search-path">{{ f.path }}</span>
        </div>
        <div v-if="!searchResults.length" class="search-empty">未找到匹配功能</div>
      </div>
    </el-dialog>
  </el-container>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Search } from '@element-plus/icons-vue'
import { userApi } from '../api'
import { useAuthStore } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

interface Feature { name: string; path: string; kw: string }
const features: Feature[] = [
  { name: '总览', path: '/dashboard', kw: 'dashboard home overview 首页 总览 概览' },
  { name: '设备管理', path: '/devices', kw: 'device 设备 批量 导入 导出 检测' },
  { name: 'GB28181 接入', path: '/gb', kw: 'gb28181 国标 白名单 黑名单 级联 设备' },
  { name: 'GA/T1400 级联', path: '/ga1400', kw: 'ga1400 1400 视图库 级联' },
  { name: '国密 GB35114', path: '/gb35114', kw: 'gb35114 国密 证书 sm2' },
  { name: '实时视频', path: '/live', kw: 'live 直播 实时 云台 ptz 诊断 vqd 流量 状态' },
  { name: '多屏播放', path: '/multiscreen', kw: 'multi 分屏 轮播 多屏' },
  { name: '录像回看', path: '/recordings', kw: 'recording 录像 回看 倍速 时间轴 紧急标记' },
  { name: '快照中心', path: '/snapshots', kw: 'snapshot 快照 抓拍' },
  { name: '视频资源中心', path: '/resources', kw: 'resource 资源 视频' },
  { name: 'AI 能力接入', path: '/ai/providers', kw: 'provider ai 能力 接入' },
  { name: '模型管理', path: '/ai/models', kw: 'model 模型 版本 部署' },
  { name: '分析任务', path: '/ai/tasks', kw: 'task 任务 roi 计划 灵敏度 分析' },
  { name: '数据与训练', path: '/ai/pipeline', kw: 'dataset 数据集 标注 训练 流水线' },
  { name: 'AI 事件中心', path: '/ai/events', kw: 'event 事件 告警 确认' },
  { name: '智能检索', path: '/search', kw: 'search 检索 语义 搜索' },
  { name: '告警通知', path: '/notifications', kw: 'notify 通知 webhook 邮件' },
  { name: '告警策略', path: '/alerts', kw: 'alert 策略 分级 分发' },
  { name: '集群', path: '/cluster', kw: 'cluster 集群 节点' },
  { name: '开放 API', path: '/apikeys', kw: 'api key 开放 密钥' },
  { name: '用户与角色', path: '/users', kw: 'user role 用户 角色 批量 导入 导出' },
  { name: '权限策略', path: '/policy', kw: 'policy 权限 casbin 策略' },
  { name: '平台配置', path: '/config', kw: 'config 配置 gb28181 ehome gb35114 开关 播放鉴权 信令' },
  { name: '设备分组', path: '/groups', kw: 'group 分组' },
  { name: '电子地图', path: '/map', kw: 'map 地图 gis 轨迹 定位' },
  { name: '运维审计', path: '/audit', kw: 'audit 审计 日志 操作记录' },
]

const searchVisible = ref(false)
const searchQuery = ref('')
const searchInput = ref()
const searchResults = computed(() => {
  const q = searchQuery.value.trim().toLowerCase()
  if (!q) return features
  return features.filter((f) => (f.name + f.path + f.kw).toLowerCase().includes(q))
})

function openSearch() {
  searchQuery.value = ''
  searchVisible.value = true
}
function focusSearch() {
  nextTick(() => searchInput.value?.focus())
}
function goto(path: string) {
  searchVisible.value = false
  searchQuery.value = ''
  router.push(path)
}
function runFirst() {
  if (searchResults.value.length) goto(searchResults.value[0].path)
}
function onKey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    openSearch()
  }
}

const pwdVisible = ref(false)
const pwdSaving = ref(false)
const pwdForm = reactive({ oldPassword: '', newPassword: '' })

onMounted(() => {
  if (!auth.user) auth.loadProfile().catch(() => {})
  window.addEventListener('keydown', onKey)
})
onUnmounted(() => window.removeEventListener('keydown', onKey))

async function changePwd() {
  if (!pwdForm.newPassword) {
    ElMessage.warning('请输入新密码')
    return
  }
  pwdSaving.value = true
  try {
    await userApi.changePassword(pwdForm.oldPassword, pwdForm.newPassword)
    ElMessage.success('密码已修改')
    pwdVisible.value = false
    Object.assign(pwdForm, { oldPassword: '', newPassword: '' })
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '修改失败')
  } finally {
    pwdSaving.value = false
  }
}

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
.search {
  width: 220px;
  margin-right: 6px;
}
.search-list {
  margin-top: 10px;
  max-height: 320px;
  overflow-y: auto;
}
.search-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
}
.search-item:hover {
  background: #f1f5f9;
}
.search-path {
  color: #94a3b8;
  font-size: 12px;
}
.search-empty {
  padding: 16px;
  text-align: center;
  color: #94a3b8;
}
</style>
