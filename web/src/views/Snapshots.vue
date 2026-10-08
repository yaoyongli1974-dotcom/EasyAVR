<template>
  <div class="page">
    <div class="page-header">
      <h2>快照中心</h2>
      <div>
        <el-select v-model="channelId" placeholder="全部通道" filterable clearable style="width: 200px; margin-right: 8px" @change="load">
          <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" />
        </el-select>
        <el-button :disabled="!channelId" @click="capture">立即抓拍</el-button>
        <el-button :disabled="!channelId" @click="openConfig">抓拍配置</el-button>
        <el-button @click="load">刷新</el-button>
      </div>
    </div>

    <el-card v-loading="loading">
      <div v-if="snapshots.length" class="snap-grid">
        <div v-for="s in snapshots" :key="s.id" class="snap-item">
          <img :src="s.url" @click="preview = s.url" />
          <div class="meta">
            <span>{{ format(s.takenAt) }}</span>
            <el-button link type="danger" size="small" @click="remove(s)">删除</el-button>
          </div>
        </div>
      </div>
      <el-empty v-else description="暂无快照，选择通道后点击“立即抓拍”" />
      <el-pagination
        v-if="total > pageSize"
        style="margin-top: 12px"
        layout="total, prev, pager, next"
        :total="total"
        :page-size="pageSize"
        :current-page="page"
        @current-change="(p: number) => { page = p; load() }"
      />
    </el-card>

    <el-dialog v-model="configVisible" title="通道抓拍配置" width="460px">
      <el-form :model="config" label-width="110px">
        <el-form-item label="启用定时抓拍"><el-switch v-model="config.snapshotEnabled" /></el-form-item>
        <el-form-item label="抓拍间隔(秒)"><el-input-number v-model="config.snapshotInterval" :min="30" :step="30" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="configVisible = false">取消</el-button>
        <el-button type="primary" @click="saveConfig">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="previewVisible" width="60%">
      <img v-if="preview" :src="preview" style="width: 100%" />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { channelApi, snapshotApi } from '../api'
import type { Channel, Snapshot } from '../types'

const channels = ref<Channel[]>([])
const channelId = ref<number>()
const snapshots = ref<Snapshot[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 24
const loading = ref(false)
const preview = ref('')
const previewVisible = computed({
  get: () => !!preview.value,
  set: (v: boolean) => { if (!v) preview.value = '' },
})
const configVisible = ref(false)
const config = reactive({ snapshotEnabled: false, snapshotInterval: 300 })

async function load() {
  loading.value = true
  try {
    const res = await snapshotApi.list({ channelId: channelId.value, page: page.value, pageSize })
    snapshots.value = res.items
    total.value = res.total
  } finally {
    loading.value = false
  }
}

async function capture() {
  if (!channelId.value) return
  try {
    await snapshotApi.capture(channelId.value)
    ElMessage.success('抓拍成功')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '抓拍失败（需 ffmpeg 且通道可访问）')
  }
}

function openConfig() {
  const ch = channels.value.find((c) => c.id === channelId.value)
  if (ch) {
    config.snapshotEnabled = ch.snapshotEnabled
    config.snapshotInterval = ch.snapshotInterval || 300
    configVisible.value = true
  }
}

async function saveConfig() {
  if (!channelId.value) return
  await channelApi.update(channelId.value, { ...config })
  ElMessage.success('已保存')
  configVisible.value = false
  channels.value = await channelApi.list()
}

async function remove(s: Snapshot) {
  await ElMessageBox.confirm('删除该快照？', '确认', { type: 'warning' })
  await snapshotApi.remove(s.id)
  load()
}

function format(t: string) {
  return t ? new Date(t).toLocaleString() : ''
}

onMounted(async () => {
  channels.value = await channelApi.list()
  load()
})
</script>

<style scoped>
.snap-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px;
}
.snap-item {
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  overflow: hidden;
}
.snap-item img {
  width: 100%;
  aspect-ratio: 16 / 9;
  object-fit: cover;
  cursor: pointer;
  display: block;
}
.snap-item .meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px;
  font-size: 12px;
  color: #64748b;
}
</style>
