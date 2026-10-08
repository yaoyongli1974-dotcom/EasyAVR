<template>
  <div class="page">
    <div class="page-header">
      <h2>运维审计</h2>
      <el-button @click="load">刷新</el-button>
      <el-button type="primary" @click="exportLogs" :loading="exporting">导出 CSV</el-button>
    </div>

    <el-card>
      <el-form :model="filters" label-width="80px" inline size="small" class="filter-form">
        <el-form-item label="用户名"><el-input v-model="filters.username" placeholder="用户名" clearable style="width: 180px" /></el-form-item>
        <el-form-item label="操作"><el-select v-model="filters.action" placeholder="操作类型" style="width: 140px" clearable>
          <el-option v-for="a in actions" :key="a" :label="a" :value="a" />
        </el-select></el-form-item>
        <el-form-item label="资源"><el-select v-model="filters.resource" placeholder="资源类型" style="width: 140px" clearable>
          <el-option v-for="r in resources" :key="r" :label="r" :value="r" />
        </el-select></el-form-item>
        <el-form-item label="结果"><el-select v-model="filters.result" placeholder="结果" style="width: 120px" clearable>
          <el-option label="成功" value="success" />
          <el-option label="失败" value="failed" />
        </el-select></el-form-item>
        <el-form-item label="IP"><el-input v-model="filters.ip" placeholder="IP 地址" clearable style="width: 160px" /></el-form-item>
        <el-form-item label="时间范围">
          <el-date-picker v-model="filters.dateRange" type="datetimerange" range-separator="至" start-placeholder="开始" end-placeholder="结束" value-format="YYYY-MM-DDTHH:mm:ss" style="width: 320px" />
        </el-form-item>
        <el-form-item label="关键词"><el-input v-model="filters.keyword" placeholder="路径/错误/请求体" clearable style="width: 200px" /></el-form-item>
        <el-form-item>
          <el-button @click="load">查询</el-button>
          <el-button @click="resetFilters">重置</el-button>
        </el-form-item>
      </el-form>

      <el-table :data="logs" border v-loading="loading" row-key="id" style="margin-top: 16px">
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column label="时间" width="180">
          <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column prop="username" label="用户" width="120" />
        <el-table-column prop="ip" label="IP" width="130" />
        <el-table-column prop="method" label="方法" width="80">
          <template #default="{ row }">
            <el-tag :type="methodType(row.method)">{{ row.method }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="path" label="路径" min-width="200" show-overflow-tooltip />
        <el-table-column prop="action" label="操作" width="120" />
        <el-table-column prop="resource" label="资源" width="120" />
        <el-table-column prop="resourceId" label="资源ID" width="90" />
        <el-table-column label="结果" width="90">
          <template #default="{ row }">
            <el-tag :type="row.result === 'success' ? 'success' : 'danger'">{{ row.result === 'success' ? '成功' : '失败' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="latencyMs" label="耗时(ms)" width="90" />
        <el-table-column label="操作" width="100">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="showDetail(row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        v-model:current-page="page"
        v-model:page-size="pageSize"
        :total="total"
        :page-sizes="[20, 50, 100, 200]"
        layout="total, sizes, prev, pager, next, jumper"
        @current-change="load"
        @page-size-change="load"
        style="margin-top: 16px; justify-content: flex-end"
      />
    </el-card>

    <el-dialog v-model="detailVisible" title="审计详情" width="800px">
      <div class="detail-content" v-if="selectedLog">
        <div class="detail-row"><span class="label">ID:</span> {{ selectedLog.id }}</div>
        <div class="detail-row"><span class="label">时间:</span> {{ formatTime(selectedLog.createdAt) }}</div>
        <div class="detail-row"><span class="label">用户:</span> {{ selectedLog.username }} (ID: {{ selectedLog.userId }})</div>
        <div class="detail-row"><span class="label">IP:</span> {{ selectedLog.ip }}</div>
        <div class="detail-row"><span class="label">方法:</span> <el-tag :type="methodType(selectedLog.method)">{{ selectedLog.method }}</el-tag></div>
        <div class="detail-row"><span class="label">路径:</span> {{ selectedLog.path }}</div>
        <div class="detail-row"><span class="label">操作:</span> {{ selectedLog.action }}</div>
        <div class="detail-row"><span class="label">资源:</span> {{ selectedLog.resource }} (ID: {{ selectedLog.resourceId }})</div>
        <div class="detail-row"><span class="label">结果:</span> <el-tag :type="selectedLog.result === 'success' ? 'success' : 'danger'">{{ selectedLog.result === 'success' ? '成功' : '失败' }}</el-tag></div>
        <div class="detail-row" v-if="selectedLog.errorMsg"><span class="label">错误:</span> <span class="error">{{ selectedLog.errorMsg }}</span></div>
        <div class="detail-row"><span class="label">耗时:</span> {{ selectedLog.latencyMs }} ms</div>
        <div class="detail-row" v-if="selectedLog.requestBody">
          <span class="label">请求体:</span>
          <pre class="json-body">{{ formatJson(selectedLog.requestBody) }}</pre>
        </div>
      </div>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, reactive } from 'vue'
import { ElMessage } from 'element-plus'
import { auditApi } from '../api'
import type { AuditLog, AuditLogPage } from '../types'

const actions = ['create', 'update', 'delete', 'read', 'login', 'logout', 'start', 'stop', 'sync', 'import', 'bind', 'unbind', 'assign', 'issue', 'revoke', 'test', 'update_gps', 'change_password', 'export']
const resources = ['devices', 'channels', 'users', 'roles', 'groups', 'ai_tasks', 'ai_providers', 'ai_events', 'recordings', 'snapshots', 'gb_devices', 'gb_cascades', 'ga1400_cascades', 'notification_channels', 'notification_rules', 'cluster_nodes', 'api_keys', 'map', 'video', 'events', 'system']

const loading = ref(false)
const exporting = ref(false)
const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)

const filters = reactive({
  username: '',
  action: '',
  resource: '',
  result: '',
  ip: '',
  dateRange: [] as [string, string] | [],
  keyword: '',
})

const detailVisible = ref(false)
const selectedLog = ref<AuditLog | null>(null)

function methodType(m: string) {
  switch (m) {
    case 'GET': return 'primary'
    case 'POST': return 'success'
    case 'PUT': return 'warning'
    case 'DELETE': return 'danger'
    default: return 'info'
  }
}

function formatTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { hour12: false })
}

