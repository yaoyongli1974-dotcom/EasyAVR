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
          <el-input v-model="form.roi" type="textarea" :rows="2"
            placeholder='归一化多边形：[[0.1,0.1],[0.9,0.1],[0.9,0.9],[0.1,0.9]]' />
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
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { aiApi, channelApi } from '../api'
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

onMounted(load)
</script>
