<template>
  <div class="page">
    <div class="page-header">
      <h2>平台配置</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-alert type="info" :closable="false" style="margin-bottom: 16px"
      title="设备接入类开关可即时启停信令监听，无需重启；其余参数由环境变量决定（见 README）。" />

    <el-card v-loading="loading">
      <template #header>设备接入信令</template>
      <el-form label-width="180px">
        <el-form-item label="GB28181 信令">
          <el-switch :model-value="cfg?.gb.enabled" @change="(v: boolean) => toggle('gbEnabled', v)" />
          <el-tag size="small" :type="cfg?.gb.running ? 'success' : 'info'" style="margin-left: 10px">
            {{ cfg?.gb.running ? '监听中' : '未运行' }}
          </el-tag>
          <span class="hint">SIP {{ cfg?.gb.listen }} · ID {{ cfg?.gb.id }} · Realm {{ cfg?.gb.realm }}</span>
        </el-form-item>
        <el-form-item label="EHOME/ISUP 信令">
          <el-switch :model-value="cfg?.ehome.enabled" @change="(v: boolean) => toggle('ehomeEnabled', v)" />
          <el-tag size="small" :type="cfg?.ehome.running ? 'success' : 'info'" style="margin-left: 10px">
            {{ cfg?.ehome.running ? '监听中' : '未运行' }}
          </el-tag>
          <span class="hint">CMS {{ cfg?.ehome.cmsListen }} · SMS {{ cfg?.ehome.smsListen }}</span>
        </el-form-item>
        <el-form-item label="GB35114 国密信令">
          <el-switch :model-value="cfg?.gb35114.enabled" @change="(v: boolean) => toggle('gb35114Enabled', v)" />
          <el-tag size="small" :type="cfg?.gb35114.running ? 'success' : 'info'" style="margin-left: 10px">
            {{ cfg?.gb35114.running ? '监听中' : '未运行' }}
          </el-tag>
          <span class="hint">安全 SIP {{ cfg?.gb35114.sipListen || '（未配置端口）' }}</span>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card style="margin-top: 16px">
      <template #header>播放与运维</template>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="播放鉴权">{{ cfg?.playback.auth ? '已启用' : '未启用' }}</el-descriptions-item>
        <el-descriptions-item label="播放 token 时效">{{ cfg?.playback.tokenTtlMin }} 分钟</el-descriptions-item>
        <el-descriptions-item label="播放来源白名单">{{ cfg?.playback.whitelist?.length ? cfg.playback.whitelist.join(', ') : '不限制' }}</el-descriptions-item>
        <el-descriptions-item label="状态采样周期">{{ cfg?.monitorSec ? cfg.monitorSec + ' 秒' : '关闭' }}</el-descriptions-item>
      </el-descriptions>
      <p class="hint">播放鉴权、白名单、采样周期及证书目录等由环境变量配置，修改后需重启进程。</p>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { configApi } from '../api'
import type { PlatformConfig, PlatformConfigUpdate } from '../types'

const cfg = ref<PlatformConfig | null>(null)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    cfg.value = await configApi.platform()
  } finally {
    loading.value = false
  }
}

async function toggle(key: keyof PlatformConfigUpdate, value: boolean) {
  try {
    const res = await configApi.updatePlatform({ [key]: value } as PlatformConfigUpdate)
    if (res.warnings?.length) ElMessage.warning(res.warnings.join('；'))
    else ElMessage.success('已应用')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '应用失败')
  }
  load()
}

onMounted(load)
</script>

<style scoped>
.hint {
  margin-left: 12px;
  color: #94a3b8;
  font-size: 12px;
}
</style>
