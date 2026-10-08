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
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { eventApi, systemApi, videoApi } from '../api'

const loading = ref(false)
const info = ref<Record<string, any>>({})
const video = ref<Record<string, any>>({})
const eventStats = ref<Record<string, any>>({})
const planes = computed(() => info.value.planes ?? { deviceAccess: {}, ai: {} })
const planeEvents = computed(() => info.value.planes?.ai?.events ?? 0)
const typeRows = computed(() =>
  Object.entries(eventStats.value.byType || {}).map(([type, count]) => ({ type, count })),
)

async function load() {
  loading.value = true
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
