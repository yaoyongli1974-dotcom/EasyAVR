<template>
  <div class="page">
    <div class="page-header">
      <h2>设备接入</h2>
      <div>
        <el-input v-model="keyword" placeholder="搜索名称/IP" style="width: 200px; margin-right: 8px" clearable @keyup.enter="load" />
        <el-button @click="openDiscovery">主动发现</el-button>
        <el-button @click="exportDevices">导出 CSV</el-button>
        <el-button @click="triggerImport">导入 CSV</el-button>
        <el-button @click="checkAll" :loading="checkingAll">批量检测</el-button>
        <el-button type="primary" @click="openCreate">添加设备</el-button>
        <input ref="fileInput" type="file" accept=".csv,text/csv" style="display: none" @change="importDevices" />
      </div>
    </div>

    <el-table :data="devices" v-loading="loading" border>
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="protocol" label="协议" width="110" />
      <el-table-column prop="accessMode" label="接入方式" width="110" />
      <el-table-column prop="ip" label="地址" min-width="140" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'online' ? 'success' : 'info'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="通道" width="80">
        <template #default="{ row }">{{ row.channels?.length || 0 }}</template>
      </el-table-column>
      <el-table-column label="操作" width="300">
        <template #default="{ row }">
          <el-button link type="primary" @click="openChannels(row)">通道</el-button>
          <el-button link type="success" @click="checkDevice(row)">检测</el-button>
          <el-button link @click="openStatusLogs(row)">消息</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      style="margin-top: 12px"
      layout="total, prev, pager, next"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      @current-change="(p: number) => { page = p; load() }"
    />

    <el-dialog v-model="createVisible" title="添加设备" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称"><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="协议">
          <el-select v-model="form.protocol" style="width: 100%" @change="onProtocolChange">
            <el-option v-for="p in protocols" :key="p" :label="p" :value="p" />
          </el-select>
        </el-form-item>
        <el-form-item label="厂商">
          <el-select v-model="form.manufacturer" style="width: 100%" clearable @change="onManufacturerChange">
            <el-option label="海康 Hikvision" value="hikvision" />
            <el-option label="大华 Dahua" value="dahua" />
            <el-option label="宇视 Uniview" value="uniview" />
            <el-option label="其他" value="other" />
          </el-select>
        </el-form-item>
        <el-form-item label="IP/端口">
          <el-input v-model="form.ip" placeholder="192.168.1.64" style="width: 65%; margin-right: 6px" />
          <el-input-number v-model="form.port" :min="0" :controls="false" style="width: 32%" />
        </el-form-item>
        <el-form-item label="用户名"><el-input v-model="form.username" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="form.password" type="password" show-password /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="create">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="discoverVisible" title="主动发现监控设备" width="780px">
      <el-form :inline="true">
        <el-form-item label="方式">
          <el-select v-model="discoverForm.mode" style="width: 150px">
            <el-option label="ONVIF 多播" value="onvif" />
            <el-option label="IP 网段扫描" value="subnet" />
            <el-option label="全部" value="all" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="discoverForm.mode !== 'onvif'" label="网段">
          <el-input v-model="discoverForm.subnet" placeholder="192.168.1.0/24（≤ /24）" style="width: 200px" />
        </el-form-item>
        <el-form-item label="超时(秒)">
          <el-input-number v-model="discoverForm.timeoutSec" :min="1" :max="30" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="discovering" @click="runDiscovery">开始发现</el-button>
        </el-form-item>
      </el-form>

      <el-table :data="discovered" border max-height="340" @selection-change="onSelect">
        <el-table-column type="selection" width="46" :selectable="(row: DiscoveredDevice) => !row.added" />
        <el-table-column label="地址" min-width="130">
          <template #default="{ row }">{{ row.ip }}:{{ row.port }}</template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column prop="manufacturer" label="厂商" width="110" />
        <el-table-column prop="model" label="型号" min-width="130" show-overflow-tooltip />
        <el-table-column label="来源" width="120">
          <template #default="{ row }">{{ row.source === 'ws-discovery' ? 'ONVIF' : '端口扫描' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag v-if="row.added" type="info">已接入</el-tag>
            <el-tag v-else type="success">可导入</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80">
          <template #default="{ row }">
            <el-button link type="primary" :loading="probingIp === row.ip" @click="probe(row)">探测</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-form :inline="true" style="margin-top: 12px">
        <el-form-item label="自动取流">
          <el-select v-model="importForm.autoMode" style="width: 140px">
            <el-option label="ONVIF" value="onvif" />
            <el-option label="海康 ISAPI" value="isapi" />
            <el-option label="不自动" value="none" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="importForm.autoMode === 'none'" label="协议">
          <el-select v-model="importForm.protocol" style="width: 110px">
            <el-option label="rtsp" value="rtsp" />
            <el-option label="onvif" value="onvif" />
          </el-select>
        </el-form-item>
        <el-form-item label="用户名"><el-input v-model="importForm.username" style="width: 130px" /></el-form-item>
        <el-form-item label="密码"><el-input v-model="importForm.password" type="password" show-password style="width: 130px" /></el-form-item>
        <el-form-item>
          <el-button type="primary" :disabled="!selected.length" :loading="importing" @click="importSelected">
            导入所选（{{ selected.length }}）
          </el-button>
        </el-form-item>
      </el-form>
      <div class="hint">选择 ONVIF / 海康 ISAPI 后，将用上方凭据探测设备并按码流自动建主/子码流通道；选「不自动」则按协议直接导入。</div>
    </el-dialog>

    <el-dialog v-model="probeVisible" title="ONVIF 探测结果" width="680px">
      <el-descriptions :column="2" border>
        <el-descriptions-item label="厂商">{{ probeResult?.deviceInfo.manufacturer || '-' }}</el-descriptions-item>
        <el-descriptions-item label="型号">{{ probeResult?.deviceInfo.model || '-' }}</el-descriptions-item>
        <el-descriptions-item label="固件">{{ probeResult?.deviceInfo.firmware || '-' }}</el-descriptions-item>
        <el-descriptions-item label="序列号">{{ probeResult?.deviceInfo.serial || '-' }}</el-descriptions-item>
      </el-descriptions>
      <el-table :data="probeResult?.profiles || []" border style="margin-top: 12px">
        <el-table-column prop="name" label="码流" min-width="140" />
        <el-table-column prop="token" label="Profile" width="120" />
        <el-table-column prop="streamUri" label="RTSP 地址" min-width="260" show-overflow-tooltip />
      </el-table>
    </el-dialog>

    <el-dialog v-model="isapiVisible" title="海康 ISAPI 探测结果" width="680px">
      <el-descriptions :column="2" border>
        <el-descriptions-item label="名称">{{ isapiResult?.deviceInfo.name || '-' }}</el-descriptions-item>
        <el-descriptions-item label="型号">{{ isapiResult?.deviceInfo.model || '-' }}</el-descriptions-item>
        <el-descriptions-item label="序列号">{{ isapiResult?.deviceInfo.serial || '-' }}</el-descriptions-item>
        <el-descriptions-item label="固件">{{ isapiResult?.deviceInfo.firmware || '-' }}</el-descriptions-item>
      </el-descriptions>
      <el-table :data="isapiResult?.channels || []" border style="margin-top: 12px">
        <el-table-column prop="name" label="码流" min-width="130" />
        <el-table-column prop="id" label="通道" width="90" />
        <el-table-column prop="streamType" label="类型" width="80" />
        <el-table-column prop="rtspUrl" label="RTSP 地址" min-width="260" show-overflow-tooltip />
      </el-table>
    </el-dialog>

    <el-drawer v-model="drawerVisible" :title="`通道 · ${current?.name || ''}`" size="60%">
      <div style="margin-bottom: 10px">
        <el-button size="small" type="primary" @click="addChannel">添加通道</el-button>
        <el-button size="small" @click="startAll">启动全部</el-button>
      </div>
      <el-table :data="channels" border size="small">
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" label="名称" min-width="120" />
        <el-table-column prop="streamType" label="码流" width="80" />
        <el-table-column prop="streamKey" label="流ID" min-width="150" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.online ? 'success' : 'info'">{{ row.online ? '在线' : '离线' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="320">
          <template #default="{ row }">
            <el-button link type="primary" :disabled="row.online" @click="start(row)">启动</el-button>
            <el-button link type="warning" :disabled="!row.online" @click="stop(row)">停止</el-button>
            <el-button link type="success" @click="play(row)">播放</el-button>
            <el-button link type="danger" @click="record(row)">{{ row.recording ? '停录' : '录像' }}</el-button>
            <el-button link @click="removeChannel(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-drawer>

    <el-dialog v-model="statusVisible" :title="`状态记录 · ${current?.name || ''}`" width="640px">
      <el-table :data="statusLogs" border size="small" max-height="420">
        <el-table-column label="时间" width="180">
          <template #default="{ row }">{{ formatTime(row.loggedAt) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.online ? 'success' : 'info'" size="small">{{ row.online ? '在线' : '离线' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="source" label="来源" width="90" />
        <el-table-column prop="ip" label="IP" width="130" />
        <el-table-column prop="message" label="消息" min-width="160" show-overflow-tooltip />
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { channelApi, deviceApi, discoveryApi, isapiApi, onvifApi, recordingApi } from '../api'
import type { Channel, Device, DiscoveredDevice, IsapiProbeResult, OnvifProbeResult, StatusLog } from '../types'

