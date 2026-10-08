<template>
  <div class="page">
    <div class="page-header">
      <h2>模型管理</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-row :gutter="16" class="stats">
      <el-col :span="6"><el-card><div class="stat"><div class="num">{{ stats.models }}</div><div class="lbl">模型</div></div></el-card></el-col>
      <el-col :span="6"><el-card><div class="stat"><div class="num">{{ stats.versions }}</div><div class="lbl">版本</div></div></el-card></el-col>
      <el-col :span="6"><el-card><div class="stat"><div class="num">{{ stats.deployments }}</div><div class="lbl">部署</div></div></el-card></el-col>
      <el-col :span="6"><el-card><div class="stat"><div class="num">{{ stats.active }}</div><div class="lbl">运行中</div></div></el-card></el-col>
    </el-row>

    <el-tabs v-model="tab">
      <el-tab-pane label="模型" name="models">
        <el-card>
          <template #header>
            <span>模型注册表</span>
            <el-button type="primary" size="small" style="float: right" @click="openModel()">注册模型</el-button>
          </template>
          <el-table :data="models" border>
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column label="类型" width="110">
              <template #default="{ row }"><el-tag>{{ row.kind }}</el-tag></template>
            </el-table-column>
            <el-table-column prop="task" label="任务" width="120" />
            <el-table-column prop="framework" label="框架" width="110" />
            <el-table-column label="最新版本" width="130">
              <template #default="{ row }">
                <span v-if="row.latestVersion">{{ row.latestVersion }} <el-tag size="small" type="info">{{ row.versionCount }}</el-tag></span>
                <span v-else>-</span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="220">
              <template #default="{ row }">
                <el-button link type="primary" @click="openVersions(row)">版本</el-button>
                <el-button link type="primary" @click="openModel(row)">编辑</el-button>
                <el-button link type="danger" @click="removeModel(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="部署" name="deployments">
        <el-card>
          <template #header>
            <span>模型部署</span>
            <el-button type="primary" size="small" style="float: right" @click="openDeploy()">新建部署</el-button>
          </template>
          <el-table :data="deployments" border>
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column prop="modelName" label="模型" min-width="120" />
            <el-table-column prop="version" label="版本" width="110" />
            <el-table-column prop="providerName" label="运行时" min-width="120" />
            <el-table-column prop="replicas" label="副本" width="70" />
            <el-table-column label="状态" width="100">
              <template #default="{ row }">
                <el-tag :type="row.status === 'active' ? 'success' : row.status === 'failed' ? 'danger' : 'info'">{{ row.status }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="健康" width="90">
              <template #default="{ row }">
                <el-tag :type="row.health === 'healthy' ? 'success' : row.health === 'unhealthy' ? 'danger' : 'info'">{{ row.health }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="200">
              <template #default="{ row }">
                <el-button v-if="row.status !== 'active'" link type="success" @click="activate(row)">启用</el-button>
                <el-button v-else link type="warning" @click="stop(row)">停止</el-button>
                <el-button link type="danger" @click="removeDeploy(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="modelVisible" :title="editModel ? '编辑模型' : '注册模型'" width="560px">
      <el-form :model="modelForm" label-width="90px">
        <el-form-item label="名称"><el-input v-model="modelForm.name" /></el-form-item>
        <el-form-item label="类型">
          <el-select v-model="modelForm.kind" style="width: 100%">
            <el-option v-for="k in KINDS" :key="k" :label="k" :value="k" />
          </el-select>
        </el-form-item>
        <el-form-item label="任务"><el-input v-model="modelForm.task" placeholder="detection / classification / chat / embedding" /></el-form-item>
        <el-form-item label="框架"><el-input v-model="modelForm.framework" placeholder="onnx / pytorch / tensorrt / api" /></el-form-item>
        <el-form-item label="来源"><el-input v-model="modelForm.source" placeholder="local / remote" /></el-form-item>
        <el-form-item label="标签"><el-input v-model="modelForm.tags" placeholder="逗号分隔" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="modelForm.description" type="textarea" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="modelForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="modelVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveModel">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="versionsVisible" :title="`版本管理 - ${currentModel?.name || ''}`" width="820px">
      <el-table :data="versions" border size="small">
        <el-table-column prop="version" label="版本" width="110" />
        <el-table-column prop="status" label="状态" width="110">
          <template #default="{ row }"><el-tag :type="row.status === 'deployed' ? 'success' : row.status === 'archived' ? 'info' : 'warning'">{{ row.status }}</el-tag></template>
        </el-table-column>
        <el-table-column prop="format" label="格式" width="90" />
        <el-table-column label="大小" width="100">
          <template #default="{ row }">{{ row.sizeBytes ? (row.sizeBytes / 1048576).toFixed(1) + ' MB' : '-' }}</template>
        </el-table-column>
        <el-table-column prop="notes" label="备注" min-width="140" />
        <el-table-column label="操作" width="180">
          <template #default="{ row }">
            <el-button link type="warning" :disabled="row.status === 'archived'" @click="archiveVersion(row)">归档</el-button>
            <el-button link type="danger" @click="removeVersion(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-divider>注册新版本</el-divider>
      <el-form :model="versionForm" label-width="90px">
        <el-row :gutter="12">
          <el-col :span="12"><el-form-item label="版本号"><el-input v-model="versionForm.version" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="格式"><el-input v-model="versionForm.format" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="大小(字节)"><el-input-number v-model="versionForm.sizeBytes" :min="0" style="width: 100%" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="校验和"><el-input v-model="versionForm.checksum" /></el-form-item></el-col>
          <el-col :span="24"><el-form-item label="指标(JSON)"><el-input v-model="versionForm.metrics" type="textarea" :rows="2" placeholder='{"mAP":0.9}' /></el-form-item></el-col>
          <el-col :span="24"><el-form-item label="标签(JSON)"><el-input v-model="versionForm.labels" placeholder='["person","car"]' /></el-form-item></el-col>
          <el-col :span="24"><el-form-item label="备注"><el-input v-model="versionForm.notes" /></el-form-item></el-col>
        </el-row>
      </el-form>
      <template #footer>
        <el-button @click="versionsVisible = false">关闭</el-button>
        <el-button type="primary" :loading="saving" @click="saveVersion">注册版本</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="deployVisible" title="新建部署" width="560px">
      <el-form :model="deployForm" label-width="90px">
        <el-form-item label="模型">
          <el-select v-model="deployForm.modelId" style="width: 100%" @change="onDeployModelChange">
            <el-option v-for="m in models" :key="m.id" :label="m.name" :value="m.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="版本">
          <el-select v-model="deployForm.versionId" style="width: 100%">
            <el-option v-for="v in deployVersions" :key="v.id" :label="`${v.version} (${v.status})`" :value="v.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="运行时">
          <el-select v-model="deployForm.providerId" style="width: 100%">
            <el-option v-for="p in providers" :key="p.id" :label="`${p.name} (${p.kind})`" :value="p.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称"><el-input v-model="deployForm.name" placeholder="留空自动生成" /></el-form-item>
        <el-form-item label="副本数"><el-input-number v-model="deployForm.replicas" :min="1" /></el-form-item>
        <el-form-item label="配置(JSON)"><el-input v-model="deployForm.config" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="deployVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveDeploy">部署</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { aiApi, modelApi } from '../api'
import type { AIModel, AIModelDeployment, AIModelVersion, AIProvider } from '../types'

const KINDS = ['cv', 'vlm', 'llm', 'embedding']

const tab = ref('models')
const saving = ref(false)
const models = ref<AIModel[]>([])
const deployments = ref<AIModelDeployment[]>([])
const providers = ref<AIProvider[]>([])
const stats = reactive({ models: 0, versions: 0, deployments: 0, active: 0 })

const modelVisible = ref(false)
const versionsVisible = ref(false)
const deployVisible = ref(false)
const editModel = ref<AIModel | null>(null)
const currentModel = ref<AIModel | null>(null)
const versions = ref<AIModelVersion[]>([])
const deployVersions = ref<AIModelVersion[]>([])

const modelForm = reactive({ name: '', kind: 'cv', task: '', framework: '', source: 'local', tags: '', description: '', enabled: true })
const versionForm = reactive({ version: '', format: '', sizeBytes: 0, checksum: '', metrics: '', labels: '', notes: '' })
const deployForm = reactive({ modelId: 0, versionId: 0, providerId: 0, name: '', replicas: 1, config: '' })

async function load() {
  models.value = await modelApi.list()
  deployments.value = await modelApi.deployments()
  Object.assign(stats, await modelApi.stats())
}

function openModel(row?: AIModel) {
  editModel.value = row || null
  Object.assign(modelForm, row
    ? { name: row.name, kind: row.kind, task: row.task, framework: row.framework, source: row.source, tags: row.tags, description: row.description, enabled: row.enabled }
    : { name: '', kind: 'cv', task: '', framework: '', source: 'local', tags: '', description: '', enabled: true })
  modelVisible.value = true
}

async function saveModel() {
  if (!modelForm.name) {
    ElMessage.warning('请输入模型名称')
    return
  }
  saving.value = true
  try {
    if (editModel.value) await modelApi.update(editModel.value.id, { ...modelForm } as any)
    else await modelApi.create({ ...modelForm } as any)
    ElMessage.success('已保存')
    modelVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function removeModel(row: AIModel) {
  await ElMessageBox.confirm(`删除模型「${row.name}」及其版本与部署？`, '确认', { type: 'warning' })
  try {
    await modelApi.remove(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

async function openVersions(row: AIModel) {
  currentModel.value = row
  Object.assign(versionForm, { version: '', format: '', sizeBytes: 0, checksum: '', metrics: '', labels: '', notes: '' })
  versions.value = await modelApi.versions(row.id)
  versionsVisible.value = true
}

async function saveVersion() {
  if (!currentModel.value) return
  if (!versionForm.version) {
    ElMessage.warning('请输入版本号')
    return
  }
  saving.value = true
  try {
    await modelApi.createVersion(currentModel.value.id, versionForm)
    ElMessage.success('已注册')
    versions.value = await modelApi.versions(currentModel.value.id)
    Object.assign(versionForm, { version: '', format: '', sizeBytes: 0, checksum: '', metrics: '', labels: '', notes: '' })
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '注册失败')
  } finally {
    saving.value = false
  }
}

async function archiveVersion(row: AIModelVersion) {
  if (!currentModel.value) return
  try {
    await modelApi.archiveVersion(currentModel.value.id, row.id)
    versions.value = await modelApi.versions(currentModel.value.id)
    ElMessage.success('已归档')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '归档失败')
  }
}

async function removeVersion(row: AIModelVersion) {
  if (!currentModel.value) return
  await ElMessageBox.confirm(`删除版本「${row.version}」？`, '确认', { type: 'warning' })
  try {
    await modelApi.removeVersion(currentModel.value.id, row.id)
    versions.value = await modelApi.versions(currentModel.value.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

function openDeploy() {
  Object.assign(deployForm, { modelId: 0, versionId: 0, providerId: 0, name: '', replicas: 1, config: '' })
  deployVersions.value = []
  deployVisible.value = true
}

async function onDeployModelChange(id: number) {
  deployForm.versionId = 0
  deployVersions.value = id ? await modelApi.versions(id) : []
}

async function saveDeploy() {
  if (!deployForm.modelId || !deployForm.versionId || !deployForm.providerId) {
    ElMessage.warning('请选择模型、版本与运行时')
    return
  }
  saving.value = true
  try {
    await modelApi.createDeployment({ ...deployForm })
    ElMessage.success('已部署')
    deployVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '部署失败')
  } finally {
    saving.value = false
  }
}

async function activate(row: AIModelDeployment) {
  await modelApi.activateDeployment(row.id)
  ElMessage.success('已启用')
  load()
}
async function stop(row: AIModelDeployment) {
  await modelApi.stopDeployment(row.id)
  ElMessage.success('已停止')
  load()
}
async function removeDeploy(row: AIModelDeployment) {
  await ElMessageBox.confirm(`删除部署「${row.name}」？`, '确认', { type: 'warning' })
  try {
    await modelApi.removeDeployment(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

onMounted(async () => {
  providers.value = await aiApi.providers()
  load()
})
</script>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.stats {
  margin-bottom: 16px;
}
.stat {
  text-align: center;
}
.stat .num {
  font-size: 26px;
  font-weight: 700;
  color: #1e293b;
}
.stat .lbl {
  color: #64748b;
  font-size: 13px;
}
</style>
