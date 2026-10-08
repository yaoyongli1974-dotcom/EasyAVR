<template>
  <div class="page">
    <div class="page-header">
      <h2>告警策略</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-row :gutter="16" class="stats">
      <el-col :span="5"><el-card><div class="stat"><div class="num">{{ stats.policies }}</div><div class="lbl">策略</div></div></el-card></el-col>
      <el-col :span="5"><el-card><div class="stat"><div class="num">{{ stats.enabled }}</div><div class="lbl">启用</div></div></el-card></el-col>
      <el-col :span="5"><el-card><div class="stat"><div class="num">{{ stats.last24h }}</div><div class="lbl">24h 投递</div></div></el-card></el-col>
      <el-col :span="5"><el-card><div class="stat"><div class="num">{{ stats.deliveries }}</div><div class="lbl">累计投递</div></div></el-card></el-col>
      <el-col :span="4"><el-card><div class="stat"><div class="num fail">{{ stats.failed }}</div><div class="lbl">失败</div></div></el-card></el-col>
    </el-row>

    <el-tabs v-model="tab" @tab-change="onTab">
      <el-tab-pane label="分发策略" name="policies">
        <el-card>
          <template #header>
            <span>AI 事件分级分发策略</span>
            <el-button type="primary" size="small" style="float: right" @click="openPolicy()">新建策略</el-button>
          </template>
          <el-table :data="policies" border>
            <el-table-column prop="name" label="名称" min-width="140" />
            <el-table-column label="匹配" min-width="220">
              <template #default="{ row }">
                <el-tag size="small" style="margin: 2px">级别 ≥ {{ levelLabel(row.minLevel) }}</el-tag>
                <el-tag v-if="row.kind" size="small" type="info" style="margin: 2px">{{ row.kind }}</el-tag>
                <el-tag v-if="row.eventType" size="small" type="info" style="margin: 2px">{{ row.eventType }}</el-tag>
                <el-tag v-if="row.channelId" size="small" type="info" style="margin: 2px">通道 {{ row.channelId }}</el-tag>
                <el-tag v-if="row.keywords" size="small" type="info" style="margin: 2px">{{ row.keywords }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="分级" width="90">
              <template #default="{ row }">{{ row.tiers?.length || 0 }} 级</template>
            </el-table-column>
            <el-table-column label="需确认" width="90">
              <template #default="{ row }"><el-tag :type="row.ackRequired ? 'warning' : 'info'" size="small">{{ row.ackRequired ? '是' : '否' }}</el-tag></template>
            </el-table-column>
            <el-table-column prop="cooldownSec" label="冷却(s)" width="90" />
            <el-table-column label="状态" width="80">
              <template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'" size="small">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
            </el-table-column>
            <el-table-column label="操作" width="220">
              <template #default="{ row }">
                <el-button link type="primary" @click="openPolicy(row)">编辑</el-button>
                <el-button link type="success" @click="test(row)">测试</el-button>
                <el-button link type="danger" @click="removePolicy(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="投递记录" name="deliveries">
        <el-card>
          <template #header>
            <span>投递记录</span>
            <el-select v-model="deliveryStatus" size="small" clearable placeholder="状态" style="width: 120px; float: right" @change="loadDeliveries">
              <el-option label="成功" value="success" />
              <el-option label="失败" value="failed" />
            </el-select>
          </template>
          <el-table :data="deliveries" border size="small">
            <el-table-column prop="id" label="ID" width="70" />
            <el-table-column prop="policyName" label="策略" min-width="140" />
            <el-table-column prop="tier" label="分级" width="70" />
            <el-table-column prop="eventId" label="事件ID" width="90" />
            <el-table-column prop="channelName" label="通道" min-width="120" />
            <el-table-column prop="reason" label="原因" width="100" />
            <el-table-column label="状态" width="90">
              <template #default="{ row }"><el-tag :type="row.status === 'success' ? 'success' : 'danger'" size="small">{{ row.status }}</el-tag></template>
            </el-table-column>
            <el-table-column prop="error" label="错误" min-width="140" />
            <el-table-column label="时间" width="180">
              <template #default="{ row }">{{ formatTime(row.createdAt) }}</template>
            </el-table-column>
          </el-table>
          <el-pagination
            style="margin-top: 12px; justify-content: flex-end"
            layout="total, prev, pager, next"
            :total="deliveryTotal"
            :page-size="deliveryPageSize"
            :current-page="deliveryPage"
            @current-change="(p: number) => { deliveryPage = p; loadDeliveries() }"
          />
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="policyVisible" :title="editPolicy ? '编辑策略' : '新建策略'" width="760px">
      <el-form :model="policyForm" label-width="110px">
        <el-row :gutter="12">
          <el-col :span="12"><el-form-item label="名称"><el-input v-model="policyForm.name" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="优先级"><el-input-number v-model="policyForm.priority" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="最低级别">
            <el-select v-model="policyForm.minLevel" style="width: 100%">
              <el-option label="提示 info" value="info" />
              <el-option label="警告 warning" value="warning" />
              <el-option label="严重 critical" value="critical" />
            </el-select>
          </el-form-item></el-col>
          <el-col :span="12"><el-form-item label="类型 kind"><el-input v-model="policyForm.kind" placeholder="cv / vlm / llm" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="事件类型"><el-input v-model="policyForm.eventType" placeholder="fire / person_intrusion" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="通道ID"><el-input-number v-model="policyForm.channelId" :min="0" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="关键词"><el-input v-model="policyForm.keywords" placeholder="逗号分隔" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="冷却(秒)"><el-input-number v-model="policyForm.cooldownSec" :min="0" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="需确认"><el-switch v-model="policyForm.ackRequired" /></el-form-item></el-col>
          <el-col :span="12"><el-form-item label="启用"><el-switch v-model="policyForm.enabled" /></el-form-item></el-col>
          <el-col :span="24"><el-form-item label="描述"><el-input v-model="policyForm.description" /></el-form-item></el-col>
        </el-row>
      </el-form>

      <el-divider>分级分发（tier 0 为即时，delay > 0 为未确认升级）</el-divider>
      <el-table :data="policyForm.tiers" border size="small">
        <el-table-column label="级别" width="70">
          <template #default="{ row }"><el-input-number v-model="row.tier" :min="0" size="small" controls-position="right" style="width: 100%" /></template>
        </el-table-column>
        <el-table-column label="延迟(秒)" width="110">
          <template #default="{ row }"><el-input-number v-model="row.delaySec" :min="0" size="small" controls-position="right" style="width: 100%" /></template>
        </el-table-column>
        <el-table-column label="最低级别" width="130">
          <template #default="{ row }">
            <el-select v-model="row.minLevel" size="small" clearable placeholder="继承策略">
              <el-option label="info" value="info" />
              <el-option label="warning" value="warning" />
              <el-option label="critical" value="critical" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="通知通道" min-width="200">
          <template #default="{ row }">
            <el-select v-model="row.targetIdList" multiple size="small" placeholder="选择通道" style="width: 100%">
              <el-option v-for="ch in channels" :key="ch.id" :label="ch.name" :value="ch.id" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80">
          <template #default="{ $index }"><el-button link type="danger" @click="policyForm.tiers.splice($index, 1)">移除</el-button></template>
        </el-table-column>
      </el-table>
      <el-button size="small" style="margin-top: 8px" @click="addTier">添加分级</el-button>

      <template #footer>
        <el-button @click="policyVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="savePolicy">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { alertApi, notifyApi } from '../api'
import type { AlertDelivery, AlertPolicy, AlertStats, NotificationChannel } from '../types'

interface TierForm { id: number; tier: number; delaySec: number; minLevel: string; targetIdList: number[]; template: string }

const tab = ref('policies')
const saving = ref(false)
const policies = ref<AlertPolicy[]>([])
const channels = ref<NotificationChannel[]>([])
const stats = reactive<AlertStats>({ policies: 0, enabled: 0, deliveries: 0, failed: 0, last24h: 0 })

const policyVisible = ref(false)
const editPolicy = ref<AlertPolicy | null>(null)
const policyForm = reactive({
  name: '', description: '', enabled: true, priority: 0, minLevel: 'info', kind: '', eventType: '',
  channelId: 0, keywords: '', cooldownSec: 0, ackRequired: false,
  tiers: [] as TierForm[],
})

const deliveries = ref<AlertDelivery[]>([])
const deliveryTotal = ref(0)
const deliveryPage = ref(1)
const deliveryPageSize = ref(20)
const deliveryStatus = ref('')

async function load() {
  policies.value = await alertApi.policies()
  Object.assign(stats, await alertApi.stats())
  channels.value = await notifyApi.channels()
  if (tab.value === 'deliveries') await loadDeliveries()
}

function onTab() {
  if (tab.value === 'deliveries') loadDeliveries()
}

async function loadDeliveries() {
  const params: Record<string, unknown> = { page: deliveryPage.value, pageSize: deliveryPageSize.value }
  if (deliveryStatus.value) params.status = deliveryStatus.value
  const r = await alertApi.deliveries(params)
  deliveries.value = r.items
  deliveryTotal.value = r.total
}

function toTierForm(t: { id: number; tier: number; delaySec: number; minLevel: string; targetIds: string; template: string }): TierForm {
  return {
    id: t.id, tier: t.tier, delaySec: t.delaySec, minLevel: t.minLevel, template: t.template,
    targetIdList: (t.targetIds || '').split(',').map(s => Number(s.trim())).filter(n => n > 0),
  }
}

function openPolicy(row?: AlertPolicy) {
  editPolicy.value = row || null
  if (row) {
    Object.assign(policyForm, {
      name: row.name, description: row.description, enabled: row.enabled, priority: row.priority,
      minLevel: row.minLevel, kind: row.kind, eventType: row.eventType, channelId: row.channelId,
      keywords: row.keywords, cooldownSec: row.cooldownSec, ackRequired: row.ackRequired,
      tiers: (row.tiers || []).map(toTierForm),
    })
  } else {
    Object.assign(policyForm, {
      name: '', description: '', enabled: true, priority: 0, minLevel: 'info', kind: '', eventType: '',
      channelId: 0, keywords: '', cooldownSec: 0, ackRequired: false, tiers: [],
    })
    addTier()
  }
  policyVisible.value = true
}

function addTier() {
  policyForm.tiers.push({ id: 0, tier: policyForm.tiers.length, delaySec: 0, minLevel: '', targetIdList: [], template: '' })
}

async function savePolicy() {
  if (!policyForm.name) {
    ElMessage.warning('请输入策略名称')
    return
  }
  const body: any = {
    ...policyForm,
    tiers: policyForm.tiers.map(t => ({
      id: t.id, tier: t.tier, delaySec: t.delaySec, minLevel: t.minLevel,
      targetIds: t.targetIdList.join(','), template: t.template,
    })),
  }
  saving.value = true
  try {
    if (editPolicy.value) await alertApi.updatePolicy(editPolicy.value.id, body)
    else await alertApi.createPolicy(body)
    ElMessage.success('已保存')
    policyVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function test(row: AlertPolicy) {
  try {
    const r = await alertApi.testPolicy(row.id)
    ElMessage.success(`已发送 ${r.sent} 条测试通知`)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '测试失败')
  }
}

async function removePolicy(row: AlertPolicy) {
  await ElMessageBox.confirm(`删除策略「${row.name}」？`, '确认', { type: 'warning' })
  try {
    await alertApi.removePolicy(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

function levelLabel(level: string) {
  return { info: '提示', warning: '警告', critical: '严重' }[level] || level || '-'
}
function formatTime(t: string) {
  return t ? new Date(t).toLocaleString() : '-'
}

onMounted(load)
</script>

<style scoped>
.page-header { display: flex; justify-content: space-between; align-items: center; }
.stats { margin-bottom: 16px; }
.stat { text-align: center; }
.stat .num { font-size: 24px; font-weight: 700; color: #1e293b; }
.stat .num.fail { color: #f56c6c; }
.stat .lbl { color: #64748b; font-size: 13px; }
</style>
