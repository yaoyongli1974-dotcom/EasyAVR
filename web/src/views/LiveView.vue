<template>
  <div class="page">
    <div class="page-header">
      <h2>实时视频</h2>
      <div>
        <el-input v-model="keyword" placeholder="搜索通道" style="width: 180px; margin-right: 8px" clearable @keyup.enter="load" />
        <el-button @click="load">刷新</el-button>
      </div>
    </div>
    <el-row :gutter="16">
      <el-col :span="6">
        <el-card body-style="padding: 0">
          <el-table :data="channels" height="560" highlight-current-row @current-change="select" @row-click="select">
            <el-table-column label="通道">
              <template #default="{ row }">
                <span>{{ row.name }}</span>
                <el-tag size="small" :type="row.online ? 'success' : 'info'" style="margin-left: 6px">
                  {{ row.online ? '在线' : '离线' }}
                </el-tag>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-col>
      <el-col :span="18">
        <el-card>
          <template #header>
            <span>{{ selected?.name || '请选择通道' }}</span>
            <el-button v-if="selected" size="small" style="float: right" @click="runDiagnose">诊断</el-button>
            <el-button v-if="selected" size="small" style="float: right; margin-right: 8px" @click="openShare">分享</el-button>
            <el-button v-if="selected" size="small" style="float: right; margin-right: 8px" @click="openStatus">消息</el-button>
            <el-button v-if="selected && !selected.online" size="small" type="primary" style="float: right; margin-right: 8px" @click="start">
              启动拉流
            </el-button>
          </template>
          <VideoPlayer v-if="playUrls && Object.keys(playUrls).length" :urls="playUrls" />
          <el-empty v-else description="选择左侧在线通道开始播放" />
          <el-descriptions v-if="selected" :column="2" border style="margin-top: 12px">
            <el-descriptions-item label="流ID">{{ selected.streamKey }}</el-descriptions-item>
            <el-descriptions-item label="码流">{{ selected.streamType }}</el-descriptions-item>
            <el-descriptions-item label="推流地址" :span="2">{{ pushUrl }}</el-descriptions-item>
            <el-descriptions-item label="累计流量">{{ traffic ? (traffic.bytes / 1048576).toFixed(2) + ' MB' : '-' }}</el-descriptions-item>
            <el-descriptions-item label="在线">{{ selected.online ? '在线' : '离线' }}</el-descriptions-item>
          </el-descriptions>

          <div v-if="selected" class="ptz">
            <el-divider content-position="left">云台控制</el-divider>
            <div class="ptz-body">
              <div class="ptz-pad">
                <el-button @click="ptz('up_left')">↖</el-button>
                <el-button @click="ptz('up')">↑</el-button>
                <el-button @click="ptz('up_right')">↗</el-button>
                <el-button @click="ptz('left')">←</el-button>
                <el-button type="danger" @click="ptz('stop')">■</el-button>
                <el-button @click="ptz('right')">→</el-button>
                <el-button @click="ptz('down_left')">↙</el-button>
                <el-button @click="ptz('down')">↓</el-button>
                <el-button @click="ptz('down_right')">↘</el-button>
              </div>
              <div class="ptz-side">
                <div>
                  <el-button @click="ptz('zoom_in')">放大 +</el-button>
                  <el-button @click="ptz('zoom_out')">缩小 -</el-button>
                </div>
                <div class="speed">速度 <el-slider v-model="speed" :min="1" :max="100" style="width: 160px" /></div>
                <div class="presets">
                  <span>预置位：</span>
                  <el-tag
                    v-for="p in presets"
                    :key="p.id"
                    closable
                    style="margin: 2px; cursor: pointer"
                    @click="gotoPreset(p)"
                    @close="removePreset(p)"
                  >{{ p.preset }}: {{ p.name || '-' }}</el-tag>
                  <span v-if="!presets.length" class="muted">暂无</span>
                  <div style="margin-top: 6px">
                    <el-input-number v-model="presetNo" :min="1" :max="255" size="small" controls-position="right" style="width: 100px" />
                    <el-input v-model="presetName" size="small" placeholder="名称" style="width: 110px; margin: 0 6px" />
                    <el-button size="small" @click="savePreset">保存当前位置</el-button>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-dialog v-model="diagVisible" title="通道诊断" width="720px">
      <div v-if="diagLoading" style="text-align: center; padding: 20px; color: #94a3b8">诊断中…</div>
      <template v-else>
        <h4 style="margin: 0 0 8px">播放诊断</h4>
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="状态">
            <el-tag :type="diag?.status === 'ok' ? 'success' : 'danger'" size="small">{{ diag?.status }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="连接耗时">{{ diag?.probe?.latencyMs ?? '-' }} ms</el-descriptions-item>
          <el-descriptions-item label="封装">{{ diag?.probe?.format || '-' }}</el-descriptions-item>
          <el-descriptions-item label="码率">{{ diag?.probe?.bitRate ? Math.round(diag.probe.bitRate / 1000) + ' kbps' : '-' }}</el-descriptions-item>
          <el-descriptions-item label="分辨率">{{ diag?.probe?.video ? `${diag.probe.video.width}×${diag.probe.video.height}` : '-' }}</el-descriptions-item>
          <el-descriptions-item label="视频编码">{{ diag?.probe?.video ? `${diag.probe.video.codec} ${diag.probe.video.profile || ''}` : '-' }}</el-descriptions-item>
          <el-descriptions-item label="帧率">{{ diag?.probe?.video ? diag.probe.video.frameRate.toFixed(1) : '-' }}</el-descriptions-item>
          <el-descriptions-item label="音频">{{ diag?.probe?.audio ? `${diag.probe.audio.codec} ${diag.probe.audio.channels}ch` : '无' }}</el-descriptions-item>
        </el-descriptions>
        <el-alert v-if="diag?.error" type="error" :closable="false" :title="diag.error" style="margin-top: 8px" />

        <h4 style="margin: 16px 0 8px">视频质量诊断 (VQD)</h4>
        <el-alert v-if="vqd?.error" type="error" :closable="false" :title="vqd.error" />
        <template v-else>
          <div style="margin-bottom: 8px">
            级别：
            <el-tag :type="levelTag(vqd?.level)" size="small">{{ vqd?.level || '正常' }}</el-tag>
            <span v-if="vqd?.eventId" class="muted">（已记录事件 #{{ vqd.eventId }}）</span>
          </div>
          <el-descriptions :column="3" border size="small">
            <el-descriptions-item label="亮度">{{ vqd?.metrics?.brightness?.toFixed(0) }}</el-descriptions-item>
            <el-descriptions-item label="对比度">{{ vqd?.metrics?.contrast?.toFixed(0) }}</el-descriptions-item>
            <el-descriptions-item label="清晰度">{{ vqd?.metrics?.sharpness?.toFixed(0) }}</el-descriptions-item>
            <el-descriptions-item label="黑屏比">{{ ((vqd?.metrics?.blackRatio || 0) * 100).toFixed(0) }}%</el-descriptions-item>
            <el-descriptions-item label="噪声">{{ vqd?.metrics?.noise?.toFixed(1) }}</el-descriptions-item>
            <el-descriptions-item label="偏色">{{ vqd?.metrics?.colorCast }}</el-descriptions-item>
            <el-descriptions-item label="蓝屏比">{{ ((vqd?.metrics?.blueRatio || 0) * 100).toFixed(0) }}%</el-descriptions-item>
            <el-descriptions-item label="方块度">{{ vqd?.metrics?.blockiness?.toFixed(2) }}</el-descriptions-item>
            <el-descriptions-item label="帧差">{{ vqd?.temporal?.meanDiff?.toFixed(2) ?? '-' }}</el-descriptions-item>
          </el-descriptions>
          <div style="margin-top: 8px">
            <el-tag v-for="i in vqd?.issues" :key="i.code" :type="levelTag(i.level)" size="small" style="margin: 2px">{{ i.message }}</el-tag>
            <span v-if="!vqd?.issues?.length" class="muted">未发现质量问题</span>
          </div>
        </template>
      </template>
      <template #footer>
        <el-button @click="diagVisible = false">关闭</el-button>
        <el-button type="primary" :loading="diagLoading" @click="runDiagnose">重新诊断</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="shareVisible" title="分享播放地址" width="640px">
      <el-alert v-if="shareAuth" type="warning" :closable="false" style="margin-bottom: 10px"
        :title="`播放鉴权已启用，地址将于 ${shareExpires || '稍后'} 过期`" />
      <el-descriptions v-for="(u, k) in shareUrls" :key="k" :column="1" border size="small" style="margin-bottom: 8px">
        <el-descriptions-item :label="String(k)">
          <div class="share-row">
            <span class="share-url">{{ u }}</span>
            <el-button size="small" @click="copy(u)">复制</el-button>
          </div>
        </el-descriptions-item>
      </el-descriptions>
    </el-dialog>

    <el-drawer v-model="statusVisible" :title="`通道消息 · ${selected?.name || ''}`" size="40%">
      <el-table :data="statusLogs" border size="small">
        <el-table-column label="时间" width="170">
          <template #default="{ row }">{{ new Date(row.loggedAt).toLocaleString() }}</template>
        </el-table-column>
        <el-table-column label="状态" width="80">
          <template #default="{ row }">
            <el-tag :type="row.online ? 'success' : 'info'" size="small">{{ row.online ? '在线' : '离线' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="source" label="来源" width="90" />
        <el-table-column prop="message" label="消息" min-width="140" show-overflow-tooltip />
      </el-table>
      <el-empty v-if="!statusLogs.length" description="暂无记录" />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import VideoPlayer from '../components/VideoPlayer.vue'
import { channelApi } from '../api'
import type { Channel, PTZPreset, ChannelDiagnose, ChannelVQD, ChannelTraffic, StatusLog } from '../types'

const route = useRoute()
const channels = ref<Channel[]>([])
const selected = ref<Channel | null>(null)
const playUrls = ref<Record<string, string>>({})
const pushUrl = ref('')
const keyword = ref('')
const speed = ref(50)
const presets = ref<PTZPreset[]>([])
const presetNo = ref(1)
const presetName = ref('')
const diagVisible = ref(false)
const diagLoading = ref(false)
const diag = ref<ChannelDiagnose | null>(null)
const vqd = ref<ChannelVQD | null>(null)
const traffic = ref<ChannelTraffic | null>(null)
const statusVisible = ref(false)
const statusLogs = ref<StatusLog[]>([])
const shareVisible = ref(false)
const shareUrls = ref<Record<string, string>>({})
const shareAuth = ref(false)
const shareExpires = ref('')

async function load() {
  channels.value = await channelApi.list({ keyword: keyword.value })
  const presetId = Number(route.query.channelId)
  if (presetId) {
    const ch = channels.value.find((c) => c.id === presetId)
    if (ch) select(ch)
  }
}

async function select(row: Channel) {
  if (!row) return
  selected.value = row
  try {
    const res = await channelApi.playUrls(row.id)
    playUrls.value = res.playUrls || {}
    pushUrl.value = res.pushUrl
  } catch {
    playUrls.value = {}
  }
  loadPresets()
  loadTraffic()
}

async function loadTraffic() {
  traffic.value = null
  if (!selected.value) return
  try {
    const res = await channelApi.traffic(selected.value.id)
    traffic.value = res.traffic
  } catch {
    traffic.value = null
  }
}

async function openStatus() {
  if (!selected.value) return
  statusVisible.value = true
  statusLogs.value = await channelApi.statusLogs(selected.value.id).catch(() => [])
}

async function openShare() {
  if (!selected.value) return
  try {
    const res = await channelApi.playToken(selected.value.id)
    shareUrls.value = res.playUrls || {}
    shareAuth.value = res.auth
    shareExpires.value = res.expiresAt ? new Date(res.expiresAt).toLocaleString() : ''
    shareVisible.value = true
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '获取分享地址失败')
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择')
  }
}

async function loadPresets() {
  if (!selected.value) return
  try {
    presets.value = await channelApi.ptzPresets(selected.value.id)
  } catch {
    presets.value = []
  }
}

async function ptz(cmd: string) {
  if (!selected.value) return
  try {
    await channelApi.ptz(selected.value.id, { cmd, speed: speed.value })
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '云台控制失败')
  }
}

async function savePreset() {
  if (!selected.value) return
  try {
    const r = await channelApi.savePTZPreset(selected.value.id, { preset: presetNo.value, name: presetName.value })
    if (r.warning) ElMessage.warning('已保存到平台，设备端：' + r.warning)
    else ElMessage.success('预置位已保存')
    presetName.value = ''
    loadPresets()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存预置位失败')
  }
}

async function gotoPreset(p: PTZPreset) {
  if (!selected.value) return
  try {
    await channelApi.gotoPTZPreset(selected.value.id, p.preset)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '调用预置位失败')
  }
}

