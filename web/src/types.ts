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
  roi: string
  sensitivity: number
  schedule: string
  enabled: boolean
  status: string
}

export interface AITaskSchedule {
  taskId: number
  schedule: { days: string; start: string; end: string }
  active: boolean
  sensitivity: number
  roi: string
}

export interface AIModel {
  id: number
  name: string
  kind: 'cv' | 'vlm' | 'llm' | 'embedding'
  task: string
  framework: string
  source: string
  description: string
  tags: string
  enabled: boolean
  versionCount?: number
  latestVersion?: string
}

export interface AIModelVersion {
  id: number
  modelId: number
  version: string
  status: string
  format: string
  sizeBytes: number
  checksum: string
  path: string
  url: string
  metrics: string
  labels: string
  params: string
  notes: string
  createdAt: string
}

export interface AIModelDeployment {
  id: number
  name: string
  modelId: number
  versionId: number
  providerId: number
  status: string
  replicas: number
  config: string
  health: string
  deployedAt?: string
  lastHealthAt?: string
  modelName?: string
  version?: string
  providerName?: string
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
  acked: boolean
  ackedAt?: string
  ackedBy: string
}

export interface AlertPolicy {
  id: number
  name: string
  description: string
  enabled: boolean
  priority: number
  kind: string
  eventType: string
  minLevel: string
  channelId: number
  keywords: string
  cooldownSec: number
  ackRequired: boolean
  tiers?: AlertPolicyTier[]
}

export interface AlertPolicyTier {
  id: number
  policyId: number
  tier: number
  minLevel: string
  delaySec: number
  targetIds: string
  template: string
}

export interface AlertDelivery {
  id: number
  policyId: number
  policyName: string
  tier: number
  eventId: number
  channelId: number
  channelName: string
  reason: string
  status: string
  error: string
  createdAt: string
}

export interface AlertStats {
  policies: number
  enabled: number
  deliveries: number
  failed: number
  last24h: number
}

export interface Dataset {
  id: number
  name: string
  description: string
  kind: string
  source: string
  labels: string
  status: string
  sampleCount: number
  labeledCount: number
  createdBy: string
}

export interface DatasetSample {
  id: number
  datasetId: number
  eventId: number
  channelId: number
  imageUrl: string
  labels: string
  split: string
  status: string
  note: string
}

export interface AnnotationTask {
  id: number
  name: string
  datasetId: number
  assignee: string
  status: string
  instructions: string
  total: number
  labeled: number
}

export interface TrainingJob {
  id: number
  name: string
  datasetId: number
  modelId: number
  baseModelId: number
  framework: string
  hyperParams: string
  status: string
  metrics: string
  versionId: number
  log: string
  startedAt?: string
  finishedAt?: string
}

export interface PipelineStats {
  datasets: number
  samples: number
  labeled: number
  annotations: number
  jobs: number
  activeJobs: number
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

export interface MapEvent extends AIEvent {
  channelName: string
  longitude: number
  latitude: number
}

export interface MapEventStats {
  total: number
  located: number
  byType: Record<string, number>
  byLevel: Record<string, number>
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
  marked: boolean
  mark: string
}

export interface ChannelTraffic {
  id?: number
  channelId: number
  streamKey: string
  online: boolean
  bytes: number
  lastSample: string
}

export interface StatusLog {
  id: number
  deviceId: number
  channelId: number
  target: string
  online: boolean
  source: string
  message: string
  ip: string
  loggedAt: string
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

export interface GBBlackList {
  id: number
  deviceId: string
  ua: string
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
  longitude: number
  latitude: number
}

export interface GeoSuggestion {
  name: string
  address: string
  longitude: number
  latitude: number
  source: string
}

export interface PTZPreset {
  id: number
  channelId: number
  preset: number
  name: string
}

export interface ChannelDiagnose {
  channelId: number
  name: string
  sourceUrl: string
  online: boolean
  status: string
  error?: string
  probe?: {
    format: string
    duration: number
    bitRate: number
    streams: number
    latencyMs: number
    video?: { codec: string; profile: string; width: number; height: number; pixFmt: string; bitRate: number; frameRate: number }
    audio?: { codec: string; sampleRate: number; channels: number }
  }
}

export interface ChannelVQD {
  channelId: number
  name: string
  status: string
  error?: string
  level?: string
  eventId?: number
  metrics?: {
    width: number
    height: number
    brightness: number
    contrast: number
    sharpness: number
    blackRatio: number
    blueRatio: number
    domRatio: number
    blockiness: number
    noise: number
    colorCast: string
    castScore: number
  }
  temporal?: { frames: number; meanDiff: number; maxShift: number; shakeHits: number }
  issues?: { code: string; level: string; message: string }[]
}

export interface PlayTokenResult {
  channelId: number
  streamKey: string
  auth: boolean
  playUrls: Record<string, string>
  expiresAt?: string
}

export interface PlatformConfig {
  gb: { enabled: boolean; running: boolean; listen: string; id: string; realm: string; password: string; rtpIp: string }
  ehome: { enabled: boolean; running: boolean; cmsListen: string; smsListen: string; publicIp: string }
  gb35114: { enabled: boolean; running: boolean; sipListen: string; requireClient: boolean; certDir: string }
  playback: { auth: boolean; tokenTtlMin: number; whitelist: string[] }
  monitorSec: number
}

export interface PlatformConfigUpdate {
  gbEnabled?: boolean
  ehomeEnabled?: boolean
  gb35114Enabled?: boolean
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

export interface AuditLog {
  id: number
  userId: number
  username: string
  ip: string
  method: string
  path: string
  action: string
  resource: string
  resourceId: string
  result: string
  errorMsg: string
  requestBody: string
  latencyMs: number
  createdAt: string
}

export interface AuditLogPage {
  items: AuditLog[]
  total: number
  page: number
  pageSize: number
}

export interface PolicyRule {
  pType: 'p' | 'g'
  params: string[]
}

export interface PolicyTestResult {
  allowed: boolean
}
