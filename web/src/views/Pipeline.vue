<template>
  <div class="page">
    <div class="page-header">
      <h2>数据与训练</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-row :gutter="16" class="stats">
      <el-col :span="4"><el-card><div class="stat"><div class="num">{{ stats.datasets }}</div><div class="lbl">数据集</div></div></el-card></el-col>
      <el-col :span="4"><el-card><div class="stat"><div class="num">{{ stats.samples }}</div><div class="lbl">样本</div></div></el-card></el-col>
      <el-col :span="4"><el-card><div class="stat"><div class="num">{{ stats.labeled }}</div><div class="lbl">已标注</div></div></el-card></el-col>
      <el-col :span="4"><el-card><div class="stat"><div class="num">{{ stats.annotations }}</div><div class="lbl">标注任务</div></div></el-card></el-col>
      <el-col :span="4"><el-card><div class="stat"><div class="num">{{ stats.jobs }}</div><div class="lbl">训练作业</div></div></el-card></el-col>
      <el-col :span="4"><el-card><div class="stat"><div class="num">{{ stats.activeJobs }}</div><div class="lbl">进行中</div></div></el-card></el-col>
    </el-row>

    <el-tabs v-model="tab" @tab-change="load">
      <el-tab-pane label="数据集" name="datasets">
        <el-card>
          <template #header>
            <span>数据集</span>
            <el-button type="primary" size="small" style="float: right" @click="openDataset()">新建数据集</el-button>
          </template>
          <el-table :data="datasets" border>
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column prop="kind" label="类型" width="90" />
            <el-table-column prop="source" label="来源" width="100" />
            <el-table-column prop="labels" label="类别" min-width="160" show-overflow-tooltip />
            <el-table-column label="样本" width="120">
              <template #default="{ row }">{{ row.labeledCount }} / {{ row.sampleCount }}</template>
            </el-table-column>
            <el-table-column label="状态" width="100">
              <template #default="{ row }"><el-tag :type="row.status === 'ready' ? 'success' : 'info'" size="small">{{ row.status }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="220">
              <template #default="{ row }">
                <el-button link type="primary" @click="openSamples(row)">样本</el-button>
                <el-button link type="primary" @click="openDataset(row)">编辑</el-button>
                <el-button link type="danger" @click="removeDataset(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="标注任务" name="annotations">
        <el-card>
          <template #header>
            <span>标注任务</span>
            <el-button type="primary" size="small" style="float: right" @click="openAnnotation()">新建任务</el-button>
          </template>
          <el-table :data="annotations" border>
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column label="数据集" min-width="120">
              <template #default="{ row }">{{ datasetName(row.datasetId) }}</template>
            </el-table-column>
            <el-table-column prop="assignee" label="负责人" width="120" />
            <el-table-column label="进度" min-width="200">
              <template #default="{ row }">
                <el-progress :percentage="row.total ? Math.round((row.labeled / row.total) * 100) : 0" />
                <span class="muted">{{ row.labeled }} / {{ row.total }}</span>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="110">
              <template #default="{ row }"><el-tag :type="row.status === 'completed' ? 'success' : 'warning'" size="small">{{ row.status }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="150">
              <template #default="{ row }">
                <el-button link type="success" :disabled="row.status === 'completed'" @click="completeAnnotation(row)">完成</el-button>
                <el-button link type="danger" @click="removeAnnotation(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="训练作业" name="training">
        <el-card>
          <template #header>
            <span>训练作业</span>
            <el-button type="primary" size="small" style="float: right" @click="openJob()">新建作业</el-button>
          </template>
          <el-table :data="jobs" border>
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column label="数据集" min-width="120">
              <template #default="{ row }">{{ datasetName(row.datasetId) }}</template>
            </el-table-column>
            <el-table-column prop="framework" label="框架" width="110" />
            <el-table-column label="状态" width="110">
              <template #default="{ row }"><el-tag :type="jobTag(row.status)" size="small">{{ row.status }}</el-tag></template>
            </el-table-column>
            <el-table-column prop="versionId" label="产出模型版本" width="120" />
            <el-table-column prop="metrics" label="指标" min-width="160" show-overflow-tooltip />
            <el-table-column label="操作" width="200">
              <template #default="{ row }">
                <el-button link type="success" :disabled="row.status === 'running'" @click="runJob(row)">运行</el-button>
                <el-button link type="warning" :disabled="row.status === 'succeeded'" @click="cancelJob(row)">取消</el-button>
                <el-button link type="danger" @click="removeJob(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="datasetVisible" :title="editDataset ? '编辑数据集' : '新建数据集'" width="560px">
      <el-form :model="datasetForm" label-width="90px">
        <el-form-item label="名称"><el-input v-model="datasetForm.name" /></el-form-item>
        <el-form-item label="类型"><el-input v-model="datasetForm.kind" placeholder="cv / detection / classification" /></el-form-item>
        <el-form-item label="来源"><el-input v-model="datasetForm.source" placeholder="manual / events / snapshots" /></el-form-item>
        <el-form-item label="类别(JSON)"><el-input v-model="datasetForm.labels" type="textarea" :rows="2" placeholder='["person","car"]' /></el-form-item>
        <el-form-item label="描述"><el-input v-model="datasetForm.description" /></el-form-item>
        <el-form-item label="状态">
          <el-select v-model="datasetForm.status" style="width: 100%">
            <el-option label="draft" value="draft" />
            <el-option label="ready" value="ready" />
            <el-option label="archived" value="archived" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="datasetVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveDataset">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="samplesVisible" :title="`样本 - ${currentDataset?.name || ''}`" width="900px">
      <div style="margin-bottom: 12px; display: flex; gap: 8px">
        <el-button size="small" @click="openSample()">新增样本</el-button>
        <el-button size="small" @click="openImport">从事件导入</el-button>
      </div>
      <el-table :data="samples" border size="small" max-height="420">
        <el-table-column label="图片" width="90">
          <template #default="{ row }">
            <img v-if="row.imageUrl" :src="row.imageUrl" style="height: 40px; border-radius: 4px" />
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column prop="eventId" label="事件" width="80" />
        <el-table-column prop="channelId" label="通道" width="80" />
        <el-table-column prop="labels" label="标注" min-width="180" show-overflow-tooltip />
        <el-table-column prop="split" label="划分" width="80" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }"><el-tag :type="row.status === 'unlabeled' ? 'info' : 'success'" size="small">{{ row.status }}</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" width="130">
          <template #default="{ row }">
            <el-button link type="primary" @click="openSample(row)">标注</el-button>
            <el-button link type="danger" @click="removeSample(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <template #footer><el-button @click="samplesVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="sampleVisible" title="样本标注" width="520px">
      <el-form :model="sampleForm" label-width="90px">
        <el-form-item label="图片地址"><el-input v-model="sampleForm.imageUrl" /></el-form-item>
        <el-form-item label="标注(JSON)"><el-input v-model="sampleForm.labels" type="textarea" :rows="3" placeholder='{"class":"person","bbox":[x,y,w,h]}' /></el-form-item>
        <el-form-item label="划分">
          <el-select v-model="sampleForm.split" style="width: 100%">
            <el-option label="train" value="train" />
            <el-option label="val" value="val" />
            <el-option label="test" value="test" />
          </el-select>
        </el-form-item>
        <el-form-item label="状态">
          <el-select v-model="sampleForm.status" style="width: 100%">
            <el-option label="unlabeled" value="unlabeled" />
            <el-option label="labeled" value="labeled" />
            <el-option label="reviewed" value="reviewed" />
          </el-select>
        </el-form-item>
        <el-form-item label="备注"><el-input v-model="sampleForm.note" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="sampleVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveSample">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="importVisible" title="从事件导入样本" width="620px">
      <el-form label-width="90px">
        <el-form-item label="划分">
          <el-select v-model="importSplit" style="width: 100%">
            <el-option label="train" value="train" />
            <el-option label="val" value="val" />
            <el-option label="test" value="test" />
          </el-select>
        </el-form-item>
        <el-form-item label="选择事件">
          <el-select v-model="importEventIds" multiple filterable style="width: 100%" placeholder="选择要加入数据集的 AI 事件">
            <el-option v-for="e in recentEvents" :key="e.id" :label="`#${e.id} ${e.eventType} (${e.level})`" :value="e.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="doImport">导入</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="annotationVisible" title="新建标注任务" width="560px">
      <el-form :model="annotationForm" label-width="90px">
        <el-form-item label="名称"><el-input v-model="annotationForm.name" /></el-form-item>
        <el-form-item label="数据集">
          <el-select v-model="annotationForm.datasetId" style="width: 100%">
            <el-option v-for="d in datasets" :key="d.id" :label="d.name" :value="d.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="负责人"><el-input v-model="annotationForm.assignee" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="annotationForm.instructions" type="textarea" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="annotationVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveAnnotation">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="jobVisible" title="新建训练作业" width="560px">
      <el-form :model="jobForm" label-width="90px">
        <el-form-item label="名称"><el-input v-model="jobForm.name" /></el-form-item>
        <el-form-item label="数据集">
          <el-select v-model="jobForm.datasetId" style="width: 100%">
            <el-option v-for="d in datasets" :key="d.id" :label="d.name" :value="d.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="框架"><el-input v-model="jobForm.framework" placeholder="pytorch / tensorflow / paddle" /></el-form-item>
        <el-form-item label="超参(JSON)"><el-input v-model="jobForm.hyperParams" type="textarea" :rows="2" placeholder='{"epochs":50,"batch":16}' /></el-form-item>
        <el-form-item label="指标(JSON)"><el-input v-model="jobForm.metrics" type="textarea" :rows="2" placeholder='{"mAP":0.85}' /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="jobVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveJob">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { eventApi, pipelineApi } from '../api'