const router = useRouter()
const protocols = ['rtsp', 'rtmp', 'onvif', 'gb28181', 'ehome', 'rtmp_push']
const protocolPorts: Record<string, number> = {
  rtsp: 554,
  rtmp: 1935,
  onvif: 80,
  gb28181: 5060,
  ehome: 7660,
  rtmp_push: 1935,
}
const manufacturerDefaults: Record<string, { protocol: string; port: number }> = {
  hikvision: { protocol: 'rtsp', port: 554 },
  dahua: { protocol: 'rtsp', port: 554 },
  uniview: { protocol: 'rtsp', port: 554 },
  other: { protocol: 'rtsp', port: 554 },
}
const devices = ref<Device[]>([])
const channels = ref<Channel[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const keyword = ref('')
const loading = ref(false)
const saving = ref(false)
const createVisible = ref(false)
const drawerVisible = ref(false)
const current = ref<Device | null>(null)
const form = reactive({ name: '', protocol: 'rtsp', manufacturer: 'hikvision', ip: '', port: 554, username: '', password: '' })

const discoverVisible = ref(false)
const discovering = ref(false)
const importing = ref(false)
const discovered = ref<DiscoveredDevice[]>([])
const selected = ref<DiscoveredDevice[]>([])
const discoverForm = reactive<{ mode: 'onvif' | 'subnet' | 'all'; subnet: string; timeoutSec: number }>({
  mode: 'onvif',
  subnet: '',
  timeoutSec: 3,
})
const importForm = reactive<{
  protocol: string
  username: string
  password: string
  autoMode: 'none' | 'onvif' | 'isapi'
}>({ protocol: 'rtsp', username: '', password: '', autoMode: 'onvif' })
const probeVisible = ref(false)
const probingIp = ref('')
const probeResult = ref<OnvifProbeResult | null>(null)
const isapiVisible = ref(false)
const isapiResult = ref<IsapiProbeResult | null>(null)

const fileInput = ref<HTMLInputElement>()
const checkingAll = ref(false)
const statusVisible = ref(false)
const statusLogs = ref<StatusLog[]>([])

function formatTime(t: string) {
  return t && t !== '0001-01-01T00:00:00Z' ? new Date(t).toLocaleString() : '-'
}

async function exportDevices() {
  try {
    const blob = await deviceApi.exportCSV()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'devices.csv'
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    ElMessage.error('导出失败')
  }
}

function triggerImport() {
  fileInput.value?.click()
}

async function importDevices(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const csv = await file.text()
  try {
    const r = await deviceApi.importCSV(csv)
    ElMessage.success(`导入完成：新增 ${r.created}，跳过 ${r.skipped}`)
    load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '导入失败')
  } finally {
    input.value = ''
  }
}

