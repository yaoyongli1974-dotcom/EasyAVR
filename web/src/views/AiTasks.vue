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
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'running' ? 'success' : 'info'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="240">
        <template #default="{ row }">
          <el-button link type="success" :disabled="row.status === 'running'" @click="start(row)">启动</el-button>
          <el-button link type="warning" :disabled="row.status !== 'running'" @click="stop(row)">停止</el-button>
          <el-button link type="primary" @click="run(row)">试跑</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="visible" title="新建分析任务" width="560px">
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
          <el-input v-model="form.config" type="textarea" :rows="5" placeholder='{"intervalSec":30,"grabFrame":true,"prompt":"检测画面中的安全风险"}' />
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
const form = reactive<Partial<AITask>>({
  name: '',
  channelId: undefined,
  providerId: undefined,
  taskType: 'vlm_understand',
  config: '{"intervalSec":30,"grabFrame":true,"prompt":"检测画面中的安全风险，输出 JSON"}',
})

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
  Object.assign(form, {
    name: '',
    channelId: undefined,
    providerId: undefined,
    taskType: 'vlm_understand',
    config: '{"intervalSec":30,"grabFrame":true,"prompt":"检测画面中的安全风险，输出 JSON"}',
  })
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
    await aiApi.createTask(form)
    ElMessage.success('任务已创建')
    visible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '创建失败')
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
