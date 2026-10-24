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
        <el-table-column label="操作" width="380">
          <template #default="{ row }">
            <el-button link type="success" @click="play(row)">回放</el-button>
            <el-button link type="primary" @click="download(row)">下载</el-button>
            <el-button link type="primary" @click="openArchive(row)">归档到云</el-button>
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

    <el-dialog v-model="archiveVisible" title="归档到云" width="520px">
      <template v-if="!archiveResult">
        <el-form label-width="90px">
          <el-form-item label="录像">{{ archiveRow?.file }}</el-form-item>
          <el-form-item label="目标存储">
            <el-select v-model="archiveStoreId" filterable placeholder="选择云存储" style="width: 100%">
              <el-option v-for="s in storages" :key="s.id" :label="`${s.name} (${s.bucket})`" :value="s.id" />
            </el-select>
            <div v-if="!storages.length" class="archive-hint">
              尚未配置云存储，请先在「平台管理 → 存储管理」中新增
            </div>
          </el-form-item>
        </el-form>
        <div v-if="archives.length" class="archive-history">
          <div class="archive-history-title">已有归档</div>
          <div v-for="a in archives" :key="a.id" class="archive-history-item">
            <el-tag :type="a.status === 'uploaded' ? 'success' : 'danger'" size="small">
              {{ a.status === 'uploaded' ? '已上传' : '失败' }}
            </el-tag>
            <span>{{ a.bucket }}/{{ a.object }}（{{ formatSize(a.size) }}）</span>
          </div>
        </div>
      </template>
      <template v-else>
        <el-result icon="success" title="已上传" :sub-title="`${archiveResult.bucket} / ${archiveResult.object}`">
          <template #extra>
            <span style="color: #64748b">大小：{{ formatSize(archiveResult.size) }}</span>
          </template>
        </el-result>
      </template>
      <template #footer>
        <template v-if="!archiveResult">
          <el-button @click="archiveVisible = false">取消</el-button>
          <el-button type="primary" :loading="archiving" @click="doArchive">归档</el-button>
        </template>
        <el-button v-else type="primary" @click="archiveVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { channelApi, recordingApi, storageApi } from '../api'
import type { Channel, CloudStore, Recording, RecordingArchive } from '../types'

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

function formatSize(n?: number) {
  if (n === undefined || n === null) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i ? 1 : 0)} ${units[i]}`
}

const storages = ref<CloudStore[]>([])
const archives = ref<RecordingArchive[]>([])
const archiveVisible = ref(false)
const archiving = ref(false)
const archiveRow = ref<Recording | null>(null)
const archiveStoreId = ref<number>()
const archiveResult = ref<RecordingArchive | null>(null)

async function openArchive(row: Recording) {
  archiveRow.value = row
  archiveStoreId.value = undefined
  archiveResult.value = null
  archives.value = []
  archiveVisible.value = true
  storageApi.list().then((ss) => (storages.value = ss)).catch(() => (storages.value = []))
  recordingApi.archives(row.id).then((a) => (archives.value = a)).catch(() => {})
}

async function doArchive() {
  if (!archiveRow.value || !archiveStoreId.value) {
    ElMessage.warning('请选择目标存储')
    return
  }
  archiving.value = true
  try {
    const res = await recordingApi.archive(archiveRow.value.id, archiveStoreId.value)
    archiveResult.value = res
    ElMessage.success('已上传')
    recordingApi.archives(archiveRow.value.id).then((a) => (archives.value = a)).catch(() => {})
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '归档失败，请确认对象存储可访问')
  } finally {
    archiving.value = false
  }
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
}.timeline-axis {
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
.archive-hint {
  font-size: 12px;
  color: #94a3b8;
  line-height: 18px;
}
.archive-history {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px solid #e2e8f0;
}
.archive-history-title {
  font-size: 12px;
  color: #64748b;
  margin-bottom: 6px;
}
.archive-history-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #334155;
  margin-bottom: 4px;
  word-break: break-all;
}
</style>
