<template>
  <div class="page">
    <div class="page-header">
      <h2>实时视频</h2>
      <div>
        <el-input v-model="keyword" placeholder="搜索通道" style="width: 180px; margin-right: 8px" clearable @keyup.enter="load" />
        <el-button @click="load">刷新</el-button>
      </div>
    </div>
    <el-row :gutter="16">
      <el-col :span="6">
        <el-card body-style="padding: 0">
          <el-table :data="channels" height="560" highlight-current-row @current-change="select" @row-click="select">
            <el-table-column label="通道">
              <template #default="{ row }">
                <span>{{ row.name }}</span>
                <el-tag size="small" :type="row.online ? 'success' : 'info'" style="margin-left: 6px">
                  {{ row.online ? '在线' : '离线' }}
                </el-tag>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="18">
        <el-card>
          <template #header>
            <span>{{ selected?.name || '请选择通道' }}</span>
            <el-button v-if="selected && !selected.online" size="small" type="primary" style="float: right" @click="start">
              启动拉流
            </el-button>
          </template>
          <VideoPlayer v-if="playUrls && Object.keys(playUrls).length" :urls="playUrls" />
          <el-empty v-else description="选择左侧在线通道开始播放" />
          <el-descriptions v-if="selected" :column="2" border style="margin-top: 12px">
            <el-descriptions-item label="流ID">{{ selected.streamKey }}</el-descriptions-item>
            <el-descriptions-item label="码流">{{ selected.streamType }}</el-descriptions-item>
            <el-descriptions-item label="推流地址" :span="2">{{ pushUrl }}</el-descriptions-item>
          </el-descriptions>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import VideoPlayer from '../components/VideoPlayer.vue'
import { channelApi } from '../api'
import type { Channel } from '../types'

const route = useRoute()
const channels = ref<Channel[]>([])
const selected = ref<Channel | null>(null)
const playUrls = ref<Record<string, string>>({})
const pushUrl = ref('')
const keyword = ref('')

async function load() {
  channels.value = await channelApi.list({ keyword: keyword.value })
  const presetId = Number(route.query.channelId)
  if (presetId) {
    const ch = channels.value.find((c) => c.id === presetId)
    if (ch) select(ch)
  }
}

async function select(row: Channel) {
  if (!row) return
  selected.value = row
  try {
    const res = await channelApi.playUrls(row.id)
    playUrls.value = res.playUrls || {}
    pushUrl.value = res.pushUrl
  } catch {
    playUrls.value = {}
  }
}

async function start() {
  if (!selected.value) return
  try {
    const res = await channelApi.start(selected.value.id)
    playUrls.value = res.playUrls || {}
    ElMessage.success('拉流已启动')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '启动失败，请确认 ZLMediaKit 已运行')
  }
}

onMounted(load)
</script>
