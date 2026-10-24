<template>
  <div class="page">
    <div class="page-header">
      <h2>AI 分析任务</h2>
      <el-button type="primary" @click="openCreate">新建任务</el-button>
    </div>
    <el-table :data="tasks" border v-loading="loading">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column label="通道" width="140">
        <template #default="{ row }">{{ channelName(row.channelId) }}</template>
      </el-table-column>
      <el-table-column label="能力" width="160">
        <template #default="{ row }">{{ providerName(row.providerId) }}</template>
      </el-table-column>
      <el-table-column prop="taskType" label="任务类型" width="130" />
      <el-table-column label="计划" width="150">
        <template #default="{ row }">{{ scheduleText(row.schedule) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'running' ? 'success' : 'info'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="300">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="success" :disabled="row.status === 'running'" @click="start(row)">启动</el-button>
          <el-button link type="warning" :disabled="row.status !== 'running'" @click="stop(row)">停止</el-button>
          <el-button link type="primary" @click="run(row)">试跑</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="visible" :title="editingId ? '编辑分析任务' : '新建分析任务'" width="560px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="通道">
          <el-select v-model="form.channelId" style="width: 100%" filterable>
            <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="AI 能力">
          <el-select v-model="form.providerId" style="width: 100%" @change="syncKind">
            <el-option v-for="p in providers" :key="p.id" :label="`${p.name} (${p.kind})`" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="任务类型">
          <el-select v-model="form.taskType" style="width: 100%">
            <el-option label="CV 目标检测" value="cv_detect" />
            <el-option label="VLM 视觉理解" value="vlm_understand" />
            <el-option label="LLM 语义分析" value="llm_analyze" />
            <el-option label="视频质量诊断" value="vqd" />
          </el-select>
        </el-form-item>
        <el-form-item label="配置 JSON">
          <el-input v-model="form.config" type="textarea" :rows="4" placeholder='{"intervalSec":30,"grabFrame":true,"prompt":"检测画面中的安全风险"}' />
        </el-form-item>
        <el-form-item label="灵敏度">
          <el-slider v-model="form.sensitivity" :min="1" :max="100" show-input />
        </el-form-item>
        <el-form-item label="分析计划">
          <el-select v-model="schedule.days" style="width: 130px; margin-right: 8px">
            <el-option label="每天" value="daily" />
            <el-option label="工作日" value="workday" />
            <el-option label="双休日" value="weekend" />
          </el-select>
          <el-input v-model="schedule.start" placeholder="00:00" style="width: 90px" />
          <span style="margin: 0 6px">-</span>
          <el-input v-model="schedule.end" placeholder="23:59" style="width: 90px" />
        </el-form-item>
        <el-form-item label="ROI 区域">
          <div class="roi-field">
            <el-input v-model="form.roi" type="textarea" :rows="2"
              placeholder='归一化矩形数组：[[0.1,0.2,0.5,0.6],[0.6,0.6,0.95,0.95]]' />
            <el-button type="primary" class="roi-open-btn" @click="openRoi">框选区域</el-button>
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="roiVisible" title="ROI 框选" width="760px" :close-on-click-modal="false" @open="initRoiWorkbench">
      <div class="roi-toolbar">
        <el-select v-model="roiChannelId" filterable style="width: 220px" placeholder="选择通道" @change="captureRoi">
          <el-option v-for="c in channels" :key="c.id" :label="c.name" :value="c.id" />
        </el-select>
        <el-button :disabled="!roiChannelId" :loading="roiLoading" @click="captureRoi">重新抓拍</el-button>
        <span v-if="roiHint" class="roi-hint roi-hint-warn">{{ roiHint }}</span>
        <span v-else class="roi-hint">在画面上拖拽绘制检测区域，可绘制多个</span>
      </div>
      <div v-if="!roiChannelId" class="roi-empty">
        <el-empty description="请先选择通道" />
      </div>
      <div v-else-if="roiFailed" class="roi-empty">
        <el-empty description="通道不在线或抓拍失败">
          <el-button type="primary" @click="captureRoi">重试</el-button>
        </el-empty>
      </div>
      <div v-show="roiLoading" class="roi-empty" v-loading="roiLoading" element-loading-text="抓拍中…"></div>
      <div v-show="roiChannelId && !roiFailed && !roiLoading" class="roi-wrap">
        <div class="roi-stage" :class="{ 'roi-shake': roiShake }">
          <img ref="roiImgEl" :src="roiUrl" class="roi-img" draggable="false" alt="通道抓拍" @load="onRoiImgLoad" @error="roiFailed = true" />
          <svg
            v-if="imgReady"
            ref="roiOverlayEl"
            class="roi-overlay"
            :viewBox="`0 0 ${natW} ${natH}`"
            @pointerdown="onOverlayDown"
            @pointermove="onOverlayMove"
            @pointerup="onOverlayUp"
            @pointercancel="onOverlayCancel"
          >
            <rect
              v-for="(r, i) in roiRects"
              :key="i"
              class="roi-rect"
              :x="r[0] * natW"
              :y="r[1] * natH"
              :width="(r[2] - r[0]) * natW"
              :height="(r[3] - r[1]) * natH"
              @pointerdown="onRectDown($event, i)"
            />
            <text
              v-for="(r, i) in roiRects"
              :key="'t' + i"
              class="roi-rect-label"
              :x="r[0] * natW + 4"
              :y="r[1] * natH + labelFontSize + 4"
            >{{ i + 1 }}</text>
            <rect
              v-if="draftRect"
              class="roi-draft"
              :x="draftRect[0] * natW"
              :y="draftRect[1] * natH"
              :width="(draftRect[2] - draftRect[0]) * natW"
              :height="(draftRect[3] - draftRect[1]) * natH"
            />
          </svg>
        </div>
      </div>
      <div v-if="roiRects.length" class="roi-list">
        <div v-for="(r, i) in roiRects" :key="i" class="roi-chip">
          <span>区域 {{ i + 1 }}</span>
          <span class="roi-chip-coord">{{ r.map((n) => n.toFixed(2)).join(', ') }}</span>
          <el-button link type="danger" size="small" @click="roiRects.splice(i, 1)">删除</el-button>
        </div>
        <el-button size="small" @click="roiRects = []">清空</el-button>
      </div>
      <template #footer>
        <el-button @click="roiVisible = false">取消</el-button>
        <el-button type="primary" @click="applyRoi">应用</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { aiApi, channelApi, snapshotApi } from '../api'
