<template>
  <div class="page">
    <div class="page-header">
      <h2>GA/T1400 视图库级联</h2>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-alert
      type="info"
      :closable="false"
      style="margin-bottom: 12px"
      title="向上级联：本平台注册并推送 AI 事件到上级视图库；下级级联：本平台订阅下级视图库的告警。需 EASYAVR_GA1400_ENABLED=true。"
    />

    <el-card style="margin-bottom: 16px">
      <template #header>
        <span>级联列表</span>
        <el-button type="primary" size="small" style="float: right" @click="openCreate">新增级联</el-button>
      </template>
      <el-table :data="cascades" border>
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column label="方向" width="110">
          <template #default="{ row }">
            <el-tag :type="row.direction === 'up' ? 'primary' : 'success'">
              {{ row.direction === 'up' ? '向上级联' : '下级订阅' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="url" label="对端 URL" min-width="220" />
        <el-table-column prop="platformId" label="平台编码" min-width="160" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.online ? 'success' : 'info'">{{ row.online ? '在线' : '离线' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="260">
          <template #default="{ row }">
            <el-button link type="primary" @click="test(row)">测试</el-button>
            <el-button link type="primary" @click="sync(row)">同步</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card>
      <template #header>下级订阅</template>
      <el-table :data="subs" border>
        <el-table-column prop="subscribeId" label="订阅 ID" min-width="160" />
        <el-table-column prop="title" label="标题" min-width="140" />
        <el-table-column prop="eventTypes" label="事件类型" min-width="140" />
        <el-table-column prop="status" label="状态" width="100" />
        <el-table-column label="续订次数" width="100">
          <template #default="{ row }">{{ row.renewCount }}</template>
        </el-table-column>
        <el-table-column label="到期时间" min-width="170">
          <template #default="{ row }">
            {{ row.expiresAt && new Date(row.expiresAt).getFullYear() > 1 ? new Date(row.expiresAt).toLocaleString() : '-' }}
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="visible" title="新增 GA/T1400 级联" width="520px">
      <el-form :model="form" label-width="100px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="方向">
          <el-select v-model="form.direction" style="width: 100%">
            <el-option label="向上级联（推送事件）" value="up" />
            <el-option label="下级订阅（接收告警）" value="down" />
          </el-select>
        </el-form-item>
        <el-form-item label="对端 URL"><el-input v-model="form.url" placeholder="http://host:port" /></el-form-item>
        <el-form-item label="平台编码"><el-input v-model="form.platformId" /></el-form-item>
        <el-form-item label="用户名"><el-input v-model="form.username" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="form.password" type="password" show-password /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="form.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ga1400Api } from '../api'
import type { GA1400Cascade, GA1400Subscription } from '../types'

const cascades = ref<GA1400Cascade[]>([])
const subs = ref<GA1400Subscription[]>([])
const visible = ref(false)
const form = reactive<Partial<GA1400Cascade> & { password?: string }>({ direction: 'up', enabled: true })

async function load() {
  ;[cascades.value, subs.value] = await Promise.all([ga1400Api.cascades(), ga1400Api.subscriptions()])
}

function openCreate() {
  Object.assign(form, { name: '', direction: 'up', url: '', platformId: '', username: '', password: '', enabled: true })
  visible.value = true
}

async function save() {
  try {
    await ga1400Api.createCascade(form)
    ElMessage.success('已保存')
    visible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  }
}

async function test(row: GA1400Cascade) {
  try {
    await ga1400Api.testCascade(row.id)
    ElMessage.success('连通性测试成功')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '测试失败')
  }
}

async function sync(row: GA1400Cascade) {
  try {
    const res = await ga1400Api.syncCascade(row.id)
    ElMessage.success(row.direction === 'up' ? `已全量同步 ${res.count} 条事件` : '已续订下级订阅')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '同步失败')
  }
}

async function remove(row: GA1400Cascade) {
  await ElMessageBox.confirm(`删除级联「${row.name}」？`, '确认', { type: 'warning' })
  await ga1400Api.removeCascade(row.id)
  load()
}

onMounted(load)
</script>