function formatJson(str: string) {
  try {
    return JSON.stringify(JSON.parse(str), null, 2)
  } catch {
    return str
  }
}

async function load() {
  loading.value = true
  try {
    const params: Record<string, any> = { page: page.value, pageSize: pageSize.value }
    if (filters.username) params.username = filters.username
    if (filters.action) params.action = filters.action
    if (filters.resource) params.resource = filters.resource
    if (filters.result) params.result = filters.result
    if (filters.ip) params.ip = filters.ip
    if (filters.dateRange.length === 2) {
      params.start = filters.dateRange[0]
      params.end = filters.dateRange[1]
    }
    if (filters.keyword) params.keyword = filters.keyword

    const res = await auditApi.list(params) as AuditLogPage
    logs.value = res.items
    total.value = res.total
    page.value = res.page
    pageSize.value = res.pageSize
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载失败')
  } finally {
    loading.value = false
  }
}

function resetFilters() {
  Object.assign(filters, { username: '', action: '', resource: '', result: '', ip: '', dateRange: [], keyword: '' })
  page.value = 1
  load()
}

async function exportLogs() {
  exporting.value = true
  try {
    const params: Record<string, any> = {}
    if (filters.username) params.username = filters.username
    if (filters.action) params.action = filters.action
    if (filters.resource) params.resource = filters.resource
    if (filters.result) params.result = filters.result
    if (filters.ip) params.ip = filters.ip
    if (filters.dateRange.length === 2) {
      params.start = filters.dateRange[0]
      params.end = filters.dateRange[1]
    }
    if (filters.keyword) params.keyword = filters.keyword

    const blob = await auditApi.export(params) as Blob
    const url = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `audit_logs_${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '')}.csv`
    a.click()
    window.URL.revokeObjectURL(url)
    ElMessage.success('导出成功')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '导出失败')
  } finally {
    exporting.value = false
  }
}

function showDetail(log: AuditLog) {
  selectedLog.value = log
  detailVisible.value = true
}

onMounted(load)
</script>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
}
.filter-form {
  margin-bottom: 8px;
}
.detail-content {
  max-height: 60vh;
  overflow-y: auto;
}
.detail-row {
  display: flex;
  padding: 8px 0;
  border-bottom: 1px solid var(--el-border-color-light);
}
.label {
  width: 100px;
  color: var(--el-text-color-secondary);
  font-weight: 500;
  flex-shrink: 0;
}
.error {
  color: var(--el-color-danger);
}
.json-body {
  margin-top: 8px;
  background: #f5f7fa;
  padding: 12px;
  border-radius: 4px;
  font-size: 12px;
  max-height: 200px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>