import type { AIProvider, AITask, Channel } from '../types'

const tasks = ref<AITask[]>([])
const channels = ref<Channel[]>([])
const providers = ref<AIProvider[]>([])
const loading = ref(false)
const saving = ref(false)
const visible = ref(false)
const editingId = ref<number>()
const schedule = reactive({ days: 'daily', start: '00:00', end: '23:59' })
const form = reactive<Partial<AITask>>({
  name: '',
  channelId: undefined,
  providerId: undefined,
  taskType: 'vlm_understand',
  roi: '',
  sensitivity: 50,
  config: '{"intervalSec":30,"grabFrame":true,"prompt":"检测画面中的安全风险，输出 JSON"}',
})

function scheduleText(s: string) {
  if (!s) return '全天'
  try {
    const o = JSON.parse(s)
    const d = o.days === 'workday' ? '工作日' : o.days === 'weekend' ? '双休日' : '每天'
    return `${d} ${o.start || ''}-${o.end || ''}`
  } catch {
    return '-'
  }
}

function channelName(id: number) {
  return channels.value.find((c) => c.id === id)?.name || `#${id}`
}
function providerName(id: number) {
  const p = providers.value.find((x) => x.id === id)
  return p ? `${p.name} (${p.kind})` : `#${id}`
}

async function load() {
  loading.value = true
  try {
    ;[tasks.value, channels.value, providers.value] = await Promise.all([
      aiApi.tasks(),
      channelApi.list(),
      aiApi.providers(),
    ])
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = undefined
  Object.assign(form, {
    name: '',
    channelId: undefined,
    providerId: undefined,
    taskType: 'vlm_understand',
    roi: '',
    sensitivity: 50,
    config: '{"intervalSec":30,"grabFrame":true,"prompt":"检测画面中的安全风险，输出 JSON"}',
  })
  Object.assign(schedule, { days: 'daily', start: '00:00', end: '23:59' })
  visible.value = true
}

function openEdit(row: AITask) {
  editingId.value = row.id
  Object.assign(form, {
    name: row.name,
    channelId: row.channelId,
    providerId: row.providerId,
    taskType: row.taskType,
    config: row.config,
    roi: row.roi,
    sensitivity: row.sensitivity || 50,
  })
  try {
    const o = JSON.parse(row.schedule || '{}')
    Object.assign(schedule, { days: o.days || 'daily', start: o.start || '00:00', end: o.end || '23:59' })
  } catch {
    Object.assign(schedule, { days: 'daily', start: '00:00', end: '23:59' })
  }
  visible.value = true
}

function syncKind() {
  const p = providers.value.find((x) => x.id === form.providerId)
  if (p?.kind === 'cv') form.taskType = 'cv_detect'
  else if (p?.kind === 'vlm') form.taskType = 'vlm_understand'
  else if (p?.kind === 'llm') form.taskType = 'llm_analyze'
}

async function save() {
  saving.value = true
  try {
    const payload: Partial<AITask> = {
      ...form,
      schedule: JSON.stringify(schedule),
    }
    if (editingId.value) {
      await aiApi.updateTask(editingId.value, payload)
      ElMessage.success('任务已更新')
    } else {
      await aiApi.createTask(payload)
      ElMessage.success('任务已创建')
    }
    visible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function start(row: AITask) {
  await aiApi.startTask(row.id)
  ElMessage.success('任务已启动')
  load()
}
async function stop(row: AITask) {
  await aiApi.stopTask(row.id)
  load()
}
async function run(row: AITask) {
  try {
    const res = await aiApi.runTask(row.id)
    ElMessage.success(`试跑完成，生成 ${res.count} 条事件`)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '试跑失败（检查 AI 服务与 ffmpeg）')
  }
}
async function remove(row: AITask) {
  await ElMessageBox.confirm(`删除任务「${row.name}」？`, '确认', { type: 'warning' })
  await aiApi.removeTask(row.id)
  load()
}

// ---- ROI 框选 ----

type RoiRect = [number, number, number, number]

const roiVisible = ref(false)
const roiChannelId = ref<number>()
const roiUrl = ref('')
const roiLoading = ref(false)
const roiFailed = ref(false)
const roiRects = ref<RoiRect[]>([])
const roiShake = ref(false)
const roiHint = ref('')
const imgReady = ref(false)
const natW = ref(0)
const natH = ref(0)
const roiImgEl = ref<HTMLImageElement>()
const roiOverlayEl = ref<SVGSVGElement>()
const draftRect = ref<RoiRect | null>(null)

interface RoiDrag {
  mode: 'draw' | 'move'
  idx: number
  sx: number
  sy: number
  orig: RoiRect
}
const drag = ref<RoiDrag | null>(null)

const labelFontSize = computed(() => Math.max(12, Math.round(natW.value / 45)))
let hintTimer: ReturnType<typeof setTimeout> | undefined

const clamp01 = (n: number) => Math.min(1, Math.max(0, n))

function clampRect(x1: number, y1: number, x2: number, y2: number): RoiRect {
  return [clamp01(Math.min(x1, x2)), clamp01(Math.min(y1, y2)), clamp01(Math.max(x1, x2)), clamp01(Math.max(y1, y2))]
}

function parseRoiRects(s: string | undefined): RoiRect[] {
  if (!s) return []
  try {
    const o = JSON.parse(s)
    if (!Array.isArray(o)) return []
    const out: RoiRect[] = []
    for (const item of o) {
      if (
        Array.isArray(item) &&
        item.length >= 4 &&
        item.slice(0, 4).every((n: unknown) => typeof n === 'number' && Number.isFinite(n))
      ) {
        out.push(clampRect(item[0], item[1], item[2], item[3]))
      }
    }
    return out
  } catch {
    return []
  }
}

function openRoi() {
  roiChannelId.value = form.channelId
  roiRects.value = parseRoiRects(typeof form.roi === 'string' ? form.roi : '')
  drag.value = null
  draftRect.value = null
  roiHint.value = ''
  imgReady.value = false
  roiUrl.value = ''
  roiFailed.value = false
  roiVisible.value = true
}

function initRoiWorkbench() {
  if (roiChannelId.value) captureRoi()
}

async function captureRoi() {
  if (!roiChannelId.value) return
  roiLoading.value = true
  roiFailed.value = false
  imgReady.value = false
  try {
    const s = await snapshotApi.capture(roiChannelId.value)
    roiUrl.value = `${s.url}${s.url.includes('?') ? '&' : '?'}t=${Date.now()}`
  } catch {
    roiFailed.value = true
    roiUrl.value = ''
  } finally {
    roiLoading.value = false
  }
}

function onRoiImgLoad() {
  const img = roiImgEl.value
  if (!img || !img.naturalWidth || !img.naturalHeight) {
    roiFailed.value = true
    return
  }
  natW.value = img.naturalWidth
  natH.value = img.naturalHeight
  imgReady.value = true
}

function normFromEvent(e: PointerEvent): [number, number] {
  const el = roiOverlayEl.value
  if (!el) return [0, 0]
  const r = el.getBoundingClientRect()
  return [clamp01((e.clientX - r.left) / (r.width || 1)), clamp01((e.clientY - r.top) / (r.height || 1))]
}

function capturePointer(e: PointerEvent) {
  try {
    ;(e.currentTarget as Element).setPointerCapture(e.pointerId)
  } catch {
    /* 指针失效时忽略 */
  }
}

function onOverlayDown(e: PointerEvent) {
  if (!imgReady.value || roiFailed.value) return
  const [u, v] = normFromEvent(e)
  drag.value = { mode: 'draw', idx: -1, sx: u, sy: v, orig: [u, v, u, v] }
  draftRect.value = [u, v, u, v]
  capturePointer(e)
}

function onRectDown(e: PointerEvent, idx: number) {
  e.stopPropagation()
  const r = roiRects.value[idx]
  if (!r) return
  const [u, v] = normFromEvent(e)
  drag.value = { mode: 'move', idx, sx: u, sy: v, orig: [...r] as RoiRect }
  capturePointer(e)
}

function onOverlayMove(e: PointerEvent) {
  const d = drag.value
  if (!d) return
  const [u, v] = normFromEvent(e)
  if (d.mode === 'draw') {
    draftRect.value = [d.sx, d.sy, u, v]
    return
  }
  const r = roiRects.value[d.idx]
  if (!r) return
  const w = d.orig[2] - d.orig[0]
  const h = d.orig[3] - d.orig[1]
  const nx = Math.min(clamp01(d.orig[0] + (u - d.sx)), 1 - w)
  const ny = Math.min(clamp01(d.orig[1] + (v - d.sy)), 1 - h)
  r[0] = nx
  r[1] = ny
  r[2] = nx + w
  r[3] = ny + h
}

function rejectDraft() {
  roiShake.value = true
  roiHint.value = '区域太小，已忽略'
  if (hintTimer) clearTimeout(hintTimer)
  hintTimer = setTimeout(() => {
    roiShake.value = false
    roiHint.value = ''
  }, 1400)
}

function onOverlayUp() {
  const d = drag.value
  drag.value = null
  if (!d) return
  if (d.mode !== 'draw') return
  const dr = draftRect.value
  draftRect.value = null
  if (!dr) return
  const w = Math.abs(dr[2] - dr[0])
  const h = Math.abs(dr[3] - dr[1])
  if (w < 0.01 || h < 0.01) {
    rejectDraft()
    return
  }
  roiRects.value.push(clampRect(dr[0], dr[1], dr[2], dr[3]))
}

function onOverlayCancel() {
  drag.value = null
  draftRect.value = null
}

function applyRoi() {
  const arr = roiRects.value.map((r) => r.map((n) => Number(n.toFixed(2))))
  form.roi = JSON.stringify(arr)
  roiVisible.value = false
  ElMessage.success(arr.length ? `已应用 ${arr.length} 个区域` : '已清空 ROI 区域')
}


onMounted(load)
</script>

<style scoped>
.roi-field {
  width: 100%;
}
.roi-open-btn {
  margin-top: 8px;
}
.roi-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.roi-hint {
  margin-left: auto;
  font-size: 12px;
  color: #64748b;
}
.roi-hint-warn {
  color: #e6a23c;
}
.roi-empty {
  min-height: 288px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.roi-wrap {
  display: flex;
  justify-content: center;
}
.roi-stage {
  position: relative;
  display: inline-block;
  max-width: 100%;
  line-height: 0;
  user-select: none;
  -webkit-user-select: none;
}
.roi-img {
  display: block;
  max-width: 100%;
  max-height: 58vh;
  border-radius: 6px;
}
.roi-overlay {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  cursor: crosshair;
  touch-action: none;
}
.roi-rect {
  fill: rgba(47, 124, 246, 0.16);
  stroke: var(--easyavr-accent, #2f7cf6);
  stroke-width: 2;
  vector-effect: non-scaling-stroke;
  cursor: move;
}
.roi-rect-label {
  fill: var(--easyavr-accent, #2f7cf6);
  font-size: 16px;
  font-weight: 600;
  pointer-events: none;
}
.roi-draft {
  fill: rgba(47, 124, 246, 0.1);
  stroke: var(--easyavr-accent, #2f7cf6);
  stroke-width: 2;
  stroke-dasharray: 6 4;
  vector-effect: non-scaling-stroke;
  pointer-events: none;
}
.roi-list {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
}
.roi-chip {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  border: 1px solid #dbe3ef;
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 12px;
}
.roi-chip-coord {
  color: #64748b;
  font-family: 'SFMono-Regular', Consolas, monospace;
}
.roi-shake {
  animation: roi-shake 0.35s ease-in-out;
}
@keyframes roi-shake {
  0%,
  100% {
    transform: translateX(0);
  }
  20% {
    transform: translateX(-6px);
  }
  40% {
    transform: translateX(6px);
  }
  60% {
    transform: translateX(-4px);
  }
  80% {
    transform: translateX(4px);
  }
}
</style>