async function checkDevice(row: Device) {
  try {
    const r = await deviceApi.check(row.id)
    ElMessage[r.online ? 'success' : 'warning'](r.online ? '设备在线' : `设备离线：${r.message || ''}`)
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '检测失败')
  }
}

async function checkAll() {
  checkingAll.value = true
  try {
    const r = await deviceApi.checkAll()
    ElMessage.success(`检测完成：在线 ${r.online}/${r.total}`)
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '检测失败')
  } finally {
    checkingAll.value = false
  }
}

async function openStatusLogs(row: Device) {
  current.value = row
  statusLogs.value = await deviceApi.statusLogs(row.id).catch(() => [])
  statusVisible.value = true
}

function openDiscovery() {
  discovered.value = []
  selected.value = []
  Object.assign(importForm, { protocol: 'rtsp', username: '', password: '', autoMode: 'onvif' })
  discoverVisible.value = true
}

function endpointOf(d: DiscoveredDevice) {
  return d.xaddr ? { xaddr: d.xaddr } : { host: d.ip, port: d.port }
}

async function probe(row: DiscoveredDevice) {
  probingIp.value = row.ip
  try {
    if (importForm.autoMode === 'isapi') {
      isapiResult.value = await isapiApi.probe({
        host: row.ip,
        port: row.port,
        username: importForm.username,
        password: importForm.password,
      })
      isapiVisible.value = true
    } else {
      probeResult.value = await onvifApi.probe({
        ...endpointOf(row),
        username: importForm.username,
        password: importForm.password,
      })
      probeVisible.value = true
    }
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '探测失败，请检查设备地址与凭据')
  } finally {
    probingIp.value = ''
  }
}

function onSelect(rows: DiscoveredDevice[]) {
  selected.value = rows
}

