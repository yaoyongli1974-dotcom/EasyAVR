<template>
  <div class="page">
    <div class="page-header">
      <h2>GB28181 接入</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-card v-if="config.enabled" style="margin-bottom: 16px">
      <template #header>
        <div class="sip-card-header">
          <span>平台 SIP 参数（在设备端填写）</span>
          <el-button v-if="isAdmin" link type="primary" @click="openPassword">修改密钥</el-button>
        </div>
      </template>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="SIP 服务器 ID">{{ config.id }}</el-descriptions-item>
        <el-descriptions-item label="SIP 域 / Realm">{{ config.realm }}</el-descriptions-item>
        <el-descriptions-item label="SIP 监听地址">{{ config.listen }}</el-descriptions-item>
        <el-descriptions-item label="SIP 密钥">{{ config.passwordConfigured ? '已配置（不回显）' : '未配置' }}</el-descriptions-item>
        <el-descriptions-item label="收流 IP（RTP）">{{ config.rtpIp }}</el-descriptions-item>
        <el-descriptions-item label="已注册设备">{{ config.registered ?? devices.length }}</el-descriptions-item>
      </el-descriptions>
    </el-card>
    <el-alert v-else type="warning" :closable="false" title="GB28181 信令未启用" style="margin-bottom: 8px">
      <template #default>
        <el-button link type="primary" @click="goConfig">前往平台配置开启</el-button>
        <span>或在环境变量设置 EASYAVR_GB_ENABLED=true 后重启。</span>
      </template>
    </el-alert>

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
          <el-table-column label="密码" width="100">
            <template #default="{ row }">
              <el-tag :type="row.passwordConfigured ? 'success' : 'info'" size="small" effect="plain">
                {{ row.passwordConfigured ? '已配置' : '未配置' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="row.online ? 'success' : 'info'">{{ row.online ? '在线' : '离线' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最后心跳" width="180">
            <template #default="{ row }">{{ format(row.lastHeartbeat) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="240">
            <template #default="{ row }">
              <el-button link type="primary" @click="openCascade(row)">编辑</el-button>
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
          <el-table-column label="密码" width="100">
            <template #default="{ row }">
              <el-tag :type="row.passwordConfigured ? 'success' : 'info'" size="small" effect="plain">
                {{ row.passwordConfigured ? '已配置' : '未配置' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="140">
            <template #default="{ row }">
              <el-button link type="primary" @click="openWhite(row)">编辑</el-button>
              <el-button link type="danger" @click="removeWhite(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
      <el-tab-pane label="黑名单" name="blacklist">
        <el-alert type="info" :closable="false" style="margin-bottom: 10px"
          title="填写多个条件时须全部匹配才拦截；建议同时填设备编码与 IP 以防换 ID 恶意注册。" />
        <div style="margin-bottom: 10px">
          <el-button type="primary" @click="openBlack">新增黑名单</el-button>
        </div>
        <el-table :data="blacklist" border>
          <el-table-column prop="deviceId" label="设备编码" min-width="170" />
          <el-table-column prop="ua" label="UA" min-width="140" />
          <el-table-column prop="ip" label="IP" width="150" />
          <el-table-column prop="port" label="端口" width="90" />
          <el-table-column prop="protocol" label="协议" width="110" />
          <el-table-column prop="description" label="描述" min-width="140" />
          <el-table-column label="操作" width="100">
            <template #default="{ row }">
              <el-button link type="danger" @click="removeBlack(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="cascadeVisible" :title="editingCascade ? '编辑上级平台' : '新增上级平台'" width="520px">
      <el-form :model="cascadeForm" label-width="120px">
        <el-form-item label="名称"><el-input v-model="cascadeForm.name" /></el-form-item>
        <el-form-item label="上级 SIP 编码"><el-input v-model="cascadeForm.targetId" /></el-form-item>
        <el-form-item label="上级地址"><el-input v-model="cascadeForm.targetIp" /></el-form-item>
        <el-form-item label="上级端口"><el-input-number v-model="cascadeForm.targetPort" :min="1" /></el-form-item>
        <el-form-item label="本平台编码"><el-input v-model="cascadeForm.localId" placeholder="留空用默认平台编码" /></el-form-item>
        <el-form-item :label="editingCascade ? '修改密码' : '密码'">
          <SecretField
            v-model="cascadeForm.password"
            v-model:clear="cascadeForm.clearPassword"
            :configured="!!editingCascade?.passwordConfigured"
            :editing="!!editingCascade"
          />
        </el-form-item>
        <el-form-item label="启用"><el-switch v-model="cascadeForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="cascadeVisible = false">取消</el-button>
        <el-button type="primary" @click="saveCascade">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="whiteVisible" :title="editingWhite ? '编辑白名单' : '新增白名单'" width="500px">
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
        <el-form-item :label="editingWhite ? '修改密码' : '密码'">
          <SecretField
            v-model="whiteForm.password"
            v-model:clear="whiteForm.clearPassword"
            :configured="!!editingWhite?.passwordConfigured"
            :editing="!!editingWhite"
          />
        </el-form-item>
        <el-form-item label="描述"><el-input v-model="whiteForm.description" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="whiteVisible = false">取消</el-button>
        <el-button type="primary" @click="saveWhite">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="blackVisible" title="新增黑名单" width="500px">
      <el-form :model="blackForm" label-width="110px">
        <el-form-item label="设备编码"><el-input v-model="blackForm.deviceId" /></el-form-item>
        <el-form-item label="UA"><el-input v-model="blackForm.ua" placeholder="可选，User-Agent" /></el-form-item>
        <el-form-item label="IP"><el-input v-model="blackForm.ip" /></el-form-item>
        <el-form-item label="端口"><el-input-number v-model="blackForm.port" :min="0" /></el-form-item>
        <el-form-item label="协议">
          <el-select v-model="blackForm.protocol" style="width: 100%">
            <el-option label="GB28181" value="GB28181" />
            <el-option label="EHOME" value="EHOME" />
          </el-select>
        </el-form-item>
        <el-form-item label="描述"><el-input v-model="blackForm.description" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="blackVisible = false">取消</el-button>
        <el-button type="primary" @click="saveBlack">保存</el-button>
      </template>
    </el-dialog>
    <el-dialog v-model="passwordVisible" title="修改 SIP 密钥" width="480px" @closed="passwordForm.password = ''">
      <el-form :model="passwordForm" label-width="110px">
        <el-form-item label="新密钥" required>
          <el-input v-model="passwordForm.password" type="password" show-password autocomplete="new-password" placeholder="请输入新的 SIP 摘要认证密码" />
        </el-form-item>
      </el-form>
      <p class="sip-hint">保存后立即对后续设备注册生效；已注册设备重新注册时使用新密钥。新密钥加密保存，服务重启后继续有效。</p>
      <template #footer>
        <el-button @click="passwordVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingPassword" @click="savePassword">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { gbApi } from '../api'
import SecretField from '../components/SecretField.vue'
import { useAuthStore } from '../stores/auth'
import type { GBBlackList, GBCascade, GBDevice, GBWhiteList } from '../types'

const auth = useAuthStore()
const isAdmin = computed(() => auth.user?.role === 'admin')

const router = useRouter()
function goConfig() {
  router.push('/config')
}

const tab = ref('devices')
const devices = ref<GBDevice[]>([])
const cascades = ref<GBCascade[]>([])
const whitelist = ref<GBWhiteList[]>([])
const blacklist = ref<GBBlackList[]>([])
const config = ref<Record<string, any>>({})
const loading = ref(false)

const cascadeVisible = ref(false)
const whiteVisible = ref(false)
const blackVisible = ref(false)
const passwordVisible = ref(false)
const savingPassword = ref(false)
const passwordForm = reactive<{ password: string }>({ password: '' })
const editingCascade = ref<GBCascade | null>(null)
const editingWhite = ref<GBWhiteList | null>(null)
const cascadeForm = reactive<Partial<GBCascade> & { password?: string; clearPassword?: boolean }>({ targetPort: 5060, enabled: true })
const whiteForm = reactive<Partial<GBWhiteList> & { password?: string; clearPassword?: boolean }>({ protocol: 'GB28181' })
const blackForm = reactive<Partial<GBBlackList>>({ protocol: 'GB28181' })

async function load() {
  loading.value = true
  try {
    const [cfg, list, cas, wl, bl] = await Promise.all([
      gbApi.config(),
      gbApi.devices(),
      gbApi.cascades().catch(() => []),
      gbApi.whitelist().catch(() => []),
      gbApi.blacklist().catch(() => []),
    ])
    config.value = cfg
    devices.value = list
    cascades.value = cas
    whitelist.value = wl
    blacklist.value = bl
  } finally {
    loading.value = false
  }
}

function openPassword() {
  passwordForm.password = ''
  passwordVisible.value = true
}
async function savePassword() {
  const password = passwordForm.password.trim()
  if (!password) {
    ElMessage.warning('请输入新的 SIP 密钥')
    return
  }
  savingPassword.value = true
  try {
    await gbApi.setPassword(password)
    ElMessage.success('密钥已更新，立即生效并持久保存')
    passwordVisible.value = false
    passwordForm.password = ''
    load()
  } catch (e: any) {
    const status = e?.response?.status
    ElMessage.error(status === 403 ? '需要管理员权限' : (e?.response?.data?.message || '保存失败'))
  } finally {
    savingPassword.value = false
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

function openCascade(row?: GBCascade) {
  editingCascade.value = row || null
  Object.assign(cascadeForm, row ? {
    name: row.name, targetId: row.targetId, localId: row.localId,
    targetIp: row.targetIp, targetPort: row.targetPort, transport: row.transport,
    enabled: row.enabled, password: '', clearPassword: false,
  } : {
    name: '', targetId: '', targetIp: '', targetPort: 5060, localId: '',
    transport: '', password: '', clearPassword: false, enabled: true,
  })
  cascadeVisible.value = true
}
async function saveCascade() {
  const body = {
    ...cascadeForm,
    password: cascadeForm.clearPassword ? '' : (cascadeForm.password || ''),
    clearPassword: !!cascadeForm.clearPassword,
  }
  try {
    if (editingCascade.value) await gbApi.updateCascade(editingCascade.value.id, body)
    else await gbApi.createCascade(body)
    ElMessage.success(editingCascade.value ? '已保存' : '已保存并尝试注册')
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

function openWhite(row?: GBWhiteList) {
  editingWhite.value = row || null
  Object.assign(whiteForm, row ? {
    deviceId: row.deviceId, ip: row.ip, port: row.port, protocol: row.protocol,
    enabled: row.enabled, description: row.description, password: '', clearPassword: false,
  } : {
    deviceId: '', ip: '', port: 5060, protocol: 'GB28181',
    password: '', clearPassword: false, description: '',
  })
  whiteVisible.value = true
}
async function saveWhite() {
  const body = {
    ...whiteForm,
    password: whiteForm.clearPassword ? '' : (whiteForm.password || ''),
    clearPassword: !!whiteForm.clearPassword,
  }
  try {
    if (editingWhite.value) await gbApi.updateWhitelist(editingWhite.value.id, body)
    else await gbApi.createWhitelist(body)
    ElMessage.success('已保存')
    whiteVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  }
}
async function removeWhite(row: GBWhiteList) {
  await gbApi.removeWhitelist(row.id)
  load()
}

function openBlack() {
  Object.assign(blackForm, { deviceId: '', ua: '', ip: '', port: 0, protocol: 'GB28181', description: '' })
  blackVisible.value = true
}
async function saveBlack() {
  if (!blackForm.deviceId && !blackForm.ua && !blackForm.ip && !blackForm.port) {
    ElMessage.warning('至少填写一个匹配条件')
    return
  }
  try {
    await gbApi.createBlacklist(blackForm)
    ElMessage.success('已保存')
    blackVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  }
}
async function removeBlack(row: GBBlackList) {
  await gbApi.removeBlacklist(row.id)
  load()
}

function format(t: string) {
  return t && t !== '0001-01-01T00:00:00Z' ? new Date(t).toLocaleString() : '-'
}

onMounted(load)
</script>

<style scoped>
.sip-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.sip-hint {
  margin: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 1.6;
}
</style>
