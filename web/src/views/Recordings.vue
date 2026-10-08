<template>
  <div class="page">
    <div class="page-header">
      <h2>录像回看</h2>
      <div>
        <el-select v-model="channelId" placeholder="选择通道" filterable style="width: 200px; margin-right: 8px" @change="load">
          <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" />
        </el-select>
        <el-date-picker
          v-model="date"
          type="date"
          value-format="YYYY-MM-DD"
          placeholder="日期"
          style="margin-right: 8px"
          @change="load"
        />
        <el-button :disabled="!channelId" @click="sync">同步录像</el-button>
        <el-button :disabled="!channelId" @click="cleanup">清理过期</el-button>
        <el-radio-group v-model="view" style="margin-left: 8px">
          <el-radio-button value="list">列表</el-radio-button>
          <el-radio-button value="timeline">时间轴</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <el-card v-if="view === 'list'">
      <el-table :data="recordings" border v-loading="loading">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="开始时间" width="200">
          <template #default="{ row }">{{ format(row.startTime) }}</template>
        </el-table-column>
        <el-table-column prop="file" label="文件" min-width="220" show-overflow-tooltip />
        <el-table-column prop="date" label="日期" width="120" />
        <el-table-column label="标记" width="80">
          <template #default="{ row }">
            <el-tag v-if="row.marked" type="danger" size="small">紧急</el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="300">
          <template #default="{ row }">
            <el-button link type="success" @click="play(row)">回放</el-button>
            <el-button link type="primary" @click="download(row)">下载</el-button>
            <el-button link type="warning" @click="toggleMark(row)">{{ row.marked ? '取消标记' : '紧急标记' }}</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!recordings.length" description="暂无录像（确认通道已开录像并点击同步）" />
    </el-card>

    <el-card v-else v-loading="loading">
      <div class="timeline">
        <div class="timeline-axis">
          <span v-for="h in axisHours" :key="h" class="tick" :style="{ left: (h / 24 * 100) + '%' }">{{ h }}</span>
        </div>
        <el-tooltip
          v-for="r in recordings"
          :key="r.id"
          :content="`${format(r.startTime)} · ${r.file}`"
          placement="top"
        >
          <div
            class="segment"
            :class="{ marked: r.marked }"
            :style="segmentStyle(r)"
            @click="play(r)"
          ></div>
        </el-tooltip>
      </div>
      <el-empty v-if="!recordings.length" description="当日无录像" />
    </el-card>

    <el-dialog v-model="playerVisible" title="录像回放" width="720px">
      <div class="video-box">
        <video v-if="current" ref="player" :src="current.url" controls autoplay style="width: 100%"></video>
      </div>
      <div style="margin-top: 10px; text-align: right">
        <span style="margin-right: 8px; color: #64748b">倍速</span>
        <el-radio-group v-model="speed" @change="applySpeed">
          <el-radio-button v-for="s in speeds" :key="s" :value="s">{{ s }}x</el-radio-button>
        </el-radio-group>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { channelApi, recordingApi } from '../api'
import type { Channel, Recording } from '../types'

const channels = ref<Channel[]>([])
const channelId = ref<number>()
const date = ref(new Date().toISOString().slice(0, 10))
const recordings = ref<Recording[]>([])
const loading = ref(false)
const playerVisible = ref(false)
const current = ref<Recording | null>(null)
const player = ref<HTMLVideoElement>()
const view = ref<'list' | 'timeline'>('list')
const speed = ref(1)
const speeds = [0.5, 1, 2, 4, 8, 16]
const axisHours = Array.from({ length: 25 }, (_, i) => i)

async function loadChannels() {
  channels.value = await channelApi.list()
}

async function load() {
  if (!channelId.value) return
  loading.value = true
  try {
    recordings.value = await recordingApi.list({ channelId: channelId.value, date: date.value })
  } finally {
    loading.value = false
  }
}

async function sync() {
  if (!channelId.value) return
  try {
    const res = await recordingApi.sync(channelId.value, date.value)
    ElMessage.success(`已同步 ${res.count} 条录像`)
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '同步失败，请确认 ZLMediaKit 与录像已开启')
  }
}

function play(row: Recording) {
  current.value = row
  speed.value = 1
  playerVisible.value = true
  nextTick(() => {
    if (player.value) player.value.playbackRate = 1
  })
}

function applySpeed() {
  if (player.value) player.value.playbackRate = speed.value
}

function segmentStyle(r: Recording): Record<string, string> {
  const t = new Date(r.startTime)
  const secs = t.getHours() * 3600 + t.getMinutes() * 60 + t.getSeconds()
  const left = (secs / 86400) * 100
  const width = Math.max(((r.duration || 60) / 86400) * 100, 0.3)
  return { left: left + '%', width: width + '%' }
}

async function toggleMark(row: Recording) {
  const marked = !row.marked
  await recordingApi.mark(row.id, { marked, mark: marked ? '紧急' : '' })
  row.marked = marked
  row.mark = marked ? '紧急' : ''
  ElMessage.success(marked ? '已标记为紧急' : '已取消标记')
}

async function cleanup() {
  await ElMessageBox.confirm('按各通道录像计划的保存天数清理过期录像目录项？', '清理过期', { type: 'warning' })
  const r = await recordingApi.cleanup(0)
  ElMessage.success(`已清理 ${r.removed} 条`)
  load()
}

function download(row: Recording) {
  window.open(row.url, '_blank')
}

async function remove(row: Recording) {
  await ElMessageBox.confirm('删除该录像目录项？', '确认', { type: 'warning' })
  await recordingApi.remove(row.id)
  load()
}

function format(t: string) {
  return t ? new Date(t).toLocaleString() : ''
}

onMounted(async () => {
  await loadChannels()
  if (channels.value.length) {
    channelId.value = channels.value[0].id
    load()
  }
})
</script>

<style scoped>
.timeline {
  position: relative;
  height: 90px;
  background: #f8fafc;
  border-radius: 6px;
  padding-top: 24px;
}
.timeline-axis {
  position: absolute;
  top: 4px;
  left: 0;
  right: 0;
  height: 16px;
}
.tick {
  position: absolute;
  font-size: 10px;
  color: #94a3b8;
  transform: translateX(-50%);
}
.segment {
  position: absolute;
  top: 34px;
  height: 36px;
  background: #3b82f6;
  border-radius: 3px;
  min-width: 3px;
  cursor: pointer;
}
.segment.marked {
  background: #ef4444;
}
</style>
