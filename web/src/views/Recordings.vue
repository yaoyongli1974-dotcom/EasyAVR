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
      </div>
    </div>

    <el-card>
      <el-table :data="recordings" border v-loading="loading">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="开始时间" width="200">
          <template #default="{ row }">{{ format(row.startTime) }}</template>
        </el-table-column>
        <el-table-column prop="file" label="文件" min-width="220" show-overflow-tooltip />
        <el-table-column prop="date" label="日期" width="120" />
        <el-table-column label="操作" width="240">
          <template #default="{ row }">
            <el-button link type="success" @click="play(row)">回放</el-button>
            <el-button link type="primary" @click="download(row)">下载</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!recordings.length" description="暂无录像（确认通道已开录像并点击同步）" />
    </el-card>

    <el-dialog v-model="playerVisible" title="录像回放" width="720px">
      <div class="video-box">
        <video v-if="current" :src="current.url" controls autoplay style="width: 100%"></video>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
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
  playerVisible.value = true
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