import type { AIEvent, AnnotationTask, Dataset, DatasetSample, PipelineStats, TrainingJob } from '../types'

const tab = ref('datasets')
const saving = ref(false)
const datasets = ref<Dataset[]>([])
const samples = ref<DatasetSample[]>([])
const annotations = ref<AnnotationTask[]>([])
const jobs = ref<TrainingJob[]>([])
const recentEvents = ref<AIEvent[]>([])
const stats = reactive<PipelineStats>({ datasets: 0, samples: 0, labeled: 0, annotations: 0, jobs: 0, activeJobs: 0 })

const datasetVisible = ref(false)
const editDataset = ref<Dataset | null>(null)
const datasetForm = reactive({ name: '', kind: '', source: 'manual', labels: '', description: '', status: 'draft' })

const samplesVisible = ref(false)
const currentDataset = ref<Dataset | null>(null)
const sampleVisible = ref(false)
const editSample = ref<DatasetSample | null>(null)
const sampleForm = reactive({ imageUrl: '', labels: '', split: 'train', status: 'unlabeled', note: '' })

const importVisible = ref(false)
const importEventIds = ref<number[]>([])
const importSplit = ref('train')

const annotationVisible = ref(false)
const annotationForm = reactive({ name: '', datasetId: 0, assignee: '', instructions: '' })