async function runDiscovery() {
  if (discoverForm.mode !== 'onvif' && !discoverForm.subnet) {
    ElMessage.warning('请输入待扫描网段，例如 192.168.1.0/24')
    return
  }
  discovering.value = true
  try {
    discovered.value = await discoveryApi.scan({
      mode: discoverForm.mode,
      subnet: discoverForm.subnet || undefined,
      timeoutSec: discoverForm.timeoutSec,
    })
    if (!discovered.value.length) ElMessage.info('未发现设备，请检查网段或多播可达性')
    else ElMessage.success(`发现 ${discovered.value.length} 个设备`)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '发现失败')
  } finally {
    discovering.value = false
  }
}

async function importSelected() {
  importing.value = true
  let done = 0
  try {
    for (const d of selected.value) {
      try {
        if (importForm.autoMode === 'onvif') {
          await onvifApi.import({
            ...endpointOf(d),
            name: d.name || d.ip,
            manufacturer: d.manufacturer || undefined,
            username: importForm.username,
            password: importForm.password,
          })
        } else if (importForm.autoMode === 'isapi') {
          await isapiApi.import({
            host: d.ip,
            port: d.port,
            name: d.name || d.ip,
            username: importForm.username,
            password: importForm.password,
          })
        } else {
          await deviceApi.create({
            name: d.name || d.ip,
            protocol: importForm.protocol,
            manufacturer: d.manufacturer || 'other',
            ip: d.ip,
            port: d.port,
            username: importForm.username,
            password: importForm.password,
          } as Partial<Device> & { password?: string })
        }
        done++
      } catch {
        // skip device-level failures and continue importing the rest
      }
    }
    ElMessage.success(`已导入 ${done} 个设备`)
    discoverVisible.value = false
    load()
  } finally {
    importing.value = false
  }
}

async function load() {
  loading.value = true
  try {
    const res = await deviceApi.list({ page: page.value, pageSize, keyword: keyword.value })
    devices.value = res.items
    total.value = res.total
  } finally {
    loading.value = false
  }
}

function onProtocolChange(p: string) {
  form.port = protocolPorts[p] ?? form.port
}

function onManufacturerChange(m: string) {
  const preset = manufacturerDefaults[m]
  if (preset) {
    form.protocol = preset.protocol
    form.port = preset.port
  }
}

function openCreate() {
  Object.assign(form, { name: '', protocol: 'rtsp', manufacturer: 'hikvision', ip: '', port: 554, username: '', password: '' })
  createVisible.value = true
}

async function create() {
  saving.value = true
  try {
    await deviceApi.create(form)
    ElMessage.success('设备已添加（已自动创建主通道）')
    createVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '添加失败')
  } finally {
    saving.value = false
  }
}

async function remove(row: Device) {
  await ElMessageBox.confirm(`删除设备「${row.name}」及其通道？`, '确认', { type: 'warning' })
  await deviceApi.remove(row.id)
  ElMessage.success('已删除')
  load()
}

async function openChannels(row: Device) {
  current.value = row
  channels.value = await deviceApi.channels(row.id)
  drawerVisible.value = true
}

async function addChannel() {
  if (!current.value) return
  const { value } = await ElMessageBox.prompt('通道名称', '添加通道', { inputValue: '子码流' })
  await deviceApi.createChannel(current.value.id, { name: value, streamType: 'sub' })
  channels.value = await deviceApi.channels(current.value.id)
}

async function start(row: Channel) {
  try {
    await channelApi.start(row.id)
    ElMessage.success('已启动')
    channels.value = await deviceApi.channels(row.deviceId)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '启动失败，请检查 ZLMediaKit 是否在线')
  }
}

async function startAll() {
  for (const ch of channels.value) {
    if (!ch.online) await channelApi.start(ch.id).catch(() => {})
  }
  if (current.value) channels.value = await deviceApi.channels(current.value.id)
}

async function stop(row: Channel) {
  await channelApi.stop(row.id)
  if (current.value) channels.value = await deviceApi.channels(current.value.id)
}

function play(row: Channel) {
  router.push({ name: 'live', query: { channelId: row.id } })
}

async function record(row: Channel) {
  try {
    if (row.recording) await recordingApi.stop(row.id)
    else await recordingApi.start(row.id)
    ElMessage.success(row.recording ? '已停止录像' : '已开始录像')
    if (current.value) channels.value = await deviceApi.channels(current.value.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '录像操作失败，请确认通道在线且 ZLMediaKit 正常')
  }
}

async function removeChannel(row: Channel) {
  await ElMessageBox.confirm('删除该通道？', '确认', { type: 'warning' })
  await channelApi.remove(row.id)
  if (current.value) channels.value = await deviceApi.channels(current.value.id)
}

onMounted(load)
</script>

<style scoped>
.hint {
  margin-top: 8px;
  font-size: 12px;
  color: #94a3b8;
}
</style>
