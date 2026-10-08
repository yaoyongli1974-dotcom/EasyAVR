<template>
  <div class="page">
    <div class="page-header">
      <h2>告警通知</h2>
    </div>
    <el-tabs v-model="tab">
      <el-tab-pane label="通知渠道" name="channels">
        <div style="margin-bottom: 10px">
          <el-button type="primary" @click="openChannel()">新增渠道</el-button>
        </div>
        <el-table :data="channels" border>
          <el-table-column prop="id" label="ID" width="60" />
          <el-table-column prop="name" label="名称" min-width="120" />
          <el-table-column prop="type" label="类型" width="100" />
          <el-table-column prop="url" label="Webhook URL" min-width="200" show-overflow-tooltip />
          <el-table-column prop="to" label="收件人" min-width="140" show-overflow-tooltip />
          <el-table-column label="启用" width="80">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '是' : '否' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="180">
            <template #default="{ row }">
              <el-button link type="primary" @click="testChannel(row)">测试</el-button>
              <el-button link type="primary" @click="openChannel(row)">编辑</el-button>
              <el-button link type="danger" @click="removeChannel(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="通知规则" name="rules">
        <div style="margin-bottom: 10px">
          <el-button type="primary" @click="openRule()">新增规则</el-button>
        </div>
        <el-table :data="rules" border>
          <el-table-column prop="id" label="ID" width="60" />
          <el-table-column prop="name" label="名称" min-width="120" />
          <el-table-column label="目标渠道" min-width="160">
            <template #default="{ row }">{{ targetNames(row.targetIds) }}</template>
          </el-table-column>
          <el-table-column prop="minLevel" label="最低级别" width="110" />
          <el-table-column prop="kind" label="能力" width="90" />
          <el-table-column prop="eventType" label="事件类型" width="140" />
          <el-table-column label="启用" width="80">
            <template #default="{ row }">
              <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '是' : '否' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="140">
            <template #default="{ row }">
              <el-button link type="primary" @click="openRule(row)">编辑</el-button>
              <el-button link type="danger" @click="removeRule(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="channelVisible" :title="editingChannel ? '编辑渠道' : '新增渠道'" width="560px">
      <el-form :model="channelForm" label-width="110px">
        <el-form-item label="名称"><el-input v-model="channelForm.name" /></el-form-item>
        <el-form-item label="类型">
          <el-select v-model="channelForm.type" style="width: 100%">
            <el-option label="Webhook" value="webhook" />
            <el-option label="邮件 Email" value="email" />
          </el-select>
        </el-form-item>
        <template v-if="channelForm.type === 'webhook'">
          <el-form-item label="URL"><el-input v-model="channelForm.url" placeholder="https://..." /></el-form-item>
          <el-form-item label="密钥"><el-input v-model="channelForm.secret" placeholder="X-EasyAVR-Secret（可选）" /></el-form-item>
        </template>
        <template v-else>
          <el-form-item label="SMTP 主机"><el-input v-model="channelForm.smtpHost" /></el-form-item>
          <el-form-item label="SMTP 端口"><el-input-number v-model="channelForm.smtpPort" :min="0" /></el-form-item>
          <el-form-item label="用户名"><el-input v-model="channelForm.smtpUser" /></el-form-item>
          <el-form-item label="密码"><el-input v-model="channelForm.smtpPassword" type="password" show-password /></el-form-item>
          <el-form-item label="发件人"><el-input v-model="channelForm.from" /></el-form-item>
          <el-form-item label="收件人"><el-input v-model="channelForm.to" placeholder="多个用逗号分隔" /></el-form-item>
          <el-form-item label="使用 TLS"><el-switch v-model="channelForm.useTls" /></el-form-item>
        </template>
        <el-form-item label="启用"><el-switch v-model="channelForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="channelVisible = false">取消</el-button>
        <el-button type="primary" @click="saveChannel">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="ruleVisible" :title="editingRule ? '编辑规则' : '新增规则'" width="520px">
      <el-form :model="ruleForm" label-width="110px">
        <el-form-item label="名称"><el-input v-model="ruleForm.name" /></el-form-item>
        <el-form-item label="目标渠道">
          <el-select v-model="targetIds" multiple style="width: 100%">
            <el-option v-for="c in channels" :key="c.id" :label="`${c.name} (${c.type})`" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="最低级别">
          <el-select v-model="ruleForm.minLevel" style="width: 100%">
            <el-option label="info" value="info" />
            <el-option label="warning" value="warning" />
            <el-option label="critical" value="critical" />
          </el-select>
        </el-form-item>
        <el-form-item label="能力类型"><el-input v-model="ruleForm.kind" placeholder="cv/vlm/llm/gb28181/ga1400（空=全部）" /></el-form-item>
        <el-form-item label="事件类型"><el-input v-model="ruleForm.eventType" placeholder="如 person_intrusion（空=全部）" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="ruleForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="ruleVisible = false">取消</el-button>
        <el-button type="primary" @click="saveRule">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { notifyApi } from '../api'