const jobVisible = ref(false)
const jobForm = reactive({ name: '', datasetId: 0, framework: '', hyperParams: '', metrics: '' })

function datasetName(id: number) {
  return datasets.value.find(d => d.id === id)?.name || `#${id}`
}
function jobTag(status: string) {
  return status === 'succeeded' ? 'success' : status === 'failed' ? 'danger' : status === 'running' ? 'warning' : 'info'
}

async function load() {
  Object.assign(stats, await pipelineApi.stats())
  datasets.value = await pipelineApi.datasets()
  if (tab.value === 'annotations') annotations.value = await pipelineApi.annotations()
  if (tab.value === 'training') jobs.value = await pipelineApi.jobs()
}

function openDataset(row?: Dataset) {
  editDataset.value = row || null
  Object.assign(datasetForm, row
    ? { name: row.name, kind: row.kind, source: row.source, labels: row.labels, description: row.description, status: row.status }
    : { name: '', kind: '', source: 'manual', labels: '', description: '', status: 'draft' })
  datasetVisible.value = true
}

async function saveDataset() {
  if (!datasetForm.name) {
    ElMessage.warning('请输入数据集名称')
    return
  }
  saving.value = true
  try {
    if (editDataset.value) await pipelineApi.updateDataset(editDataset.value.id, datasetForm)
    else await pipelineApi.createDataset(datasetForm)
    ElMessage.success('已保存')
    datasetVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function removeDataset(row: Dataset) {
  await ElMessageBox.confirm(`删除数据集「${row.name}」及其样本与标注任务？`, '确认', { type: 'warning' })
  try {
    await pipelineApi.removeDataset(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

async function openSamples(row: Dataset) {
  currentDataset.value = row
  samples.value = await pipelineApi.samples(row.id)
  samplesVisible.value = true
}

function openSample(row?: DatasetSample) {
  editSample.value = row || null
  Object.assign(sampleForm, row
    ? { imageUrl: row.imageUrl, labels: row.labels, split: row.split, status: row.status, note: row.note }
    : { imageUrl: '', labels: '', split: 'train', status: 'unlabeled', note: '' })
  sampleVisible.value = true
}

async function saveSample() {
  if (!currentDataset.value) return
  saving.value = true
  try {
    if (editSample.value) await pipelineApi.updateSample(currentDataset.value.id, editSample.value.id, sampleForm)
    else await pipelineApi.createSample(currentDataset.value.id, sampleForm)
    samples.value = await pipelineApi.samples(currentDataset.value.id)
    sampleVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function removeSample(row: DatasetSample) {
  if (!currentDataset.value) return
  await ElMessageBox.confirm('删除该样本？', '确认', { type: 'warning' })
  await pipelineApi.removeSample(currentDataset.value.id, row.id)
  samples.value = await pipelineApi.samples(currentDataset.value.id)
  load()
}

async function openImport() {
  importEventIds.value = []
  importSplit.value = 'train'
  try {
    const r = await eventApi.list({ page: 1, pageSize: 50 })
    recentEvents.value = r.items
  } catch {
    recentEvents.value = []
  }
  importVisible.value = true
}

async function doImport() {
  if (!currentDataset.value || importEventIds.value.length === 0) {
    ElMessage.warning('请选择事件')
    return
  }
  saving.value = true
  try {
    const r = await pipelineApi.importSamples(currentDataset.value.id, { eventIds: importEventIds.value, split: importSplit.value })
    ElMessage.success(`已导入 ${r.created} 条样本`)
    samples.value = await pipelineApi.samples(currentDataset.value.id)
    importVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '导入失败')
  } finally {
    saving.value = false
  }
}

function openAnnotation() {
  Object.assign(annotationForm, { name: '', datasetId: datasets.value[0]?.id || 0, assignee: '', instructions: '' })
  annotationVisible.value = true
}

async function saveAnnotation() {
  if (!annotationForm.name || !annotationForm.datasetId) {
    ElMessage.warning('请填写名称并选择数据集')
    return
  }
  saving.value = true
  try {
    await pipelineApi.createAnnotation(annotationForm)
    ElMessage.success('已创建')
    annotationVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '创建失败')
  } finally {
    saving.value = false
  }
}

async function completeAnnotation(row: AnnotationTask) {
  await pipelineApi.completeAnnotation(row.id)
  ElMessage.success('已完成')
  load()
}
async function removeAnnotation(row: AnnotationTask) {
  await ElMessageBox.confirm(`删除任务「${row.name}」？`, '确认', { type: 'warning' })
  await pipelineApi.removeAnnotation(row.id)
  load()
}

function openJob() {
  Object.assign(jobForm, { name: '', datasetId: datasets.value[0]?.id || 0, framework: '', hyperParams: '', metrics: '' })
  jobVisible.value = true
}

async function saveJob() {
  if (!jobForm.name || !jobForm.datasetId) {
    ElMessage.warning('请填写名称并选择数据集')
    return
  }
  saving.value = true
  try {
    await pipelineApi.createJob(jobForm)
    ElMessage.success('已创建')
    jobVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '创建失败')
  } finally {
    saving.value = false
  }
}

async function runJob(row: TrainingJob) {
  try {
    const r = await pipelineApi.runJob(row.id)
    ElMessage.success(`训练完成，产出模型版本 ${r.version.version}`)
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '训练失败')
  }
}
async function cancelJob(row: TrainingJob) {
  await pipelineApi.cancelJob(row.id)
  ElMessage.success('已取消')
  load()
}
async function removeJob(row: TrainingJob) {
  await ElMessageBox.confirm(`删除作业「${row.name}」？`, '确认', { type: 'warning' })
  await pipelineApi.removeJob(row.id)
  load()
}

onMounted(load)
</script>

<style scoped>
.page-header { display: flex; justify-content: space-between; align-items: center; }
.stats { margin-bottom: 16px; }
.stat { text-align: center; }
.stat .num { font-size: 22px; font-weight: 700; color: #1e293b; }
.stat .lbl { color: #64748b; font-size: 13px; }
.muted { color: #94a3b8; font-size: 12px; }
</style>
