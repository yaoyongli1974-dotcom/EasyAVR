<template>
  <div class="page map-page">
    <div class="page-header">
      <h2>电子地图</h2>
      <div class="header-actions">
        <el-autocomplete
          v-model="searchText"
          :fetch-suggestions="fetchLocations"
          placeholder="搜索地点，如 西安 / X"
          style="width: 240px"
          clearable
          @select="onSelectLocation"
        >
          <template #default="{ item }">
            <div class="sug"><span>{{ item.name }}</span><span class="muted">{{ item.address }}</span></div>
          </template>
        </el-autocomplete>
        <el-button @click="locateMe"><el-icon><Aim /></el-icon> 定位到我</el-button>
        <el-button @click="importVisible = true">批量导入坐标</el-button>
        <el-select v-model="layer" placeholder="地图图层" style="width: 150px" @change="changeLayer">
          <el-option label="OpenStreetMap" value="osm" />
          <el-option label="高德卫星" value="gaode_sat" />
          <el-option label="高德路网" value="gaode_road" />
        </el-select>
        <el-button @click="loadDevices">刷新设备</el-button>
        <el-button @click="centerOnDevices" :disabled="!hasDevices">定位设备</el-button>
      </div>
    </div>

    <el-row :gutter="16">
      <el-col :span="5">
        <el-card>
          <template #header>
            <span>设备列表</span>
            <el-input v-model="deviceFilter" size="small" placeholder="搜索设备" style="float: right; width: 180px" clearable />
          </template>
          <el-scrollbar height="calc(100vh - 220px)">
            <el-tree
              :data="deviceTree"
              :props="treeProps"
              :default-expanded-keys="[0]"
              highlight-current
              @node-click="onDeviceClick"
            >
              <template #default="{ node, data }">
                <span class="device-tree-node">
                  <el-icon :size="14"><VideoPlay /></el-icon>
                  <span>{{ data.name }}</span>
                  <el-tag v-if="data.longitude && data.latitude" size="small" type="success">已定位</el-tag>
                  <el-tag v-else size="small" type="info">无坐标</el-tag>
                </span>
              </template>
            </el-tree>
          </el-scrollbar>
        </el-card>
      </el-col>

      <el-col :span="19">
        <el-card :body-style="{ padding: '0' }" style="height: calc(100vh - 140px)">
          <div id="map" class="map-container"></div>

          <div class="map-toolbar">
            <el-button-group>
              <el-button :plain="!showTracks" @click="showTracks = !showTracks">
                <el-icon><Location /></el-icon> 轨迹
              </el-button>
              <el-button :plain="!showHeatmap" @click="showHeatmap = !showHeatmap">
                <el-icon><DataBoard /></el-icon> 热力
              </el-button>
              <el-button :plain="!showEvents" @click="toggleEvents">
                <el-icon><Bell /></el-icon> AI 事件
              </el-button>
            </el-button-group>
            <template v-if="showEvents">
              <el-select v-model="eventLevel" placeholder="级别" clearable style="width: 120px" @change="loadEvents">
                <el-option label="提示" value="info" />
                <el-option label="警告" value="warning" />
                <el-option label="严重" value="critical" />
              </el-select>
              <el-input v-model="eventType" placeholder="事件类型" clearable style="width: 140px" @change="loadEvents" />
              <el-tag type="info">定位 {{ eventStats.located }} / {{ eventStats.total }}</el-tag>
              <el-tag v-if="eventStats.byLevel.critical" type="danger">严重 {{ eventStats.byLevel.critical }}</el-tag>
              <el-tag v-if="eventStats.byLevel.warning" type="warning">警告 {{ eventStats.byLevel.warning }}</el-tag>
            </template>
            <el-divider direction="vertical" />
            <el-select v-model="trackDeviceId" placeholder="选择设备查看轨迹" style="width: 180px" @change="loadTrack">
              <el-option v-for="d in mapDevices" :key="d.id" :label="d.name" :value="d.id" />
            </el-select>
            <el-date-picker
              v-model="trackDateRange"
              type="datetimerange"
              range-separator="至"
              start-placeholder="开始时间"
              end-placeholder="结束时间"
              style="width: 320px"
              value-format="YYYY-MM-DDTHH:mm:ss"
              @change="loadTrack"
            />
            <el-button @click="clearTrack" v-if="currentTrack.length > 0">清除轨迹</el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>

    <el-dialog v-model="gpsDialogVisible" title="设置 GPS 坐标" width="480px">
      <el-form :model="gpsForm" label-width="100px">
        <el-form-item label="设备/通道"><el-input :value="gpsForm.targetName" disabled /></el-form-item>
        <el-form-item label="经度"><el-input-number v-model="gpsForm.longitude" :precision="6" :step="0.000001" :min="-180" :max="180" /></el-form-item>
        <el-form-item label="纬度"><el-input-number v-model="gpsForm.latitude" :precision="6" :step="0.000001" :min="-90" :max="90" /></el-form-item>
        <el-form-item label=" "><el-button size="small" @click="useMyLocation"><el-icon><Aim /></el-icon> 使用我的当前位置</el-button></el-form-item>
        <el-form-item label="海拔(米)"><el-input-number v-model="gpsForm.altitude" :precision="1" :step="1" /></el-form-item>
        <el-form-item label="航向(度)"><el-input-number v-model="gpsForm.heading" :precision="1" :step="1" :min="0" :max="360" /></el-form-item>
        <el-form-item label="速度(km/h)"><el-input-number v-model="gpsForm.speed" :precision="1" :step="0.1" :min="0" /></el-form-item>
        <el-form-item label="来源"><el-select v-model="gpsForm.source" style="width: 100%">
          <el-option label="手动" value="manual" />
          <el-option label="设备上报" value="device" />
          <el-option label="通道上报" value="channel" />
          <el-option label="GB28181" value="gb28181" />
        </el-select></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="gpsDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="gpsSaving" @click="saveGPS">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="importVisible" title="批量导入坐标" width="560px">
      <el-form label-width="90px">
        <el-form-item label="导入对象">
          <el-radio-group v-model="importForm.target">
            <el-radio label="device">设备</el-radio>
            <el-radio label="channel">通道</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="CSV">
          <el-input
            v-model="importForm.csv"
            type="textarea"
            :rows="8"
            placeholder="每行：名称,经度,纬度[,海拔]&#10;例如：大门摄像头,116.4074,39.9042"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="importVisible = false">取消</el-button>
        <el-button type="primary" :loading="importing" @click="doImportCoordinates">导入</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="trackDialogVisible" title="轨迹统计" width="520px">
      <el-descriptions :column="2" border v-if="trackStats.pointCount > 0">
        <el-descriptions-item label="轨迹点数">{{ trackStats.pointCount }}</el-descriptions-item>
        <el-descriptions-item label="总里程">{{ (trackStats.totalDistance / 1000).toFixed(2) }} km</el-descriptions-item>
        <el-descriptions-item label="最高速度">{{ trackStats.maxSpeed.toFixed(1) }} km/h</el-descriptions-item>
        <el-descriptions-item label="最低海拔">{{ trackStats.minAltitude.toFixed(1) }} m</el-descriptions-item>
        <el-descriptions-item label="最高海拔">{{ trackStats.maxAltitude.toFixed(1) }} m</el-descriptions-item>
        <el-descriptions-item label="时长">{{ formatDuration(trackStats.durationSec) }}</el-descriptions-item>
      </el-descriptions>
      <div v-else style="padding: 20px; text-align: center; color: var(--el-text-color-placeholder)">暂无轨迹数据</div>
      <template #footer>
        <el-button @click="trackDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="eventDialogVisible" :title="selectedEvent ? `AI 事件 - ${levelLabel(selectedEvent.level)}` : 'AI 事件'" width="640px">
      <template v-if="selectedEvent">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="类型">{{ selectedEvent.eventType }}</el-descriptions-item>
          <el-descriptions-item label="级别">
            <el-tag :type="levelTag(selectedEvent.level)" size="small">{{ levelLabel(selectedEvent.level) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="通道">{{ selectedEvent.channelName }}</el-descriptions-item>
          <el-descriptions-item label="置信度">{{ (selectedEvent.confidence * 100).toFixed(1) }}%</el-descriptions-item>
          <el-descriptions-item label="发生时间" :span="2">{{ formatTime(selectedEvent.occurredAt) }}</el-descriptions-item>
          <el-descriptions-item label="摘要" :span="2">{{ selectedEvent.summary || '-' }}</el-descriptions-item>
        </el-descriptions>
        <div v-if="selectedEvent.snapshot" style="margin-top: 12px">
          <img :src="selectedEvent.snapshot" style="max-width: 100%; border-radius: 8px" />
        </div>
        <div style="margin-top: 12px">
          <div v-if="playLoading" style="color: var(--el-text-color-secondary)">加载视频地址…</div>
          <VideoPlayer v-else-if="hasPlayUrls" :urls="playUrls" />
          <el-alert v-else type="info" :closable="false" title="暂无可用视频地址（流媒体可能未启动）" />
        </div>
      </template>
      <template #footer>
        <el-button @click="eventDialogVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, reactive, computed, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { mapApi, deviceApi, groupApi, channelApi } from '../api'
import type { Device, Channel, TrackPoint, TrackStats, MapEvent, MapEventStats, GeoSuggestion, DeviceGroup } from '../types'
import { VideoPlay, Location, DataBoard, Bell, Aim } from '@element-plus/icons-vue'
import VideoPlayer from '../components/VideoPlayer.vue'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'

// Fix Leaflet default icon issue
delete (L.Icon.Default.prototype as any)._getIconUrl
L.Icon.Default.mergeOptions({
  iconRetinaUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon-2x.png',
  iconUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon.png',
  shadowUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-shadow.png',
})

const layer = ref('osm')
const deviceFilter = ref('')
const deviceTree = ref<any[]>([])
const mapDevices = ref<Device[]>([])
const mapChannels = ref<Channel[]>([])
const treeProps = { children: 'children', label: 'name', value: 'id' }

const map = ref<any>(null)
const tileLayer = ref<any>(null)
const deviceMarkers = ref<(L.Marker | L.CircleMarker)[]>([])
const trackPolyline = ref<L.Polyline | null>(null)
const trackPolylineLayer = ref<L.Layer | null>(null)
const heatmapLayer = ref<any>(null)
const selectedDevice = ref<Device | null>(null)

const showTracks = ref(false)
const showHeatmap = ref(false)
const trackDeviceId = ref<number | null>(null)
const trackDateRange = ref<[string, string] | []>([])
const currentTrack = ref<TrackPoint[]>([])
const trackStats = ref<TrackStats>({
  pointCount: 0, totalDistance: 0, maxSpeed: 0,
  minAltitude: 0, maxAltitude: 0, durationSec: 0
})

const gpsDialogVisible = ref(false)
const gpsSaving = ref(false)
const gpsForm = reactive({
  targetId: 0 as number,
  targetType: '' as 'device' | 'channel',
  targetName: '',
  longitude: 0,
  latitude: 0,
  altitude: 0,
  heading: 0,
  speed: 0,
  source: 'manual' as string
})

const trackDialogVisible = ref(false)

const showEvents = ref(false)
const eventLevel = ref('')
const eventType = ref('')
const mapEvents = ref<MapEvent[]>([])
const eventStats = ref<MapEventStats>({ total: 0, located: 0, byType: {}, byLevel: {} })
const eventMarkers = ref<L.CircleMarker[]>([])
const eventDialogVisible = ref(false)
const selectedEvent = ref<MapEvent | null>(null)
const playUrls = ref<Record<string, string>>({})
const playLoading = ref(false)
const hasPlayUrls = computed(() => Object.keys(playUrls.value).length > 0)

const EVENT_COLORS: Record<string, string> = { critical: '#F56C6C', warning: '#E6A23C', info: '#409EFF' }

const hasDevices = computed(() => mapDevices.value.length > 0)

const searchText = ref('')
const searchMarker = ref<L.CircleMarker | null>(null)
const myMarker = ref<L.CircleMarker | null>(null)
const groups = ref<DeviceGroup[]>([])
const importVisible = ref(false)
const importing = ref(false)
const importForm = reactive({ target: 'device' as 'device' | 'channel', csv: '' })

function resolveGroupCoord(groupId: number): { longitude: number; latitude: number } | null {
  const byId = new Map(groups.value.map(g => [g.id, g]))
  let cur = byId.get(groupId)
  while (cur) {
    if (cur.longitude || cur.latitude) return { longitude: cur.longitude, latitude: cur.latitude }
    cur = cur.parentId ? byId.get(cur.parentId) : undefined
  }
  return null
}

const LAYERS: Record<string, { url: string; attribution: string; subdomains?: string }> = {
  osm: {
    url: 'https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png',
    attribution: '&copy; OpenStreetMap contributors'
  },
  gaode_sat: {
    url: 'https://webst0{s}.is.autonavi.com/appmaptile?style=6&x={x}&y={y}&z={z}',
    attribution: '&copy; 高德地图', subdomains: '1234'
  },
  gaode_road: {
    url: 'https://webrd0{s}.is.autonavi.com/appmaptile?lang=zh_cn&size=1&scale=1&style=8&x={x}&y={y}&z={z}',
    attribution: '&copy; 高德地图', subdomains: '1234'
  }
}

async function loadDevices() {
  try {
    const [devs, chs, grps] = await Promise.all([
      mapApi.devices(),
      mapApi.channels(),
      groupApi.list()
    ])
    mapDevices.value = devs
    mapChannels.value = chs
    groups.value = grps as DeviceGroup[]
    buildDeviceTree(grps)
    renderDeviceMarkers()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载设备失败')
  }
}

function buildDeviceTree(groups: any[]) {
  const groupMap = new Map<number, any>()
  groups.forEach(g => groupMap.set(g.id, { ...g, children: [] }))
  const roots: any[] = []
  groups.forEach(g => {
    const node = groupMap.get(g.id)!
    if (g.parentId === 0) roots.push(node)
    else {
      const parent = groupMap.get(g.parentId)
      if (parent) parent.children.push(node)
    }
  })
  // Attach devices to groups
  const devicesByGroup = new Map<number, Device[]>()
  mapDevices.value.forEach(d => {
    if (d.groupId) {
      const arr = devicesByGroup.get(d.groupId) || []
      arr.push(d)
      devicesByGroup.set(d.groupId, arr)
    }
  })
  function attachDevices(node: any) {
    const devs = devicesByGroup.get(node.id) || []
    devs.forEach(d => node.children.push({ ...d, isDevice: true, leaf: true }))
    node.children.forEach(attachDevices)
  }
  roots.forEach(attachDevices)
  deviceTree.value = roots
}

function renderDeviceMarkers() {
  if (!map.value) return
  deviceMarkers.value.forEach(m => map.value!.removeLayer(m))
  deviceMarkers.value = []

  mapDevices.value.forEach(d => {
    if (d.longitude && d.latitude) {
      const marker = L.marker([d.latitude, d.longitude], {
        icon: L.icon({
          iconUrl: 'https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon.png',
          iconSize: [25, 41], iconAnchor: [12, 41],
          popupAnchor: [1, -34]
        })
      }).addTo(map.value!)
      marker.bindPopup(`<b>${d.name}</b><br>经度: ${d.longitude.toFixed(6)}<br>纬度: ${d.latitude.toFixed(6)}<br>状态: ${d.status}`)
      marker.on('click', () => onDeviceClick(d))
      deviceMarkers.value.push(marker)
    } else if (d.groupId) {
      const base = resolveGroupCoord(d.groupId)
      if (base) {
        const marker = L.circleMarker([base.latitude, base.longitude], {
          radius: 6, color: '#E6A23C', fillColor: '#E6A23C', fillOpacity: 0.7, weight: 2
        }).addTo(map.value!)
        marker.bindPopup(`<b>${d.name}</b> (分组基准坐标)<br>经度: ${base.longitude.toFixed(6)}<br>纬度: ${base.latitude.toFixed(6)}`)
        marker.on('click', () => onDeviceClick(d))
        deviceMarkers.value.push(marker)
      }
    }
  })
  mapChannels.value.forEach(ch => {
    if (ch.longitude && ch.latitude) {
      const marker = L.circleMarker([ch.latitude, ch.longitude], {
        radius: 6, color: '#409EFF', fillColor: '#409EFF', fillOpacity: 0.8, weight: 2
      }).addTo(map.value!)
      marker.bindPopup(`<b>${ch.name}</b> (通道)<br>经度: ${ch.longitude.toFixed(6)}<br>纬度: ${ch.latitude.toFixed(6)}`)
      deviceMarkers.value.push(marker)
    }
  })
}

function changeLayer(val: string) {
  if (!map.value || !tileLayer.value) return
  map.value.removeLayer(tileLayer.value)
  const cfg = LAYERS[val as keyof typeof LAYERS]
  tileLayer.value = L.tileLayer(cfg.url, { attribution: cfg.attribution, subdomains: cfg.subdomains }).addTo(map.value)
}

function initMap() {
  map.value = L.map('map', { center: [39.9042, 116.4074], zoom: 10 })
  const cfg = LAYERS.osm
  tileLayer.value = L.tileLayer(cfg.url, { attribution: cfg.attribution }).addTo(map.value)
  map.value.on('click', (e: L.LeafletMouseEvent) => {
    if (gpsDialogVisible.value) return
    // Could add right-click context menu for adding GPS
  })
}

function centerOnDevices() {
  if (!map.value || deviceMarkers.value.length === 0) return
  const group = L.featureGroup(deviceMarkers.value as unknown as L.Layer[])
  map.value.fitBounds(group.getBounds(), { padding: [50, 50] })
}

async function fetchLocations(query: string, cb: (items: any[]) => void) {
  const q = (query || '').trim()
  if (!q) {
    cb([])
    return
  }
  try {
    const r = await mapApi.geocode(q)
    cb(r.items.map(i => ({ ...i, value: i.name })))
  } catch {
    cb([])
  }
}

function onSelectLocation(item: GeoSuggestion) {
  if (!map.value) return
  map.value.setView([item.latitude, item.longitude], 16)
  if (searchMarker.value) map.value.removeLayer(searchMarker.value)
  searchMarker.value = L.circleMarker([item.latitude, item.longitude], {
    radius: 9, color: '#F56C6C', fillColor: '#F56C6C', fillOpacity: 0.6, weight: 2
  }).addTo(map.value)
  searchMarker.value.bindPopup(`<b>${item.name}</b><br>${item.address}`).openPopup()

  if (selectedDevice.value) {
    const dev = selectedDevice.value
    ElMessageBox.confirm(`将「${item.name}」设为设备「${dev.name}」的坐标？`, '设置坐标', { type: 'info' })
      .then(async () => {
        await mapApi.updateDeviceGPS(dev.id, { longitude: item.longitude, latitude: item.latitude })
        ElMessage.success('已更新设备坐标')
        loadDevices()
      })
      .catch(() => {})
  }
}

function getMyPosition(): Promise<GeolocationPosition> {
  return new Promise((resolve, reject) => {
    if (!navigator.geolocation) {
      reject(new Error('浏览器不支持定位'))
      return
    }
    navigator.geolocation.getCurrentPosition(resolve, reject, { enableHighAccuracy: true, timeout: 10000 })
  })
}

async function locateMe() {
  try {
    const pos = await getMyPosition()
    const { latitude, longitude, accuracy } = pos.coords
    map.value?.setView([latitude, longitude], 16)
    if (myMarker.value && map.value) map.value.removeLayer(myMarker.value)
    if (map.value) {
      myMarker.value = L.circleMarker([latitude, longitude], {
        radius: 8, color: '#409EFF', fillColor: '#409EFF', fillOpacity: 0.8, weight: 2
      }).addTo(map.value)
      myMarker.value.bindPopup('我的位置').openPopup()
    }
    ElMessage.success(`已定位（精度约 ${Math.round(accuracy || 0)} 米）`)
  } catch (e: any) {
    ElMessage.error('定位失败：' + (e?.message || '请检查浏览器定位权限（需 HTTPS）'))
  }
}

async function useMyLocation() {
  try {
    const pos = await getMyPosition()
    gpsForm.longitude = Math.round(pos.coords.longitude * 1e6) / 1e6
    gpsForm.latitude = Math.round(pos.coords.latitude * 1e6) / 1e6
    ElMessage.success('已填入当前位置')
  } catch (e: any) {
    ElMessage.error('定位失败：' + (e?.message || '请检查浏览器定位权限（需 HTTPS）'))
  }
}

async function doImportCoordinates() {
  if (!importForm.csv.trim()) {
    ElMessage.warning('请输入 CSV 内容')
    return
  }
  importing.value = true
  try {
    const r = await mapApi.importCoordinates(importForm)
    ElMessage.success(`已更新 ${r.updated} 条，跳过 ${r.skipped} 条`)
    importVisible.value = false
    importForm.csv = ''
    loadDevices()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '导入失败')
  } finally {
    importing.value = false
  }
}

function onDeviceClick(data: any) {
  if (data.isDevice) {
    selectedDevice.value = data
    map.value?.setView([data.latitude, data.longitude], 16)
  } else {
    // Group clicked - could expand/collapse
  }
}

async function loadTrack() {
  if (!trackDeviceId.value) return
  const params: any = { deviceId: trackDeviceId.value, limit: 5000 }
  if (trackDateRange.value.length === 2) {
    params.start = trackDateRange.value[0]
    params.end = trackDateRange.value[1]
  }
  try {
    const [tracks, stats] = await Promise.all([
      mapApi.tracks(params),
      mapApi.trackStats(params)
    ])
    currentTrack.value = tracks
    trackStats.value = stats
    renderTrack(tracks)
    trackDialogVisible.value = true
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载轨迹失败')
  }
}

function renderTrack(tracks: TrackPoint[]) {
  if (!map.value) return
  if (trackPolylineLayer.value) map.value.removeLayer(trackPolylineLayer.value)
  if (tracks.length < 2) return
  const latlngs = tracks.map(t => [t.latitude, t.longitude] as [number, number])
  const polyline = L.polyline(latlngs, { color: '#409EFF', weight: 3, opacity: 0.8 }).addTo(map.value)
  trackPolyline.value = polyline
  trackPolylineLayer.value = polyline
  map.value.fitBounds(polyline.getBounds(), { padding: [50, 50] })
}

function clearTrack() {
  if (trackPolylineLayer.value && map.value) {
    map.value.removeLayer(trackPolylineLayer.value)
    trackPolylineLayer.value = null
    trackPolyline.value = null
  }
  currentTrack.value = []
  trackStats.value = { pointCount: 0, totalDistance: 0, maxSpeed: 0, minAltitude: 0, maxAltitude: 0, durationSec: 0 }
  trackDialogVisible.value = false
}

function openGPSDialog(device: Device | Channel, type: 'device' | 'channel') {
  gpsForm.targetId = device.id
  gpsForm.targetType = type
  gpsForm.targetName = device.name
  gpsForm.longitude = device.longitude || 0
  gpsForm.latitude = device.latitude || 0
  gpsForm.altitude = device.altitude || 0
  gpsForm.heading = device.heading || 0
  gpsForm.speed = device.speed || 0
  gpsForm.source = 'manual'
  gpsDialogVisible.value = true
}

async function saveGPS() {
  if (!gpsForm.longitude || !gpsForm.latitude) {
    ElMessage.warning('请填写经度和纬度')
    return
  }
  gpsSaving.value = true
  try {
    if (gpsForm.targetType === 'device') {
      await mapApi.updateDeviceGPS(gpsForm.targetId, gpsForm)
    } else {
      await mapApi.updateChannelGPS(gpsForm.targetId, gpsForm)
    }
    ElMessage.success('GPS 坐标已保存')
    gpsDialogVisible.value = false
    await loadDevices()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    gpsSaving.value = false
  }
}

function formatDuration(sec: number) {
  if (sec < 60) return `${sec} 秒`
  if (sec < 3600) return `${Math.floor(sec / 60)} 分 ${sec % 60} 秒`
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  return `${h} 小时 ${m} 分`
}

function toggleEvents() {
  showEvents.value = !showEvents.value
  if (showEvents.value) loadEvents()
  else clearEventMarkers()
}

async function loadEvents() {
  if (!showEvents.value) return
  const params: Record<string, unknown> = { limit: 1000 }
  if (eventLevel.value) params.level = eventLevel.value
  if (eventType.value) params.eventType = eventType.value
  try {
    const [evs, st] = await Promise.all([mapApi.events(params), mapApi.eventStats(params)])
    mapEvents.value = evs
    eventStats.value = st
    renderEventMarkers()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载 AI 事件失败')
  }
}

function renderEventMarkers() {
  if (!map.value) return
  clearEventMarkers()
  mapEvents.value.forEach(ev => {
    const color = EVENT_COLORS[ev.level] || EVENT_COLORS.info
    const marker = L.circleMarker([ev.latitude, ev.longitude], {
      radius: 8, color, fillColor: color, fillOpacity: 0.75, weight: 2
    }).addTo(map.value!)
    marker.bindTooltip(`${ev.eventType} · ${ev.channelName}`, { direction: 'top' })
    marker.on('click', () => openEvent(ev))
    eventMarkers.value.push(marker)
  })
}

function clearEventMarkers() {
  if (map.value) eventMarkers.value.forEach(m => map.value!.removeLayer(m))
  eventMarkers.value = []
}

async function openEvent(ev: MapEvent) {
  selectedEvent.value = ev
  playUrls.value = {}
  eventDialogVisible.value = true
  map.value?.setView([ev.latitude, ev.longitude], 16)
  playLoading.value = true
  try {
    const r = await channelApi.playUrls(ev.channelId)
    playUrls.value = r.playUrls || {}
  } catch {
    playUrls.value = {}
  } finally {
    playLoading.value = false
  }
}

function levelLabel(level: string) {
  return { info: '提示', warning: '警告', critical: '严重' }[level] || level
}
function levelTag(level: string) {
  return ({ info: 'info', warning: 'warning', critical: 'danger' } as Record<string, any>)[level] || 'info'
}
function formatTime(t: string) {
  if (!t) return '-'
  return new Date(t).toLocaleString()
}

onMounted(() => {
  nextTick(() => {
    initMap()
    loadDevices()
  })
})

onUnmounted(() => {
  if (map.value) map.value.remove()
})
</script>

<style scoped>
.map-page { height: 100%; overflow: hidden; }
.page-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px; }
.header-actions { display: flex; gap: 8px; align-items: center; }
.map-container { height: 100%; width: 100%; }
.map-toolbar {
  position: absolute; bottom: 16px; left: 16px; right: 16px;
  background: rgba(255,255,255,0.95); backdrop-filter: blur(8px);
  padding: 12px 16px; border-radius: 8px; box-shadow: 0 2px 12px rgba(0,0,0,0.1);
  display: flex; align-items: center; gap: 12px; flex-wrap: wrap;
}
.device-tree-node { display: flex; align-items: center; gap: 6px; width: 100%; }
.sug { display: flex; justify-content: space-between; gap: 12px; }
.muted { color: #94a3b8; font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
</style>