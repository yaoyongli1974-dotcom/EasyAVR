<template>
  <div class="page">
    <div class="page-header">
      <h2>AI 能力接入</h2>
      <el-button type="primary" @click="openCreate">注册 AI 能力</el-button>
    </div>
    <el-alert
      title="AI 中台统一抽象三类能力：CV（检测/结构化）、VLM（视觉理解）、LLM（语义/问答）。VLM/LLM 兼容 OpenAI 接口；CV 对接外部检测服务。"
      type="info"
      :closable="false"
      style="margin-bottom: 12px"
    />
    <el-table :data="providers" border v-loading="loading">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column label="类型" width="90">
        <template #default="{ row }">
          <el-tag :type="row.kind === 'cv' ? 'warning' : row.kind === 'vlm' ? 'success' : 'primary'">{{ row.kind }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="vendor" label="厂商" width="110" />
      <el-table-column prop="endpoint" label="Endpoint" min-width="220" />
      <el-table-column prop="model" label="模型" width="140" />
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '是' : '否' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="visible" :title="editing ? '编辑能力' : '注册能力'" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.kind" style="width: 100%" @change="applyPreset">
            <el-option label="CV · 检测/结构化" value="cv" />
            <el-option label="VLM · 视觉语言" value="vlm" />
            <el-option label="LLM · 语言/语义" value="llm" />
            <el-option label="Embedding · 向量化" value="embedding" />
          </el-select>
        </el-form-item>
        <el-form-item label="厂商"><el-input v-model="form.vendor" placeholder="openai / qwen / ollama / yolo ..." /></el-form-item>
        <el-form-item label="Endpoint">
          <el-input v-model="form.endpoint" placeholder="http://127.0.0.1:8000" />
        </el-form-item>
        <el-form-item label="模型"><el-input v-model="form.model" /></el-form-item>
        <el-form-item label="API Key"><el-input v-model="form.apiKey" type="password" show-password placeholder="可留空（如 Ollama）" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="form.enabled" /></el-form-item>
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
import { aiApi } from '../api'
import type { AIProvider } from '../types'

const providers = ref<AIProvider[]>([])
const loading = ref(false)
const saving = ref(false)
const visible = ref(false)
const editing = ref<AIProvider | null>(null)
const form = reactive<Partial<AIProvider> & { apiKey?: string }>({ kind: 'vlm', enabled: true })

async function load() {
  loading.value = true
  try {
    providers.value = await aiApi.providers()
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, { name: '', kind: 'vlm', vendor: '', endpoint: '', model: '', apiKey: '', enabled: true })
  visible.value = true
}

function openEdit(row: AIProvider) {
  editing.value = row
  Object.assign(form, { ...row, apiKey: '' })
  visible.value = true
}

function applyPreset() {
  if (form.kind === 'cv' && !form.endpoint) form.endpoint = 'http://127.0.0.1:9000/detect'
  if ((form.kind === 'vlm' || form.kind === 'llm' || form.kind === 'embedding') && !form.endpoint) {
    form.endpoint = 'http://127.0.0.1:11434'
  }
}

async function save() {
  saving.value = true
  try {
    if (editing.value) await aiApi.updateProvider(editing.value.id, form)
    else await aiApi.createProvider(form)
    ElMessage.success('已保存')
    visible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function remove(row: AIProvider) {
  await ElMessageBox.confirm(`删除能力「${row.name}」？`, '确认', { type: 'warning' })
  await aiApi.removeProvider(row.id)
  load()
}

onMounted(load)
</script>
