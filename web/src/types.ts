export interface ApiResult<T> {
  code: number
  message: string
  data: T
}

export interface User {
  id: number
  username: string
  nickname: string
  role: string
  enabled: boolean
}

export interface Role {
  id: number
  name: string
  description: string
  permissions: string
  builtin: boolean
}

export interface DiscoveredDevice {
  ip: string
  port: number
  xaddr: string
  name: string
  manufacturer: string
  model: string
  location: string
  protocol: string
  source: string
  added: boolean
}

export interface IsapiStreamChannel {
  id: string
  name: string
  streamType: string
  rtspPort: number
  rtspUrl: string
}

export interface IsapiProbeResult {
  host: string
  deviceInfo: {
    name: string
    deviceId: string
    model: string
    serial: string
    firmware: string
    macAddress: string
  }
  channels: IsapiStreamChannel[]
}

export interface OnvifProfile {
  token: string
  name: string
  streamUri: string
}

export interface OnvifProbeResult {
  xaddr: string
  mediaXaddr: string
  deviceInfo: {
    manufacturer: string
    model: string
    firmware: string
    serial: string
    hardware: string
  }
  profiles: OnvifProfile[]
}

export interface Channel {
  id: number
  deviceId: number
  name: string
  streamType: string
  sourceUrl: string
  streamKey: string
  online: boolean
  enabled: boolean
  recording: boolean
  description: string
  snapshotEnabled: boolean
  snapshotInterval: number
  gbDeviceId: string
  gbChannelId: string
  longitude?: number
  latitude?: number
  altitude?: number
  heading?: number
  speed?: number
  gpsTime?: string
}

export interface Device {
  id: number
  name: string
  protocol: string
  accessMode: string
  manufacturer: string
  ip: string
  port: number
  username: string
  status: string
  groupId: number
  channels?: Channel[]
  longitude?: number
  latitude?: number
  altitude?: number
  heading?: number
  speed?: number
  gpsTime?: string
}

export interface AIProvider {
  id: number
  name: string
  kind: 'cv' | 'vlm' | 'llm' | 'embedding'
  vendor: string
  endpoint: string
  model: string
  enabled: boolean
}

export interface AITask {
  id: number
  name: string
  channelId: number
  providerId: number
  taskType: string
  config: string
  enabled: boolean
  status: string
}

export interface AIEvent {
  id: number
  channelId: number
  taskId: number
  providerId: number
  kind: string
  eventType: string
  level: string
  confidence: number
  summary: string
  payload: string
  snapshot: string
  occurredAt: string
  createdAt: string
}

export interface VideoResource {
  id: number
  channelId: number
  kind: string
  protocol: string
  url: string
  path: string
  sizeBytes: number
  duration: number
  tags: string
}

export interface Page<T> {
  items: T[]
  total: number
  page: number
  pageSize: number
}

export interface Recording {
  id: number
  channelId: number
  streamKey: string
  date: string
  file: string
  url: string
  sizeBytes: number
  duration: number
  startTime: string
}

export interface RecordingPlan {
  id?: number
  channelId: number
  enabled: boolean
  days: string
  startTime: string
  endTime: string
  retentionDays: number
  streamType: string
}

export interface Snapshot {
  id: number
  channelId: number
  path: string
  url: string
  sizeBytes: number
  takenAt: string
}

export interface GBDevice {
  id: number
  deviceId: string
  name: string
  manufacturer: string
  model: string
  firmware: string
  ip: string
  port: number
  transport: string
  channelCount: number
  online: boolean
  registeredAt: string
  lastKeepalive: string
  expires: number
}

export interface NotificationChannel {
  id: number
  name: string
  type: 'webhook' | 'email'
  enabled: boolean
  url: string
  smtpHost: string
  smtpPort: number
  smtpUser: string
  from: string
  to: string
  useTls: boolean
}

export interface NotificationRule {
  id: number
  name: string
  enabled: boolean
  targetIds: string
  minLevel: string
  kind: string
  eventType: string
}

export interface ClusterNode {
  id: number
  nodeId: string
  name: string
  apiBase: string
  status: string
  isSelf: boolean
  lastHeartbeat: string
}

export interface GBCascade {
  id: number
  name: string
  targetId: string
  localId: string
  targetIp: string
  targetPort: number
  transport: string
  enabled: boolean
  online: boolean
  lastHeartbeat: string
}

export interface GBWhiteList {
  id: number
  deviceId: string
  ip: string
  port: number
  protocol: string
  enabled: boolean
  description: string
}

export interface SearchHit {
  event: AIEvent
  score: number
}

export interface GA1400Cascade {
  id: number
  name: string
  direction: 'up' | 'down'
  platformId: string
  url: string
  username: string
  enabled: boolean
  online: boolean
  lastHeartbeat: string
  lastSyncAt: string
  syncCount: number
}

export interface GA1400Subscription {
  id: number
  cascadeId: number
  subscribeId: string
  title: string
  eventTypes: string
  status: string
  expiresAt: string
  lastRenewAt: string
  renewCount: number
}

export interface APIKey {
  id: number
  name: string
  prefix: string
  scopes: string
  enabled: boolean
  expiresAt?: string
  lastUsedAt?: string
  lastUsedIp: string
  rateLimit: number
  quotaPerDay: number
  usedToday: number
  quotaResetAt: string
  createdAt: string
}

export interface APIRequestLog {
  id: number
  keyId: number
  keyName: string
  method: string
  path: string
  status: number
  latencyMs: number
  ip: string
  createdAt: string
}

export interface APIKeyStats {
  keys: number
  enabled: number
  requestsToday: number
  requestsTotal: number
  topKeys: Array<{ keyId: number; keyName: string; count: number }>
}

export interface GB35114Cert {
  id: number
  deviceId: string
  serial: string
  subject: string
  status: string
  notBefore: string
  notAfter: string
  revokedAt?: string
}

export interface DeviceGroup {
  id: number
  name: string
  description: string
  parentId: number
  path: string
  sort: number
}

export interface DeviceGroupDevice {
  deviceId: number
  groupId: number
}

export interface ChannelGroupChannel {
  channelId: number
  groupId: number
}

export interface UserGroup {
  id: number
  userId: number
  groupId: number
  permissions: string
  user?: User
  group?: DeviceGroup
}

export interface GPSPosition {
  longitude: number
  latitude: number
  altitude?: number
  heading?: number
  speed?: number
  gpsTime?: string
}

export interface TrackPoint {
  id: number
  deviceId: number
  channelId: number
  longitude: number
  latitude: number
  altitude: number
  heading: number
  speed: number
  accuracy: number
  source: string
  trackTime: string
}

export interface TrackStats {
  pointCount: number
  totalDistance: number
  maxSpeed: number
  minAltitude: number
  maxAltitude: number
  durationSec: number
}