async function removePreset(p: PTZPreset) {
  if (!selected.value) return
  try {
    await channelApi.removePTZPreset(selected.value.id, p.preset)
    loadPresets()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除预置位失败')
  }
}

async function start() {
  if (!selected.value) return
  try {
    const res = await channelApi.start(selected.value.id)
    playUrls.value = res.playUrls || {}
    ElMessage.success('拉流已启动')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '启动失败，请确认 ZLMediaKit 已运行')
  }
}

onMounted(load)

function levelTag(level?: string) {
  return level === 'critical' ? 'danger' : level === 'warning' ? 'warning' : level === 'info' ? 'info' : 'success'
}

async function runDiagnose() {
  if (!selected.value) return
  diagVisible.value = true
  diagLoading.value = true
  diag.value = null
  vqd.value = null
  const id = selected.value.id
  try {
    diag.value = await channelApi.diagnose(id, 10)
  } catch (e: any) {
    diag.value = { channelId: id, name: '', sourceUrl: '', online: false, status: 'failed', error: e?.response?.data?.message || '诊断失败' }
  }
  try {
    vqd.value = await channelApi.vqd(id)
  } catch (e: any) {
    vqd.value = { channelId: id, name: '', status: 'failed', error: e?.response?.data?.message || '诊断失败' }
  } finally {
    diagLoading.value = false
  }
}
</script>

<style scoped>
.ptz-body {
  display: flex;
  gap: 24px;
  align-items: flex-start;
}
.ptz-pad {
  display: grid;
  grid-template-columns: repeat(3, 48px);
  gap: 6px;
}
.ptz-pad :deep(.el-button) {
  width: 48px;
  height: 40px;
  padding: 0;
}
.ptz-side {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.speed {
  display: flex;
  align-items: center;
  gap: 8px;
}
.muted {
  color: #94a3b8;
  font-size: 12px;
}
.share-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.share-url {
  flex: 1;
  word-break: break-all;
  font-size: 12px;
  color: #334155;
}
</style>
