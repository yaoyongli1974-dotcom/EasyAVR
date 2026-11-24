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
          <img v-if="s.url" :src="s.url" @click="openPreview(s)" />
          <div v-else class="snap-placeholder">图片暂不可用</div>
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

    <el-card class="retention-card" v-loading="retentionLoading">
      <template #header>
        <div class="card-header">
          <span>保留策略</span>
          <span class="card-desc">按保留天数与磁盘上限自动清理历史快照</span>
        </div>
      </template>
      <div class="retention-body">
        <el-form :model="retentionForm" label-width="120px" class="retention-form">
          <el-form-item label="保留天数">
            <el-input-number v-model="retentionForm.days" :min="0" :step="1" controls-position="right" />
          </el-form-item>
          <el-form-item label="磁盘上限(MB)">
            <el-input-number v-model="retentionForm.maxDiskUsageMB" :min="0" :step="100" controls-position="right" />
            <span class="field-tip">0 表示不限制</span>
          </el-form-item>
          <el-form-item label="清理间隔(秒)">
            <el-input-number v-model="retentionForm.cleanupIntervalSec" :min="0" :step="60" controls-position="right" />
          </el-form-item>
          <el-form-item label="单次上限">
            <el-input-number v-model="retentionForm.maxPerRun" :min="0" :step="50" controls-position="right" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="savingRetention" @click="saveRetention">保存策略</el-button>
          </el-form-item>
        </el-form>
        <div class="retention-status">
          <div class="status-title">清理状态</div>
          <div class="status-line"><span>上次清理</span><b>{{ retention.lastCleanupAt ? format(retention.lastCleanupAt) : '尚未执行' }}</b></div>
          <div class="status-line"><span>上次删除</span><b>{{ retention.lastDeleted ?? 0 }} 张</b></div>
          <div v-if="retention.lastError" class="status-error">上次清理失败：{{ retention.lastError }}</div>
          <div v-if="retentionError" class="status-error">策略读取失败，请稍后重试</div>
          <el-button class="cleanup-btn" :loading="cleaning" @click="cleanupNow">立即清理</el-button>
          <div class="status-tip">清理会按当前策略删除过期快照，操作不可撤销。</div>
        </div>
      </div>
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
      <img v-if="preview && !previewFailed" :src="preview" style="width: 100%" @error="previewFailed = true" />
      <el-empty v-else-if="preview" description="图片暂不可用" />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { channelApi, resolveSnapshotSrc, snapshotApi } from '../api'
import type { Channel, Snapshot, SnapshotRetention } from '../types'

const channels = ref<Channel[]>([])
const channelId = ref<number>()
const snapshots = ref<Snapshot[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 24
const loading = ref(false)
const preview = ref('')
const previewFailed = ref(false)
const previewVisible = computed({
  get: () => !!preview.value,
  set: (v: boolean) => { if (!v) preview.value = '' },
})
const configVisible = ref(false)
const config = reactive({ snapshotEnabled: false, snapshotInterval: 300 })

const retention = ref<SnapshotRetention>({
  days: 0,
  maxDiskUsageMB: 0,
  cleanupIntervalSec: 0,
  maxPerRun: 0,
})
const retentionForm = reactive({
  days: 0,
  maxDiskUsageMB: 0,
  cleanupIntervalSec: 0,
  maxPerRun: 0,
})
const retentionLoading = ref(false)
const retentionError = ref(false)
const savingRetention = ref(false)
const cleaning = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await snapshotApi.list({ channelId: channelId.value, page: page.value, pageSize })
    snapshots.value = await Promise.all(
      res.items.map(async (s) => ({ ...s, url: await resolveSnapshotSrc(s) })),
    )
    total.value = res.total
  } finally {
    loading.value = false
  }
}

async function loadRetention() {
  retentionLoading.value = true
  try {
    const r = await snapshotApi.retention()
    retention.value = r
    retentionForm.days = r.days ?? 0
    retentionForm.maxDiskUsageMB = r.maxDiskUsageMB ?? 0
    retentionForm.cleanupIntervalSec = r.cleanupIntervalSec ?? 0
    retentionForm.maxPerRun = r.maxPerRun ?? 0
    retentionError.value = false
  } catch {
    retentionError.value = true
  } finally {
    retentionLoading.value = false
  }
}

async function saveRetention() {
  savingRetention.value = true
  try {
    const r = await snapshotApi.updateRetention({ ...retentionForm })
    retention.value = { ...retention.value, ...r }
    ElMessage.success('保留策略已保存')
    loadRetention()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存策略失败')
  } finally {
    savingRetention.value = false
  }
}

async function cleanupNow() {
  try {
    await ElMessageBox.confirm('按当前保留策略立即清理过期快照？被清理的文件不可恢复。', '立即清理', { type: 'warning' })
  } catch {
    return
  }
  cleaning.value = true
  try {
    const r = await snapshotApi.cleanup()
    ElMessage.success(`已清理 ${r.deleted ?? 0} 张快照，释放 ${formatSize(r.bytes ?? 0)}`)
    loadRetention()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '清理失败')
  } finally {
    cleaning.value = false
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

function openPreview(s: Snapshot) {
  previewFailed.value = false
  preview.value = s.url
}

function format(t?: string | null) {
  return t ? new Date(t).toLocaleString() : ''
}

function formatSize(n: number) {
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

onMounted(async () => {
  channels.value = await channelApi.list()
  load()
  loadRetention()
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
.snap-placeholder {
  width: 100%;
  aspect-ratio: 16 / 9;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--el-fill-color-light);
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.snap-item .meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px;
  font-size: 12px;
  color: #64748b;
}
.retention-card {
  margin-top: 16px;
}
.card-header {
  display: flex;
  align-items: baseline;
  gap: 10px;
}
.card-desc {
  font-size: 12px;
  color: #94a3b8;
}
.retention-body {
  display: flex;
  flex-wrap: wrap;
  gap: 24px;
}
.retention-form {
  flex: 1 1 380px;
  max-width: 520px;
}
.field-tip {
  margin-left: 10px;
  font-size: 12px;
  color: #94a3b8;
}
.retention-status {
  flex: 1 1 220px;
  min-width: 210px;
  padding-left: 24px;
  border-left: 1px solid #e2e8f0;
}
.status-title {
  font-size: 14px;
  font-weight: 600;
  color: #334155;
  margin-bottom: 8px;
}
.status-line {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 4px 0;
  font-size: 13px;
  color: #64748b;
}
.status-line b {
  color: #334155;
  font-weight: 500;
  text-align: right;
}
.status-error {
  margin-top: 8px;
  font-size: 12px;
  color: #ef4444;
  line-height: 18px;
}
.cleanup-btn {
  width: 100%;
  margin-top: 14px;
}
.status-tip {
  margin-top: 8px;
  font-size: 12px;
  color: #94a3b8;
  line-height: 18px;
}
@media (max-width: 760px) {
  .retention-status {
    padding-left: 0;
    padding-top: 16px;
    border-left: none;
    border-top: 1px solid #e2e8f0;
  }
}
</style>
