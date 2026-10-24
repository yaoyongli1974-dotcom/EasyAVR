<template>
  <div class="page">
    <div class="page-header">
      <h2>转码 / OSD 派生流</h2>
      <div>
        <el-button @click="load">刷新</el-button>
        <el-button type="primary" @click="open()">新建派生流</el-button>
      </div>
    </div>

    <el-alert
      v-if="fontMissing"
      type="warning"
      :closable="false"
      title="系统未检测到可用字体，水印将无法渲染"
      style="margin-bottom: 10px"
    />

    <el-card v-loading="loading">
      <el-table :data="rows" border row-key="id" @expand-change="onExpand">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="detail-pane" v-loading="detailLoading[row.job.id]">
              <div class="detail-line"><span class="detail-label">流标识</span><code>{{ row.job.streamKey }}</code></div>
              <template v-if="detail(row)">
                <div class="detail-line"><span class="detail-label">播放地址</span></div>
                <div v-for="(url, key) in detail(row)!.playUrls" :key="key" class="url-row">
                  <el-tag size="small" type="info">{{ key }}</el-tag>
                  <span class="url-text">{{ url }}</span>
                  <el-button link type="primary" @click="copy(url)">复制</el-button>
                  <el-button link type="primary" @click="openUrl(url)">打开</el-button>
                </div>
                <div v-if="!Object.keys(detail(row)!.playUrls || {}).length" class="detail-line muted">
                  暂无播放地址（任务未运行或媒体服务不可用）
                </div>
              </template>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="job.id" label="ID" width="60" />
        <el-table-column prop="job.name" label="名称" min-width="140" show-overflow-tooltip />
        <el-table-column prop="channel" label="通道" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">{{ row.channel || '-' }}</template>
        </el-table-column>
        <el-table-column label="模式" width="100">
          <template #default="{ row }">
            <el-tag size="small">{{ modeText(row.job.mode) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="目标分辨率" width="110">
          <template #default="{ row }">{{ resolution(row.job) }}</template>
        </el-table-column>
        <el-table-column label="码率" width="90">
          <template #default="{ row }">{{ row.job.bitrate ? row.job.bitrate + 'k' : '-' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tooltip
              :content="row.job.lastError"
              placement="top"
              :disabled="row.job.status !== 'failed'"
            >
              <el-tag :type="statusType(row.job.status)" size="small">{{ statusText(row.job.status) }}</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="随平台启动" width="110">
          <template #default="{ row }">
            <el-tag :type="row.job.enabled ? 'success' : 'info'" size="small">{{ row.job.enabled ? '是' : '否' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <el-button
              link
              type="success"
              :loading="busy[row.job.id] === 'start'"
              :disabled="row.job.status === 'running'"
              @click="start(row)"
            >启动</el-button>
            <el-button
              link
              type="warning"
              :loading="busy[row.job.id] === 'stop'"
              :disabled="row.job.status !== 'running'"
              @click="stop(row)"
            >停止</el-button>
            <el-button link type="primary" :disabled="row.job.status === 'running'" @click="open(row.job)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>暂无派生流任务，点击「新建派生流」创建</template>
      </el-table>
    </el-card>

    <el-dialog v-model="visible" :title="editing ? '编辑派生流' : '新建派生流'" width="560px">
      <el-form :model="form" label-width="120px">
        <el-form-item label="名称"><el-input v-model="form.name" placeholder="如：主入口-水印" /></el-form-item>
        <el-form-item label="通道">
          <el-select v-model="form.channelId" filterable style="width: 100%" :disabled="!!editing">
            <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
          <div v-if="editing" class="form-hint">通道创建后不可修改</div>
        </el-form-item>
        <el-form-item label="模式">
          <el-radio-group v-model="form.mode">
            <el-radio value="transcode">转码</el-radio>
            <el-radio value="osd">水印 OSD</el-radio>
            <el-radio value="both">转码 + 水印</el-radio>
          </el-radio-group>
        </el-form-item>
        <template v-if="form.mode === 'osd' || form.mode === 'both'">
          <el-form-item label="水印文本"><el-input v-model="form.text" placeholder="叠加在画面上的文字，≤100 字" /></el-form-item>
          <el-form-item label="位置">
            <el-select v-model="form.position" style="width: 100%">
              <el-option v-for="p in positions" :key="p.value" :label="p.label" :value="p.value" />
            </el-select>
          </el-form-item>
          <el-form-item label="字号">
            <el-input-number v-model="form.fontSize" :min="10" :max="200" />
          </el-form-item>
        </template>
        <template v-if="form.mode === 'transcode' || form.mode === 'both'">
          <el-form-item label="宽度">
            <el-input-number v-model="form.width" :min="0" :step="16" />
            <div class="form-hint">分辨率留空保持原图</div>
          </el-form-item>
          <el-form-item label="高度">
            <el-input-number v-model="form.height" :min="0" :step="16" />
          </el-form-item>
          <el-form-item label="码率 (kbps)">
            <el-input-number v-model="form.bitrate" :min="0" :step="500" />
          </el-form-item>
        </template>
        <el-form-item label="随平台启动自动运行">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { channelApi, transcodeApi } from '../api'
import type { Channel, TranscodeJob, TranscodeJobRow } from '../types'

const rows = ref<TranscodeJobRow[]>([])
const channels = ref<Channel[]>([])
const loading = ref(false)
const busy = reactive<Record<number, 'start' | 'stop' | undefined>>({})
const detailLoading = reactive<Record<number, boolean>>({})
const details = reactive<Record<number, TranscodeJobRow>>({})

const visible = ref(false)
const saving = ref(false)
const editing = ref<TranscodeJob | null>(null)
const form = reactive<Partial<TranscodeJob>>({})

const positions = [
  { label: '左上 top-left', value: 'top-left' },
  { label: '右上 top-right', value: 'top-right' },
  { label: '左下 bottom-left', value: 'bottom-left' },
  { label: '右下 bottom-right', value: 'bottom-right' },
]

const fontMissing = computed(() => rows.value.some((r) => r.fontFound === false))

function detail(row: TranscodeJobRow) {
  return details[row.job.id]
}

function modeText(mode: string) {
  return { transcode: '转码', osd: '水印 OSD', both: '转码 + 水印' }[mode] || mode
}

function statusText(status: string) {
  return { stopped: '已停止', running: '运行中', failed: '失败' }[status] || status
}

function statusType(status: string) {
  return ({ stopped: 'info', running: 'success', failed: 'danger' } as const)[status] || 'info' as const
}

function resolution(job: TranscodeJob) {
  return job.width && job.height ? `${job.width}×${job.height}` : '保持原图'
}

async function load() {
  loading.value = true
  try {
    rows.value = await transcodeApi.list()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载失败')
  } finally {
    loading.value = false
  }
}

function onExpand(row: TranscodeJobRow, expanded: TranscodeJobRow[]) {
  if (!expanded.find((r) => r.job.id === row.job.id) || details[row.job.id]) return
  detailLoading[row.job.id] = true
  transcodeApi
    .get(row.job.id)
    .then((d) => {
      details[row.job.id] = d
    })
    .catch((e: any) => ElMessage.error(e?.response?.data?.message || '加载详情失败'))
    .finally(() => (detailLoading[row.job.id] = false))
}

function copy(url: string) {
  navigator.clipboard
    ? navigator.clipboard.writeText(url).then(() => ElMessage.success('已复制'))
    : ElMessage.warning('浏览器不支持复制')
}

function openUrl(url: string) {
  window.open(url, '_blank')
}

function open(job?: TranscodeJob) {
  editing.value = job || null
  if (job) {
    Object.assign(form, { ...job, channelId: job.channelId })
  } else {
    Object.assign(form, {
      id: undefined, channelId: undefined, name: '', mode: 'transcode', text: '',
      position: 'top-left', fontSize: 24, width: 0, height: 0, bitrate: 2000, enabled: true,
    })
  }
  visible.value = true
}

async function save() {
  if (!form.name) {
    ElMessage.warning('请输入任务名称')
    return
  }
  if (!form.channelId) {
    ElMessage.warning('请选择通道')
    return
  }
  if ((form.mode === 'osd' || form.mode === 'both') && !form.text) {
    ElMessage.warning('水印模式下必须填写水印文本')
    return
  }
  saving.value = true
  try {
    if (editing.value) await transcodeApi.update(editing.value.id, form)
    else await transcodeApi.create(form)
    ElMessage.success('已保存')
    visible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function start(row: TranscodeJobRow) {
  busy[row.job.id] = 'start'
  try {
    const v = await transcodeApi.start(row.job.id)
    Object.assign(row, v)
    ElMessage.success('已启动')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '启动失败')
  } finally {
    busy[row.job.id] = undefined
  }
}

async function stop(row: TranscodeJobRow) {
  busy[row.job.id] = 'stop'
  try {
    const v = await transcodeApi.stop(row.job.id)
    Object.assign(row, v)
    delete details[row.job.id]
    ElMessage.success('已停止')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '停止失败')
  } finally {
    busy[row.job.id] = undefined
  }
}

async function remove(row: TranscodeJobRow) {
  await ElMessageBox.confirm(`删除派生流任务「${row.job.name}」？`, '确认', { type: 'warning' })
  await transcodeApi.remove(row.job.id)
  ElMessage.success('已删除')
  delete details[row.job.id]
  load()
}

onMounted(async () => {
  load()
  channelApi.list().then((cs) => (channels.value = cs)).catch(() => {})
})
</script>

<style scoped>
.detail-pane {
  padding: 4px 16px 12px;
}
.detail-line {
  margin-bottom: 6px;
  font-size: 13px;
}
.detail-label {
  display: inline-block;
  width: 72px;
  color: #64748b;
}
.muted {
  color: #94a3b8;
}
.url-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
  font-size: 13px;
}
.url-text {
  color: var(--easyavr-accent);
  word-break: break-all;
}
.form-hint {
  font-size: 12px;
  color: #94a3b8;
  line-height: 18px;
}
</style>
