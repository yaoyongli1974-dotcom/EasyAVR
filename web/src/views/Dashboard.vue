<template>
  <div class="page">
    <div class="page-header">
      <h2>平台总览</h2>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>

    <div class="stat-grid">
      <div class="stat-card">
        <div class="label">设备接入平面 · 设备/通道</div>
        <div class="value">{{ planes.devices }} / {{ planes.channels }}</div>
      </div>
      <div class="stat-card">
        <div class="label">在线通道</div>
        <div class="value">{{ video.channelOnline ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">活跃流（ZLMediaKit）</div>
        <div class="value">{{ video.activeStreams ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">视频资源中心</div>
        <div class="value">{{ video.resourceTotal ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">AI 能力 / 任务</div>
        <div class="value">{{ planes.providers }} / {{ planes.tasks }}</div>
      </div>
      <div class="stat-card">
        <div class="label">AI 事件总数</div>
        <div class="value">{{ planeEvents }}</div>
      </div>
    </div>

    <el-row :gutter="16" style="margin-top: 16px">
      <el-col :span="12">
        <el-card>
          <template #header>运行状态</template>
          <el-descriptions :column="1" border>
            <el-descriptions-item label="版本">{{ info.version }}</el-descriptions-item>
            <el-descriptions-item label="运行时长">{{ info.uptime }}</el-descriptions-item>
            <el-descriptions-item label="ZLMediaKit">
              <el-tag :type="video.zlmHealthy ? 'success' : 'danger'">
                {{ video.zlmHealthy ? '在线' : '离线' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="事件级别分布">
              <el-tag v-for="(v, k) in eventStats.byLevel" :key="k" style="margin-right: 6px">
                {{ k }}: {{ v }}
              </el-tag>
              <span v-if="!eventStats.byLevel || !Object.keys(eventStats.byLevel).length">暂无</span>
            </el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
      <el-col :span="12">
        <el-card>
          <template #header>AI 事件类型 Top</template>
          <el-table :data="typeRows" size="small" height="240">
            <el-table-column prop="type" label="事件类型" />
            <el-table-column prop="count" label="数量" width="100" />
          </el-table>
        </el-card>
      </el-col>
    </el-row>

    <el-row :gutter="16" style="margin-top: 16px">
      <el-col :span="24">
        <el-card v-loading="storageLoading">
          <template #header>
            <div class="card-header">
              <span>磁盘健康</span>
              <span class="checked-at">{{ health.checkedAt ? `检查于 ${format(health.checkedAt)}` : '等待检查' }}</span>
            </div>
          </template>
          <template v-if="hasStorage">
            <div class="disk-summary">
              <div class="disk-metric"><span>总容量</span><b>{{ formatSize(health.totalBytes) }}</b></div>
              <div class="disk-metric"><span>已用</span><b>{{ formatSize(health.usedBytes) }}</b></div>
              <div class="disk-metric"><span>剩余</span><b>{{ formatSize(health.freeBytes) }}</b></div>
              <div class="disk-metric"><span>使用率</span><b :style="{ color: diskColor }">{{ diskPercent.toFixed(1) }}%</b></div>
            </div>
            <el-progress :percentage="diskPercent" :color="diskColor" :stroke-width="14" />

            <div class="disk-columns">
              <div class="disk-col">
                <div class="col-title">目录占用</div>
                <div v-for="d in health.dirs" :key="d.path" class="dir-row">
                  <div class="dir-info">
                    <span class="dir-name">{{ d.name }}</span>
                    <span class="dir-path">{{ d.path }}</span>
                  </div>
                  <div class="dir-figures">
                    <span>{{ formatSize(d.bytes) }}</span>
                    <span class="dir-files">{{ d.files }} 个文件</span>
                  </div>
                </div>
                <el-empty v-if="!health.dirs?.length" description="暂无目录数据" :image-size="50" />
              </div>
              <div class="disk-col">
                <div class="col-title">告警</div>
                <div v-for="(a, i) in health.alerts" :key="i" class="alert-row">
                  <el-alert :title="a.message" :type="alertType(a.level)" :closable="false" show-icon />
                  <div class="alert-meta">{{ a.dir }} · 使用率 {{ a.usedPercent }}% · 级别 {{ levelText(a.level) }}</div>
                </div>
                <el-empty v-if="!health.alerts?.length" description="暂无磁盘告警" :image-size="50" />
              </div>
            </div>

            <div class="disk-footer">
              <span>上次清理：{{ health.lastCleanupAt ? format(health.lastCleanupAt) : '尚未执行' }}</span>
              <span>清理结果：{{ cleanupText(health.lastCleanupResult) }}</span>
            </div>
          </template>
          <el-empty v-else description="暂无磁盘监控数据" />
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { eventApi, systemApi, videoApi } from '../api'
import type { StorageHealth } from '../types'

const loading = ref(false)
const info = ref<Record<string, any>>({})
const video = ref<Record<string, any>>({})
const eventStats = ref<Record<string, any>>({})
const planes = computed(() => {
  const p = info.value.planes ?? {}
  return {
    devices: p.deviceAccess?.devices ?? 0,
    channels: p.deviceAccess?.channels ?? 0,
    providers: p.ai?.providers ?? 0,
    tasks: p.ai?.tasks ?? 0,
    events: p.ai?.events ?? 0,
  }
})
const planeEvents = computed(() => planes.value.events)
const typeRows = computed(() =>
  Object.entries(eventStats.value.byType || {}).map(([type, count]) => ({ type, count })),
)

const storage = ref<StorageHealth | null>(null)
const storageLoading = ref(false)
const health = computed<StorageHealth>(() => storage.value ?? {
  totalBytes: 0,
  usedBytes: 0,
  freeBytes: 0,
  usedPercent: 0,
  dirs: [],
  alerts: [],
  lastCleanupAt: null,
  lastCleanupResult: null,
})
const hasStorage = computed(() => {
  const s = storage.value
  return !!s && (s.totalBytes > 0 || (s.dirs ?? []).length > 0 || (s.alerts ?? []).length > 0)
})
const diskPercent = computed(() => {
  const s = storage.value
  if (!s) return 0
  const p = Number(s.usedPercent)
  if (Number.isFinite(p)) return Math.min(100, Math.max(0, p))
  if (s.totalBytes > 0) return Math.min(100, Math.max(0, (s.usedBytes / s.totalBytes) * 100))
  return 0
})
const diskColor = computed(() => {
  const levels = (storage.value?.alerts ?? []).map((a) => a.level)
  if (levels.includes('critical')) return '#ef4444'
  if (levels.includes('warning')) return '#f59e0b'
  return '#409EFF'
})

function alertType(level: string) {
  if (level === 'critical') return 'error' as const
  if (level === 'warning') return 'warning' as const
  return 'info' as const
}

function levelText(level: string) {
  if (level === 'critical') return '严重'
  if (level === 'warning') return '警告'
  if (level === 'info') return '提示'
  return level
}

function cleanupText(v: unknown) {
  if (v === null || v === undefined || v === '') return '无'
  if (typeof v === 'string') return v
  if (typeof v === 'object') {
    const r = v as { deleted?: number; bytes?: number; message?: string }
    if (r.deleted !== undefined) {
      return `删除 ${r.deleted} 项${r.bytes !== undefined ? `，释放 ${formatSize(r.bytes)}` : ''}`
    }
    if (r.message) return r.message
  }
  return String(v)
}

function format(t?: string | null) {
  return t ? new Date(t).toLocaleString() : ''
}

function formatSize(n?: number) {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i ? 1 : 0)} ${units[i]}`
}

async function loadStorage() {
  storageLoading.value = true
  try {
    storage.value = await systemApi.storageHealth()
  } catch {
    storage.value = null
  } finally {
    storageLoading.value = false
  }
}

async function load() {
  loading.value = true
  loadStorage()
  try {
    const [i, v, e] = await Promise.all([systemApi.info(), videoApi.stats(), eventApi.stats()])
    info.value = i
    video.value = v
    eventStats.value = e
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.card-header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}
.checked-at {
  font-size: 12px;
  color: #94a3b8;
}
.disk-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 32px;
  margin-bottom: 14px;
}
.disk-metric {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.disk-metric span {
  font-size: 12px;
  color: #64748b;
}
.disk-metric b {
  font-size: 18px;
  color: #334155;
}
.disk-columns {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
  margin-top: 18px;
}
.col-title {
  font-size: 13px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 8px;
}
.dir-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 6px 0;
  border-bottom: 1px solid #f1f5f9;
}
.dir-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.dir-name {
  font-size: 13px;
  color: #334155;
}
.dir-path {
  font-size: 11px;
  color: #94a3b8;
  word-break: break-all;
}
.dir-figures {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  white-space: nowrap;
  font-size: 13px;
  color: #334155;
}
.dir-files {
  font-size: 11px;
  color: #94a3b8;
}
.alert-row {
  margin-bottom: 8px;
}
.alert-meta {
  font-size: 12px;
  color: #64748b;
  margin-top: 2px;
}
.disk-footer {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
  margin-top: 16px;
  padding-top: 10px;
  border-top: 1px solid #e2e8f0;
  font-size: 12px;
  color: #64748b;
}
@media (max-width: 860px) {
  .disk-columns {
    grid-template-columns: 1fr;
  }
}
</style>
