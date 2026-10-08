<template>
  <div class="page">
    <div class="page-header">
      <h2>GB35114 国密</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-alert
      v-if="!config.enabled"
      type="warning"
      :closable="false"
      title="GB35114 未启用"
    >
      <template #default>
        <el-button link type="primary" @click="goConfig">前往平台配置开启</el-button>
        <span>或在环境变量设置 EASYAVR_GB35114_ENABLED=true 后重启。</span>
      </template>
    </el-alert>
    <template v-else>
      <el-card style="margin-bottom: 16px">
        <template #header>平台 SM2 证书（安装到设备）</template>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="白名单模式">{{ config.whiteList ? '已开启' : '未开启' }}</el-descriptions-item>
          <el-descriptions-item label="证书目录">{{ config.certDir }}</el-descriptions-item>
          <el-descriptions-item label="平台证书">
            <el-tag :type="config.certReady ? 'success' : 'info'">{{ config.certReady ? '已生成' : '未生成' }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="安全 SIP (GM/TLS)">
            <el-tag v-if="config.sipListen" type="success">{{ config.sipListen }} · {{ config.sipRequireClient ? '双向认证' : '单向认证' }}</el-tag>
            <el-tag v-else type="info">未启用（设置 EASYAVR_GB35114_SIP_LISTEN）</el-tag>
          </el-descriptions-item>
        </el-descriptions>
        <div style="margin-top: 12px">
          <el-button type="primary" @click="generate">生成平台证书</el-button>
          <el-button :disabled="!platformCert" @click="downloadCert">下载平台证书</el-button>
        </div>
        <el-input v-if="platformCert" v-model="platformCert" type="textarea" :rows="6" readonly style="margin-top: 12px" />
      </el-card>

      <el-row :gutter="16" style="margin-bottom: 16px">
        <el-col :span="12">
          <el-card>
            <template #header>登记设备证书（双向认证）</template>
            <el-input v-model="deviceId" placeholder="设备编码（可选，默认取 CSR 的 CN）" style="margin-bottom: 10px" />
            <el-input v-model="csr" type="textarea" :rows="6" placeholder="粘贴设备证书请求（CSR PEM）" />
            <el-button type="primary" style="margin-top: 10px" @click="enroll">签发并登记</el-button>
            <el-input v-if="signedCert" v-model="signedCert" type="textarea" :rows="6" readonly style="margin-top: 10px" />
          </el-card>
        </el-col>
        <el-col :span="12">
          <el-card>
            <template #header>校验证书</template>
            <el-input v-model="verifyInput" type="textarea" :rows="6" placeholder="粘贴设备证书（PEM）" />
            <el-button type="primary" style="margin-top: 10px" @click="verify">校验</el-button>
            <el-alert
              v-if="verifyResult"
              style="margin-top: 10px"
              :type="verifyResult.valid ? 'success' : 'error'"
              :closable="false"
              :title="verifyResult.valid ? '证书有效，由平台 CA 签发' : verifyResult.message"
            />
            <el-descriptions v-if="verifyResult?.valid" :column="1" border style="margin-top: 10px">
              <el-descriptions-item label="设备编码">{{ verifyResult.info.deviceId }}</el-descriptions-item>
              <el-descriptions-item label="序列号">{{ verifyResult.info.serial }}</el-descriptions-item>
              <el-descriptions-item label="SM3 指纹">{{ verifyResult.fingerprint }}</el-descriptions-item>
              <el-descriptions-item label="已登记">{{ verifyResult.info.enrolled ? '是' : '否' }}</el-descriptions-item>
            </el-descriptions>
          </el-card>
        </el-col>
      </el-row>

      <el-row :gutter="16">
        <el-col :span="16">
          <el-card>
            <template #header>已登记设备证书</template>
            <el-table :data="certs" border>
              <el-table-column prop="deviceId" label="设备编码" min-width="140" />
              <el-table-column prop="subject" label="主题" min-width="200" />
              <el-table-column prop="serial" label="序列号" min-width="140" show-overflow-tooltip />
              <el-table-column label="状态" width="100">
                <template #default="{ row }">
                  <el-tag :type="row.status === 'active' ? 'success' : 'danger'">
                    {{ row.status === 'active' ? '有效' : '已吊销' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="100">
                <template #default="{ row }">
                  <el-button link type="danger" :disabled="row.status !== 'active'" @click="revoke(row)">吊销</el-button>
                </template>
              </el-table-column>
            </el-table>
          </el-card>
        </el-col>
        <el-col :span="8">
          <el-card>
            <template #header>SM3 摘要工具</template>
            <el-input v-model="sm3Input" placeholder="输入文本" />
            <el-button style="margin-top: 10px" @click="sm3">计算 SM3</el-button>
            <el-input v-if="sm3Output" v-model="sm3Output" readonly style="margin-top: 10px" />
          </el-card>
        </el-col>
      </el-row>
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { gb35114Api } from '../api'
import type { GB35114Cert } from '../types'

const router = useRouter()
function goConfig() {
  router.push('/config')
}

const config = ref<Record<string, any>>({})
const platformCert = ref('')
const csr = ref('')
const deviceId = ref('')
const signedCert = ref('')
const verifyInput = ref('')
const verifyResult = ref<{ valid: boolean; message?: string; fingerprint?: string; info: Record<string, any> } | null>(null)
const certs = ref<GB35114Cert[]>([])
const sm3Input = ref('')
const sm3Output = ref('')

async function load() {
  config.value = await gb35114Api.config()
  if (config.value.enabled) certs.value = await gb35114Api.certs()
}

async function generate() {
  try {
    const res = await gb35114Api.generateCert()
    platformCert.value = res.cert
    ElMessage.success('平台证书已生成')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '生成失败')
  }
}

function downloadCert() {
  const blob = new Blob([platformCert.value], { type: 'application/x-pem-file' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = 'easyavr-platform.crt'
  a.click()
}

async function enroll() {
  if (!csr.value) return
  try {
    const res = await gb35114Api.enroll(csr.value, deviceId.value || undefined)
    signedCert.value = res.cert
    ElMessage.success('已签发并登记设备证书')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '签发失败（需 SM2 CSR）')
  }
}

async function verify() {
  if (!verifyInput.value) return
  try {
    verifyResult.value = await gb35114Api.verify(verifyInput.value)
  } catch (e: any) {
    verifyResult.value = { valid: false, message: e?.response?.data?.message || '校验失败', info: {} }
  }
}

async function revoke(row: GB35114Cert) {
  await ElMessageBox.confirm(`吊销设备「${row.deviceId}」的证书？`, '确认', { type: 'warning' })
  await gb35114Api.revoke(row.id)
  ElMessage.success('已吊销')
  load()
}

async function sm3() {
  const res = await gb35114Api.sm3(sm3Input.value)
  sm3Output.value = res.sm3
}

onMounted(load)
</script>
