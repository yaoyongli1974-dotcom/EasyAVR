<template>
  <div class="video-wrap">
    <div class="video-box">
      <video ref="videoEl" controls autoplay muted playsinline></video>
      <div v-if="error" class="err">{{ error }}</div>
    </div>
    <div class="bar">
      <el-select v-model="protocol" size="small" style="width: 140px" @change="reload">
        <el-option label="HTTP-FLV" value="http-flv" />
        <el-option label="HLS" value="hls" />
        <el-option label="WebRTC" value="webrtc" />
        <el-option label="HTTP-FMP4" value="http-fmp4" />
      </el-select>
      <el-input v-model="currentUrl" size="small" readonly style="flex: 1; margin: 0 8px" />
      <el-button size="small" @click="copy">复制</el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import Hls from 'hls.js'
import mpegts from 'mpegts.js'

const props = defineProps<{ urls: Record<string, string>; initial?: string }>()
const protocol = ref(props.initial || 'http-flv')
const videoEl = ref<HTMLVideoElement | null>(null)
const error = ref('')
let player: any = null
let pc: RTCPeerConnection | null = null
let hls: Hls | null = null

const currentUrl = computed(() => props.urls?.[protocol.value] || '')

function cleanup() {
  if (player) {
    try {
      player.destroy()
    } catch {}
    player = null
  }
  if (hls) {
    hls.destroy()
    hls = null
  }
  if (pc) {
    pc.close()
    pc = null
  }
  if (videoEl.value) {
    videoEl.value.src = ''
    videoEl.value.srcObject = null
  }
}

function reload() {
  error.value = ''
  cleanup()
  const url = currentUrl.value
  const video = videoEl.value
  if (!url || !video) return
  if (protocol.value === 'http-flv' || protocol.value === 'http-fmp4') {
    if (!mpegts.isSupported()) {
      error.value = '当前浏览器不支持 MSE 播放'
      return
    }
    player = mpegts.createPlayer({ type: protocol.value === 'http-flv' ? 'flv' : 'mp4', url, isLive: true })
    player.attachMediaElement(video)
    player.on(mpegts.Events.ERROR, (t: string, d: string) => (error.value = `${t}: ${d}`))
    player.load()
    player.play().catch(() => {})
  } else if (protocol.value === 'hls') {
    if (Hls.isSupported()) {
      hls = new Hls({ liveDurationInfinity: true })
      hls.loadSource(url)
      hls.attachMedia(video)
      hls.on(Hls.Events.ERROR, (_e, data) => (error.value = data.details))
    } else {
      video.src = url
    }
    video.play().catch(() => {})
  } else if (protocol.value === 'webrtc') {
    playWebRTC(url, video)
  }
}

async function playWebRTC(url: string, video: HTMLVideoElement) {
  try {
    pc = new RTCPeerConnection()
    pc.addTransceiver('video', { direction: 'recvonly' })
    pc.addTransceiver('audio', { direction: 'recvonly' })
    pc.ontrack = (e) => {
      video.srcObject = e.streams[0]
      video.play().catch(() => {})
    }
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: offer.sdp,
    })
    const answer = await res.text()
    await pc.setRemoteDescription({ type: 'answer', sdp: answer })
  } catch (e: any) {
    error.value = 'WebRTC 播放失败：' + (e?.message || e)
  }
}

function copy() {
  navigator.clipboard?.writeText(currentUrl.value)
}

watch(() => props.urls, reload, { deep: true })
watch(() => props.initial, (v) => { if (v) { protocol.value = v; reload() } })

reload()
onBeforeUnmount(cleanup)
defineExpose({ reload })
</script>

<style scoped>
.video-wrap {
  width: 100%;
}
.video-box {
  position: relative;
  width: 100%;
  background: #000;
  aspect-ratio: 16 / 9;
  border-radius: 8px;
  overflow: hidden;
}
.video-box video {
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.err {
  position: absolute;
  bottom: 8px;
  left: 8px;
  color: #fca5a5;
  font-size: 12px;
}
.bar {
  display: flex;
  align-items: center;
  margin-top: 8px;
}
</style>
