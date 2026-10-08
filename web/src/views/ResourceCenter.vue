<template>
  <div class="page">
    <div class="page-header">
      <h2>视频资源中心</h2>
      <div>
        <el-select v-model="kind" placeholder="类型" clearable style="width: 140px; margin-right: 8px" @change="load">
          <el-option label="实时流" value="live" />
          <el-option label="录像" value="recording" />
          <el-option label="快照" value="snapshot" />
        </el-select>
        <el-button @click="sync">同步状态</el-button>
      </div>
    </div>

    <div class="stat-grid" style="margin-bottom: 16px">
      <div class="stat-card">
        <div class="label">资源条目</div>
        <div class="value">{{ total }}</div>
      </div>
      <div class="stat-card">
        <div class="label">在线通道</div>
        <div class="value">{{ stats.channelOnline ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">活跃流</div>
        <div class="value">{{ stats.activeStreams ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="label">流媒体内核</div>
        <div class="value">{{ stats.zlmHealthy ? '在线' : '离线' }}</div>
      </div>
    </div>

    <el-table :data="resources" border v-loading="loading">
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="kind" label="类型" width="100" />
      <el-table-column prop="channelId" label="通道" width="90" />
      <el-table-column prop="protocol" label="协议" width="110" />
      <el-table-column prop="url" label="URL" min-width="300" show-overflow-tooltip />
      <el-table-column prop="tags" label="标签" min-width="140" />
      <el-table-column label="大小" width="110">
        <template #default="{ row }">{{ humanSize(row.sizeBytes) }}</template>
      </el-table-column>
    </el-table>
    <el-pagination
      style="margin-top: 12px"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="(p: number) => { page = p; load() }"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { videoApi } from '../api'
import type { VideoResource } from '../types'

const resources = ref<VideoResource[]>([])
const stats = ref<Record<string, any>>({})
const kind = ref('')
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const [list, st] = await Promise.all([
      videoApi.resources({ kind: kind.value, page: page.value, pageSize }),
      videoApi.stats(),
    ])
    resources.value = list.items
    total.value = list.total
    stats.value = st
  } finally {
    loading.value = false
  }
}

async function sync() {
  const res = await videoApi.sync()
  ElMessage.success(`已同步，更新 ${res.updated} 个通道状态`)
  load()
}

function humanSize(n: number) {
  if (!n) return '-'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

onMounted(load)
</script>
