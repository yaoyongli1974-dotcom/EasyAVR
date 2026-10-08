<template>
  <div class="page">
    <div class="page-header">
      <h2>AI 事件中心</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <div class="stat-grid" style="margin-bottom: 16px">
      <div class="stat-card">
        <div class="label">事件总数</div>
        <div class="value">{{ stats.total ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">今日新增</div>
        <div class="value">{{ stats.today ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">严重 (critical)</div>
        <div class="value">{{ stats.byLevel?.critical ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">警告 (warning)</div>
        <div class="value">{{ stats.byLevel?.warning ?? 0 }}</div>
      </div>
    </div>

    <el-card>
      <div style="display: flex; gap: 8px; margin-bottom: 12px; flex-wrap: wrap">
        <el-select v-model="filters.kind" placeholder="能力类型" clearable style="width: 130px">
          <el-option label="CV" value="cv" />
          <el-option label="VLM" value="vlm" />
          <el-option label="LLM" value="llm" />
        </el-select>
        <el-select v-model="filters.level" placeholder="级别" clearable style="width: 130px">
          <el-option label="info" value="info" />
          <el-option label="warning" value="warning" />
          <el-option label="critical" value="critical" />
        </el-select>
        <el-input v-model="filters.keyword" placeholder="关键词 (summary/payload)" style="width: 240px" clearable @keyup.enter="load" />
        <el-button type="primary" @click="load">检索</el-button>
      </div>

      <el-table :data="events" border v-loading="loading" @row-click="open">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="occurredAt" label="时间" width="180">
          <template #default="{ row }">{{ format(row.occurredAt) }}</template>
        </el-table-column>
        <el-table-column prop="kind" label="能力" width="80" />
        <el-table-column prop="eventType" label="事件类型" width="150" />
        <el-table-column label="级别" width="100">
          <template #default="{ row }">
            <el-tag :type="levelType(row.level)">{{ row.level }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="确认" width="110">
          <template #default="{ row }">
            <el-tag v-if="row.acked" type="success" size="small">已确认</el-tag>
            <el-button v-else link type="primary" @click.stop="ack(row)">确认</el-button>
          </template>
        </el-table-column>
        <el-table-column prop="summary" label="摘要" min-width="280" show-overflow-tooltip />
      </el-table>
      <el-pagination
        style="margin-top: 12px"
        layout="total, prev, pager, next"
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        @current-change="(p: number) => { page = p; load() }"
      />
    </el-card>

    <el-drawer v-model="drawer" title="事件详情" size="45%">
      <el-descriptions :column="1" border v-if="current">
        <el-descriptions-item label="事件类型">{{ current.eventType }}</el-descriptions-item>
        <el-descriptions-item label="级别">{{ current.level }}</el-descriptions-item>
        <el-descriptions-item label="置信度">{{ current.confidence }}</el-descriptions-item>
        <el-descriptions-item label="通道ID">{{ current.channelId }}</el-descriptions-item>
        <el-descriptions-item label="时间">{{ format(current.occurredAt) }}</el-descriptions-item>
        <el-descriptions-item label="摘要">{{ current.summary }}</el-descriptions-item>
        <el-descriptions-item label="确认状态">
          <el-tag v-if="current.acked" type="success" size="small">已确认 {{ current.ackedBy }}</el-tag>
          <el-button v-else link type="primary" @click="ack(current)">确认事件</el-button>
        </el-descriptions-item>
      </el-descriptions>
      <pre v-if="current" class="payload">{{ pretty(current.payload) }}</pre>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { alertApi, eventApi } from '../api'
import type { AIEvent } from '../types'

const events = ref<AIEvent[]>([])
const stats = ref<Record<string, any>>({})
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const drawer = ref(false)
const current = ref<AIEvent | null>(null)
const filters = reactive({ kind: '', level: '', keyword: '' })

async function load() {
  loading.value = true
  try {
    const [list, st] = await Promise.all([
      eventApi.list({ ...filters, page: page.value, pageSize }),
      eventApi.stats(),
    ])
    events.value = list.items
    total.value = list.total
    stats.value = st
  } finally {
    loading.value = false
  }
}

function levelType(level: string) {
  return level === 'critical' ? 'danger' : level === 'warning' ? 'warning' : 'info'
}
function format(t: string) {
  return t ? new Date(t).toLocaleString() : ''
}
function pretty(s: string) {
  try {
    return JSON.stringify(JSON.parse(s || '{}'), null, 2)
  } catch {
    return s
  }
}
function open(row: AIEvent) {
  current.value = row
  drawer.value = true
}

async function ack(row: AIEvent) {
  try {
    await alertApi.ackEvent(row.id)
    row.acked = true
    if (current.value?.id === row.id) current.value.acked = true
    ElMessage.success('已确认')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '确认失败')
  }
}

onMounted(load)
</script>

<style scoped>
.payload {
  background: #0f172a;
  color: #e2e8f0;
  padding: 12px;
  border-radius: 8px;
  margin-top: 12px;
  white-space: pre-wrap;
  font-size: 12px;
}
</style>
