<template>
  <div class="page">
    <div class="page-header">
      <h2>多屏播放</h2>
      <div>
        <el-radio-group v-model="size" @change="resize">
          <el-radio-button :value="1">单屏</el-radio-button>
          <el-radio-button :value="4">四分屏</el-radio-button>
          <el-radio-button :value="9">九分屏</el-radio-button>
          <el-radio-button :value="16">十六分屏</el-radio-button>
        </el-radio-group>
        <el-button style="margin-left: 8px" @click="toggleCarousel">
          {{ carousel ? '停止轮播' : '开始轮播' }}
        </el-button>
        <el-button @click="reloadAll">重新加载</el-button>
        <el-button @click="loadLast">上次播放</el-button>
      </div>
    </div>

    <div class="grid" :style="gridStyle">
      <div v-for="(slot, i) in slots" :key="i" class="cell">
        <VideoPlayer v-if="slot.urls && Object.keys(slot.urls).length" :urls="slot.urls" initial="webrtc" />
        <div v-else class="placeholder" @click="openPicker(i)">
          <span class="plus">+</span>
          <span>选择通道</span>
        </div>
        <div v-if="slot.channelId" class="cell-bar">
          <span class="cell-name">{{ slot.name }}</span>
          <el-button link size="small" @click="openPicker(i)">切换</el-button>
          <el-button link size="small" type="danger" @click="clearSlot(i)">清除</el-button>
        </div>
      </div>
    </div>

    <el-dialog v-model="pickerVisible" title="选择通道" width="520px">
      <el-input v-model="keyword" placeholder="搜索通道" clearable style="margin-bottom: 10px" @keyup.enter="filterChannels" />
      <el-table :data="filteredChannels" height="360" highlight-current-row @row-click="pick">
        <el-table-column label="通道">
          <template #default="{ row }">
            {{ row.name }}
            <el-tag size="small" :type="row.online ? 'success' : 'info'" style="margin-left: 6px">
              {{ row.online ? '在线' : '离线' }}
            </el-tag>
          </template>
        </el-table-column>
      </el-table>
      <el-alert v-if="carousel" type="info" :closable="false" title="轮播将按所选通道列表依次切换各窗口" />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import VideoPlayer from '../components/VideoPlayer.vue'
import { channelApi } from '../api'
import type { Channel } from '../types'

interface Slot {
  channelId?: number
  name?: string
  urls?: Record<string, string>
}

const size = ref(4)
const slots = ref<Slot[]>([])
const channels = ref<Channel[]>([])
const keyword = ref('')
const pickerVisible = ref(false)
const pickIndex = ref(0)
const carousel = ref(false)
const carouselSec = 15
let timer: number | null = null
let rotateAt = 0

const filteredChannels = computed(() =>
  channels.value.filter((c) => !keyword.value || c.name.includes(keyword.value)),
)

const gridStyle = computed(() => {
  const cols = Math.sqrt(size.value)
  return {
    display: 'grid',
    gridTemplateColumns: `repeat(${cols}, 1fr)`,
    gap: '8px',
  }
})

function initSlots() {
  const n = size.value
  const next: Slot[] = []
  for (let i = 0; i < n; i++) next.push(slots.value[i] || {})
  slots.value = next
}

function resize() {
  initSlots()
  save()
}

function filterChannels() {
  // computed handles filtering
}

function openPicker(i: number) {
  pickIndex.value = i
  pickerVisible.value = true
}

function clearSlot(i: number) {
  slots.value[i] = {}
  save()
}

async function pick(row: Channel) {
  const i = pickIndex.value
  slots.value[i] = { channelId: row.id, name: row.name }
  pickerVisible.value = false
  try {
    const res = await channelApi.playUrls(row.id)
    slots.value[i] = { channelId: row.id, name: row.name, urls: res.playUrls }
  } catch {
    ElMessage.warning(`${row.name} 取流地址获取失败`)
  }
  save()
}

async function reloadAll() {
  for (const slot of slots.value) {
    if (slot.channelId) {
      try {
        const res = await channelApi.playUrls(slot.channelId)
        slot.urls = res.playUrls
      } catch {
        /* ignore */
      }
    }
  }
}

function save() {
  localStorage.setItem('easyavr_multiscreen', JSON.stringify({ size: size.value, ids: slots.value.map((s) => s.channelId || 0) }))
}

async function loadLast() {
  const raw = localStorage.getItem('easyavr_multiscreen')
  if (!raw) {
    ElMessage.info('暂无播放记录')
    return
  }
  const data = JSON.parse(raw)
  size.value = data.size || 4
  initSlots()
  for (let i = 0; i < slots.value.length; i++) {
    const id = data.ids?.[i]
    if (!id) continue
    const ch = channels.value.find((c) => c.id === id)
    if (ch) await pickAt(i, ch)
  }
  ElMessage.success('已加载上次播放')
}

async function pickAt(i: number, row: Channel) {
  slots.value[i] = { channelId: row.id, name: row.name }
  try {
    const res = await channelApi.playUrls(row.id)
    slots.value[i] = { channelId: row.id, name: row.name, urls: res.playUrls }
  } catch {
    /* ignore */
  }
}

function toggleCarousel() {
  if (carousel.value) {
    carousel.value = false
    if (timer) window.clearInterval(timer)
    timer = null
    return
  }
  const pool = channels.value.filter((c) => c.online)
  if (pool.length === 0) {
    ElMessage.warning('没有在线通道可轮播')
    return
  }
  carousel.value = true
  rotateAt = 0
  timer = window.setInterval(async () => {
    const offset = rotateAt % pool.length
    for (let i = 0; i < slots.value.length; i++) {
      const ch = pool[(offset + i) % pool.length]
      await pickAt(i, ch)
    }
    rotateAt++
  }, carouselSec * 1000)
}

onMounted(async () => {
  channels.value = await channelApi.list()
  initSlots()
})

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
})
</script>

<style scoped>
.grid {
  background: #0b1220;
  padding: 8px;
  border-radius: 8px;
  min-height: 400px;
}
.cell {
  position: relative;
  background: #111827;
  border-radius: 6px;
  overflow: hidden;
  min-height: 160px;
}
.placeholder {
  width: 100%;
  height: 100%;
  min-height: 160px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: #64748b;
  cursor: pointer;
}
.plus {
  font-size: 30px;
  line-height: 1;
}
.cell-bar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  background: rgba(15, 23, 42, 0.7);
  color: #e2e8f0;
  font-size: 12px;
}
.cell-name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