import type { NotificationChannel, NotificationRule } from '../types'

const tab = ref('channels')
const channels = ref<NotificationChannel[]>([])
const rules = ref<NotificationRule[]>([])
const channelVisible = ref(false)
const ruleVisible = ref(false)
const editingChannel = ref<NotificationChannel | null>(null)
const editingRule = ref<NotificationRule | null>(null)
const targetIds = ref<number[]>([])

const channelForm = reactive<Partial<NotificationChannel> & { secret?: string; smtpPassword?: string }>({
  type: 'webhook',
  enabled: true,
})
const ruleForm = reactive<Partial<NotificationRule>>({ enabled: true, minLevel: 'warning' })

async function load() {
  ;[channels.value, rules.value] = await Promise.all([notifyApi.channels(), notifyApi.rules()])
}

function targetNames(ids: string) {
  return ids
    .split(',')
    .map((s) => Number(s.trim()))
    .map((id) => channels.value.find((c) => c.id === id)?.name)
    .filter(Boolean)
    .join(', ')
}

function openChannel(row?: NotificationChannel) {
  editingChannel.value = row || null
  Object.assign(channelForm, row ? { ...row, secret: '', smtpPassword: '' } : {
    name: '', type: 'webhook', enabled: true, url: '', secret: '',
    smtpHost: '', smtpPort: 465, smtpUser: '', smtpPassword: '', from: '', to: '', useTls: true,
  })
  channelVisible.value = true
}

async function saveChannel() {
  if (editingChannel.value) await notifyApi.updateChannel(editingChannel.value.id, channelForm)
  else await notifyApi.createChannel(channelForm)
  ElMessage.success('已保存')
  channelVisible.value = false
  load()
}

async function removeChannel(row: NotificationChannel) {
  await ElMessageBox.confirm(`删除渠道「${row.name}」？`, '确认', { type: 'warning' })
  await notifyApi.removeChannel(row.id)
  load()
}

async function testChannel(row: NotificationChannel) {
  try {
    await notifyApi.testChannel(row.id)
    ElMessage.success('测试通知已发送')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '发送失败')
  }
}

function openRule(row?: NotificationRule) {
  editingRule.value = row || null
  Object.assign(ruleForm, row ? { ...row } : { name: '', enabled: true, minLevel: 'warning', kind: '', eventType: '' })
  targetIds.value = row?.targetIds ? row.targetIds.split(',').map((s) => Number(s)) : []
  ruleVisible.value = true
}

async function saveRule() {
  const payload = { ...ruleForm, targetIds: targetIds.value.join(',') }
  if (editingRule.value) await notifyApi.updateRule(editingRule.value.id, payload)
  else await notifyApi.createRule(payload)
  ElMessage.success('已保存')
  ruleVisible.value = false
  load()
}

async function removeRule(row: NotificationRule) {
  await ElMessageBox.confirm(`删除规则「${row.name}」？`, '确认', { type: 'warning' })
  await notifyApi.removeRule(row.id)
  load()
}

onMounted(load)
</script>
