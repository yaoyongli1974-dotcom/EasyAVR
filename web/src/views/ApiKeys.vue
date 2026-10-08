<template>
  <div class="page">
    <div class="page-header">
      <h2>开放 API</h2>
      <el-button @click="load">刷新</el-button>
    </div>
    <el-alert
      type="info"
      :closable="false"
      style="margin-bottom: 12px"
      title="供 APP 端与第三方系统调用 /api/v1/open/*。请求头携带 X-API-Key: <secret>（或 Authorization: ApiKey <secret>）。"
    />

    <el-tabs v-model="tab">
      <el-tab-pane label="密钥" name="keys">
        <el-row :gutter="12" style="margin-bottom: 12px">
          <el-col :span="8"><el-card shadow="never"><div class="stat"><span>密钥数</span><b>{{ stats.keys }}</b></div></el-card></el-col>
          <el-col :span="8"><el-card shadow="never"><div class="stat"><span>今日请求</span><b>{{ stats.requestsToday }}</b></div></el-card></el-col>
          <el-col :span="8"><el-card shadow="never"><div class="stat"><span>累计请求</span><b>{{ stats.requestsTotal }}</b></div></el-card></el-col>
        </el-row>

        <el-card>
          <template #header>
            <span>密钥列表</span>
            <el-button type="primary" size="small" style="float: right" @click="openCreate">新建密钥</el-button>
          </template>
          <el-table :data="keys" border>
            <el-table-column prop="name" label="名称" min-width="130" />
            <el-table-column prop="prefix" label="前缀" width="130" />
            <el-table-column prop="scopes" label="权限" width="110" />
            <el-table-column label="用量" width="140">
              <template #default="{ row }">
                {{ row.quotaPerDay > 0 ? `${row.usedToday} / ${row.quotaPerDay}` : `${row.usedToday} / 不限` }}
              </template>
            </el-table-column>
            <el-table-column label="限速" width="100">
              <template #default="{ row }">{{ row.rateLimit > 0 ? `${row.rateLimit}/分` : '不限' }}</template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="最近使用" min-width="190">
              <template #default="{ row }">
                <span v-if="row.lastUsedAt">{{ new Date(row.lastUsedAt).toLocaleString() }} · {{ row.lastUsedIp }}</span>
                <span v-else>-</span>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="200">
              <template #default="{ row }">
                <el-button link type="primary" @click="toggle(row)">{{ row.enabled ? '停用' : '启用' }}</el-button>
                <el-button link type="primary" @click="openLogs(row)">日志</el-button>
                <el-button link type="danger" @click="remove(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="API 文档" name="docs">
        <el-card shadow="never" style="margin-bottom: 12px">
          <template #header>接口地址</template>
          <el-descriptions :column="2" border>
            <el-descriptions-item label="Base URL">{{ spec.servers?.[0]?.url || '/api/v1/open' }}</el-descriptions-item>
            <el-descriptions-item label="鉴权请求头">X-API-Key: &lt;secret&gt;</el-descriptions-item>
          </el-descriptions>
          <pre class="curl">curl -H "X-API-Key: ea_xxx" {{ origin }}{{ spec.servers?.[0]?.url }}/devices</pre>
        </el-card>
        <el-card>
          <template #header>接口列表</template>
          <el-table :data="docRows" border>
            <el-table-column label="方法" width="90">
              <template #default="{ row }">
                <el-tag :type="row.method === 'GET' ? 'success' : 'warning'">{{ row.method }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="path" label="路径" min-width="220" />
            <el-table-column prop="summary" label="说明" min-width="260" />
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="审计日志" name="audit">
        <el-card>
          <template #header>最近请求</template>
          <el-table :data="logs" border>
            <el-table-column label="时间" width="170">
              <template #default="{ row }">{{ new Date(row.createdAt).toLocaleString() }}</template>
            </el-table-column>
            <el-table-column prop="keyName" label="密钥" width="130" />
            <el-table-column prop="method" label="方法" width="80" />
            <el-table-column prop="path" label="路径" min-width="220" />
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag :type="row.status < 400 ? 'success' : 'danger'">{{ row.status }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="latencyMs" label="耗时(ms)" width="100" />
            <el-table-column prop="ip" label="来源 IP" min-width="130" />
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="visible" title="新建开放 API 密钥" width="520px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="名称"><el-input v-model="form.name" placeholder="如：第三方平台 / 移动端" /></el-form-item>
        <el-form-item label="权限">
          <el-checkbox-group v-model="form.scopeList">
            <el-checkbox label="read">读取</el-checkbox>
            <el-checkbox label="write">写入（事件上报）</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item label="限速(次/分)"><el-input-number v-model="form.rateLimit" :min="0" /></el-form-item>
        <el-form-item label="每日配额"><el-input-number v-model="form.quotaPerDay" :min="0" /></el-form-item>
        <el-form-item label="过期时间">
          <el-date-picker v-model="form.expiresAt" type="datetime" placeholder="留空表示永不过期" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" @click="save">生成</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="secretVisible" title="密钥已生成" width="560px">
      <el-alert type="warning" :closable="false" title="密钥只显示一次，请立即复制保存。" style="margin-bottom: 12px" />
      <el-input v-model="secret" readonly>
        <template #append><el-button @click="copySecret">复制</el-button></template>
      </el-input>
    </el-dialog>

    <el-drawer v-model="logsVisible" :title="`请求日志 · ${logKeyName}`" size="60%">
      <el-table :data="keyLogs" border>
        <el-table-column label="时间" width="170">
          <template #default="{ row }">{{ new Date(row.createdAt).toLocaleString() }}</template>
        </el-table-column>
        <el-table-column prop="method" label="方法" width="80" />
        <el-table-column prop="path" label="路径" min-width="200" />
        <el-table-column prop="status" label="状态" width="80" />
        <el-table-column prop="latencyMs" label="耗时(ms)" width="100" />
        <el-table-column prop="ip" label="来源 IP" min-width="130" />
      </el-table>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiKeyApi } from '../api'
import type { APIKey, APIKeyStats, APIRequestLog } from '../types'

const tab = ref('keys')
const keys = ref<APIKey[]>([])
const logs = ref<APIRequestLog[]>([])
const keyLogs = ref<APIRequestLog[]>([])
const stats = ref<APIKeyStats>({ keys: 0, enabled: 0, requestsToday: 0, requestsTotal: 0, topKeys: [] })
const spec = ref<Record<string, any>>({})
const visible = ref(false)
const secretVisible = ref(false)
const logsVisible = ref(false)
const logKeyName = ref('')
const secret = ref('')
const origin = window.location.origin
const form = reactive<{ name: string; scopeList: string[]; expiresAt: Date | null; rateLimit: number; quotaPerDay: number }>({
  name: '',
  scopeList: ['read'],
  expiresAt: null,
  rateLimit: 0,
  quotaPerDay: 0,
})

const docRows = computed(() => {
  const out: Array<{ method: string; path: string; summary: string }> = []
  const paths = spec.value.paths || {}
  for (const [path, methods] of Object.entries<any>(paths)) {
    for (const [method, op] of Object.entries<any>(methods)) {
      out.push({ method: method.toUpperCase(), path, summary: op.summary || '' })
    }
  }
  return out
})

async function load() {
  keys.value = await apiKeyApi.list()
  stats.value = await apiKeyApi.stats()
}

async function loadTab(name: string) {
  if (name === 'audit') logs.value = await apiKeyApi.logs()
  if (name === 'docs' && !spec.value.paths) spec.value = await apiKeyApi.openapi()
}

watch(tab, loadTab)

function openCreate() {
  Object.assign(form, { name: '', scopeList: ['read'], expiresAt: null, rateLimit: 0, quotaPerDay: 0 })
  visible.value = true
}

async function save() {
  if (!form.name) {
    ElMessage.warning('请输入名称')
    return
  }
  try {
    const res = await apiKeyApi.create({
      name: form.name,
      scopes: form.scopeList.join(',') || 'read',
      expiresAt: form.expiresAt ? form.expiresAt.toISOString() : undefined,
      rateLimit: form.rateLimit,
      quotaPerDay: form.quotaPerDay,
    })
    secret.value = res.secret
    visible.value = false
    secretVisible.value = true
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '创建失败')
  }
}

async function copySecret() {
  try {
    await navigator.clipboard.writeText(secret.value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.info('请手动复制')
  }
}

async function toggle(row: APIKey) {
  await apiKeyApi.update(row.id, { enabled: !row.enabled } as Partial<APIKey>)
  load()
}

async function openLogs(row: APIKey) {
  logKeyName.value = row.name
  keyLogs.value = await apiKeyApi.logs(row.id)
  logsVisible.value = true
}

async function remove(row: APIKey) {
  await ElMessageBox.confirm(`删除密钥「${row.name}」？删除后使用该密钥的调用将立即失效。`, '确认', { type: 'warning' })
  await apiKeyApi.remove(row.id)
  load()
}

onMounted(load)
</script>

<style scoped>
.stat {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
.stat span {
  color: #64748b;
  font-size: 13px;
}
.stat b {
  font-size: 22px;
  color: #0f172a;
}
.curl {
  background: #0f172a;
  color: #e2e8f0;
  padding: 12px;
  border-radius: 6px;
  overflow-x: auto;
  margin: 12px 0 0;
}
</style>
