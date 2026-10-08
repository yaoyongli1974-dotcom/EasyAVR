<template>
  <div class="page">
    <div class="page-header">
      <h2>GB28181 接入</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-card v-if="config.enabled" style="margin-bottom: 16px">
      <template #header>平台 SIP 参数（在设备端填写）</template>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="SIP 服务器 ID">{{ config.id }}</el-descriptions-item>
        <el-descriptions-item label="SIP 域 / Realm">{{ config.realm }}</el-descriptions-item>
        <el-descriptions-item label="SIP 监听地址">{{ config.listen }}</el-descriptions-item>
        <el-descriptions-item label="SIP 密码">{{ config.password }}</el-descriptions-item>
        <el-descriptions-item label="收流 IP（RTP）">{{ config.rtpIp }}</el-descriptions-item>
        <el-descriptions-item label="已注册设备">{{ config.registered ?? devices.length }}</el-descriptions-item>
      </el-descriptions>
    </el-card>
    <el-alert v-else type="warning" :closable="false" title="GB28181 信令未启用，请设置 EASYAVR_GB_ENABLED=true 后重启。" />

    <el-tabs v-model="tab" style="margin-top: 16px">
      <el-tab-pane label="设备" name="devices">
        <el-table :data="devices" border v-loading="loading">
          <el-table-column prop="deviceId" label="设备编码" min-width="180" />
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column prop="manufacturer" label="厂商" width="120" />
          <el-table-column prop="ip" label="IP" width="140" />
          <el-table-column prop="channelCount" label="通道数" width="90" />
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="row.online ? 'success' : 'info'">{{ row.online ? '在线' : '离线' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最后心跳" width="180">
            <template #default="{ row }">{{ format(row.lastKeepalive) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="120">
            <template #default="{ row }">
              <el-button link type="primary" @click="refresh(row)">刷新目录</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="级联（上级平台）" name="cascade">
        <div style="margin-bottom: 10px">
          <el-button type="primary" @click="openCascade">新增上级平台</el-button>
        </div>
        <el-table :data="cascades" border>
          <el-table-column prop="name" label="名称" min-width="120" />
          <el-table-column prop="targetId" label="上级 SIP 编码" min-width="170" />
          <el-table-column prop="targetIp" label="上级地址" min-width="150" />
          <el-table-column prop="localId" label="本平台编码" min-width="170" />
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="row.online ? 'success' : 'info'">{{ row.online ? '在线' : '离线' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最后心跳" width="180">
            <template #default="{ row }">{{ format(row.lastHeartbeat) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="180">
            <template #default="{ row }">
              <el-button link type="primary" @click="reRegister(row)">重新注册</el-button>
              <el-button link type="danger" @click="removeCascade(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="白名单" name="whitelist">
        <div style="margin-bottom: 10px">
          <el-button type="primary" @click="openWhite">新增白名单</el-button>
        </div>
        <el-table :data="whitelist" border>
          <el-table-column prop="deviceId" label="设备编码" min-width="170" />
          <el-table-column prop="ip" label="IP" width="150" />
          <el-table-column prop="port" label="端口" width="90" />
          <el-table-column prop="protocol" label="协议" width="110" />
          <el-table-column prop="description" label="描述" min-width="160" />
          <el-table-column label="操作" width="100">
            <template #default="{ row }">
              <el-button link type="danger" @click="removeWhite(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="cascadeVisible" title="新增上级平台" width="520px">
      <el-form :model="cascadeForm" label-width="120px">
        <el-form-item label="名称"><el-input v-model="cascadeForm.name" /></el-form-item>
        <el-form-item label="上级 SIP 编码"><el-input v-model="cascadeForm.targetId" /></el-form-item>
        <el-form-item label="上级地址"><el-input v-model="cascadeForm.targetIp" /></el-form-item>
        <el-form-item label="上级端口"><el-input-number v-model="cascadeForm.targetPort" :min="1" /></el-form-item>
        <el-form-item label="本平台编码"><el-input v-model="cascadeForm.localId" placeholder="留空用默认平台编码" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="cascadeForm.password" type="password" show-password /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="cascadeForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cascadeVisible = false">取消</el-button>
        <el-button type="primary" @click="saveCascade">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="whiteVisible" title="新增白名单" width="500px">
      <el-form :model="whiteForm" label-width="110px">
        <el-form-item label="设备编码"><el-input v-model="whiteForm.deviceId" /></el-form-item>
        <el-form-item label="IP"><el-input v-model="whiteForm.ip" /></el-form-item>
        <el-form-item label="端口"><el-input-number v-model="whiteForm.port" :min="0" /></el-form-item>
        <el-form-item label="协议">
          <el-select v-model="whiteForm.protocol" style="width: 100%">
            <el-option label="GB28181" value="GB28181" />
            <el-option label="EHOME" value="EHOME" />
          </el-select>
        </el-form-item>
        <el-form-item label="密码"><el-input v-model="whiteForm.password" type="password" show-password /></el-form-item>
        <el-form-item label="描述"><el-input v-model="whiteForm.description" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="whiteVisible = false">取消</el-button>
        <el-button type="primary" @click="saveWhite">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { gbApi } from '../api'
import type { GBCascade, GBDevice, GBWhiteList } from '../types'

const tab = ref('devices')
const devices = ref<GBDevice[]>([])
const cascades = ref<GBCascade[]>([])
const whitelist = ref<GBWhiteList[]>([])
const config = ref<Record<string, any>>({})
const loading = ref(false)

const cascadeVisible = ref(false)
const whiteVisible = ref(false)
const cascadeForm = reactive<Partial<GBCascade> & { password?: string }>({ targetPort: 5060, enabled: true })
const whiteForm = reactive<Partial<GBWhiteList> & { password?: string }>({ protocol: 'GB28181' })

async function load() {
  loading.value = true
  try {
    const [cfg, list, cas, wl] = await Promise.all([
      gbApi.config(),
      gbApi.devices(),
      gbApi.cascades().catch(() => []),
      gbApi.whitelist().catch(() => []),
    ])
    config.value = cfg
    devices.value = list
    cascades.value = cas
    whitelist.value = wl
  } finally {
    loading.value = false
  }
}

async function refresh(row: GBDevice) {
  try {
    await gbApi.refresh(row.deviceId)
    ElMessage.success('已发送目录查询')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '设备未注册或信令未启用')
  }
}

function openCascade() {
  Object.assign(cascadeForm, { name: '', targetId: '', targetIp: '', targetPort: 5060, localId: '', password: '', enabled: true })
  cascadeVisible.value = true
}
async function saveCascade() {
  try {
    await gbApi.createCascade(cascadeForm)
    ElMessage.success('已保存并尝试注册')
    cascadeVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  }
}
async function reRegister(row: GBCascade) {
  await gbApi.refreshCascade(row.id)
  ElMessage.success('已触发注册')
  setTimeout(load, 1500)
}
async function removeCascade(row: GBCascade) {
  await ElMessageBox.confirm(`删除级联「${row.name}」？`, '确认', { type: 'warning' })
  await gbApi.removeCascade(row.id)
  load()
}

function openWhite() {
  Object.assign(whiteForm, { deviceId: '', ip: '', port: 5060, protocol: 'GB28181', password: '', description: '' })
  whiteVisible.value = true
}
async function saveWhite() {
  await gbApi.createWhitelist(whiteForm)
  ElMessage.success('已保存')
  whiteVisible.value = false
  load()
}
async function removeWhite(row: GBWhiteList) {
  await gbApi.removeWhitelist(row.id)
  load()
}

function format(t: string) {
  return t && t !== '0001-01-01T00:00:00Z' ? new Date(t).toLocaleString() : '-'
}

onMounted(load)
</script>
