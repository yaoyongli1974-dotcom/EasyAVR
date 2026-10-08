import client from './client'
import type {
  AIEvent,
  AIModel,
  AIModelDeployment,
  AIModelVersion,
  AIProvider,
  AITask,
  AITaskSchedule,
  AlertDelivery,
  AlertPolicy,
  AlertPolicyTier,
  AlertStats,
  AnnotationTask,
  APIKey,
  APIKeyStats,
  APIRequestLog,
  ApiResult,
  AuditLog,
  AuditLogPage,
  Channel,
  ChannelDiagnose,
  ChannelGroupChannel,
  ChannelTraffic,
  ChannelVQD,
  ClusterNode,
  Dataset,
  DatasetSample,
  Device,
  DeviceGroup,
  DeviceGroupDevice,
  DiscoveredDevice,
  GA1400Cascade,
  GA1400Subscription,
  GB35114Cert,
  GBBlackList,
  GBCascade,
  GBDevice,
  GBWhiteList,
  GeoSuggestion,
  IsapiProbeResult,
  MapEvent,
  MapEventStats,
  NotificationChannel,
  NotificationRule,
  OnvifProbeResult,
  Page,
  PipelineStats,
  PlatformConfig,
  PlatformConfigUpdate,
  PlayTokenResult,
  PolicyRule,
  PolicyTestResult,
  PTZPreset,
  Recording,
  RecordingPlan,
  Role,
  SearchHit,
  Snapshot,
  StatusLog,
  TrackPoint,
  TrackStats,
  TrainingJob,
  User,
  UserGroup,
  VideoResource,
} from '../types'

const unwrap = <T>(p: Promise<{ data: ApiResult<T> }>) => p.then((r) => r.data.data)

export const authApi = {
  login: (username: string, password: string) =>
    unwrap<{ token: string; user: User }>(client.post('/auth/login', { username, password })),
  profile: () => unwrap<User>(client.get('/auth/profile')),
}

export const userApi = {
  list: (params?: Record<string, unknown>) => unwrap<User[]>(client.get('/users', { params })),
  create: (body: { username: string; nickname?: string; password: string; role: string; enabled?: boolean }) =>
    unwrap<User>(client.post('/users', body)),
  update: (id: number, body: { nickname?: string; role?: string; enabled?: boolean; password?: string }) =>
    unwrap<User>(client.put(`/users/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/users/${id}`)),
  changePassword: (oldPassword: string, newPassword: string) =>
    unwrap<{ id: number }>(client.post('/auth/password', { oldPassword, newPassword })),
  exportCSV: () => client.get('/users/export', { responseType: 'blob' }).then((r) => r.data as Blob),
  importCSV: (csv: string) => unwrap<{ created: number; skipped: number }>(client.post('/users/import', { csv })),
}

export const roleApi = {
  list: () => unwrap<Role[]>(client.get('/roles')),
  create: (body: { name: string; description?: string; permissions?: string }) =>
    unwrap<Role>(client.post('/roles', body)),
  update: (id: number, body: { name?: string; description?: string; permissions?: string }) =>
    unwrap<Role>(client.put(`/roles/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/roles/${id}`)),
}

export const deviceApi = {
  list: (params?: Record<string, unknown>) => unwrap<Page<Device>>(client.get('/devices', { params })),
  get: (id: number) => unwrap<Device>(client.get(`/devices/${id}`)),
  create: (body: Partial<Device> & { password?: string }) => unwrap<Device>(client.post('/devices', body)),
  update: (id: number, body: Partial<Device> & { password?: string }) =>
    unwrap<Device>(client.put(`/devices/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/devices/${id}`)),
  channels: (id: number) => unwrap<Channel[]>(client.get(`/devices/${id}/channels`)),
  createChannel: (id: number, body: Partial<Channel>) =>
    unwrap<Channel>(client.post(`/devices/${id}/channels`, body)),
  exportCSV: () => client.get('/devices/export', { responseType: 'blob' }).then((r) => r.data as Blob),
  importCSV: (csv: string) => unwrap<{ created: number; skipped: number }>(client.post('/devices/import', { csv })),
  check: (id: number) => unwrap<{ deviceId: number; online: boolean; status: string; message: string }>(client.post(`/devices/${id}/check`)),
  checkAll: () => unwrap<{ total: number; online: number }>(client.post('/devices/check')),
  statusLogs: (id: number) => unwrap<StatusLog[]>(client.get(`/devices/${id}/status-logs`)),
}

export const channelApi = {
  list: (params?: Record<string, unknown>) => unwrap<Channel[]>(client.get('/channels', { params })),
  update: (id: number, body: Partial<Channel>) => unwrap<Channel>(client.put(`/channels/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/channels/${id}`)),
  start: (id: number) =>
    unwrap<{ channel: Channel; detail: Record<string, string>; playUrls: Record<string, string> }>(
      client.post(`/channels/${id}/start`),
    ),
  stop: (id: number) => unwrap<{ id: number }>(client.post(`/channels/${id}/stop`)),
  playUrls: (id: number) =>
    unwrap<{ channelId: number; streamKey: string; online: boolean; playUrls: Record<string, string>; pushUrl: string; sourceUrl: string }>(
      client.get(`/channels/${id}/play-urls`),
    ),
  ptz: (id: number, body: { cmd: string; speed?: number }) => unwrap<{ channelId: number; cmd: string }>(client.post(`/channels/${id}/ptz`, body)),
  ptzPresets: (id: number) => unwrap<PTZPreset[]>(client.get(`/channels/${id}/ptz/presets`)),
  savePTZPreset: (id: number, body: { preset: number; name: string }) =>
    unwrap<{ channelId: number; preset: number; name: string; warning?: string }>(client.post(`/channels/${id}/ptz/presets`, body)),
  gotoPTZPreset: (id: number, preset: number) =>
    unwrap<{ channelId: number; preset: number }>(client.post(`/channels/${id}/ptz/presets/${preset}/goto`)),
  removePTZPreset: (id: number, preset: number) =>
    unwrap<{ channelId: number; preset: number }>(client.delete(`/channels/${id}/ptz/presets/${preset}`)),
  diagnose: (id: number, timeoutSec = 12) =>
    unwrap<ChannelDiagnose>(client.get(`/channels/${id}/diagnose`, { params: { timeoutSec } })),
  vqd: (id: number, record = false) =>
    unwrap<ChannelVQD>(client.get(`/channels/${id}/vqd`, { params: { record: record ? 1 : undefined } })),
  playToken: (id: number) => unwrap<PlayTokenResult>(client.post(`/channels/${id}/play-token`)),
  traffic: (id: number) => unwrap<{ channelId: number; streamKey: string; online: boolean; traffic: ChannelTraffic; media: Record<string, unknown> }>(client.get(`/channels/${id}/traffic`)),
  statusLogs: (id: number) => unwrap<StatusLog[]>(client.get(`/channels/${id}/status-logs`)),
}

export const discoveryApi = {
  scan: (body: { mode?: 'onvif' | 'subnet' | 'all'; subnet?: string; timeoutSec?: number }) =>
    unwrap<DiscoveredDevice[]>(client.post('/discovery/scan', body)),
}

export const isapiApi = {
  probe: (body: { baseUrl?: string; host?: string; port?: number; username?: string; password?: string }) =>
    unwrap<IsapiProbeResult>(client.post('/isapi/probe', body)),
  import: (body: {
    baseUrl?: string
    host?: string
    port?: number
    name?: string
    username?: string
    password?: string
  }) => unwrap<{ device: Device; probe: IsapiProbeResult }>(client.post('/isapi/import', body)),
}

export const onvifApi = {
  probe: (body: { xaddr?: string; host?: string; port?: number; username?: string; password?: string }) =>
    unwrap<OnvifProbeResult>(client.post('/onvif/probe', body)),
  import: (body: {
    xaddr?: string
    host?: string
    port?: number
    name?: string
    manufacturer?: string
    username?: string
    password?: string
  }) => unwrap<{ device: Device; probe: OnvifProbeResult }>(client.post('/onvif/import', body)),
}

export const videoApi = {
  resources: (params?: Record<string, unknown>) =>
    unwrap<Page<VideoResource>>(client.get('/video/resources', { params })),
  stats: () => unwrap<Record<string, unknown>>(client.get('/video/stats')),
  streams: () => unwrap<unknown[]>(client.get('/video/streams')),
  sync: () => unwrap<{ updated: number }>(client.post('/video/sync')),
  trafficSync: () => unwrap<{ channels: number; transitions: number }>(client.post('/video/traffic/sync')),
}

export const aiApi = {
  providers: () => unwrap<AIProvider[]>(client.get('/ai/providers')),
  createProvider: (body: Partial<AIProvider> & { apiKey?: string }) =>
    unwrap<AIProvider>(client.post('/ai/providers', body)),
  updateProvider: (id: number, body: Partial<AIProvider> & { apiKey?: string }) =>
    unwrap<AIProvider>(client.put(`/ai/providers/${id}`, body)),
  removeProvider: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/providers/${id}`)),

  tasks: (params?: Record<string, unknown>) => unwrap<AITask[]>(client.get('/ai/tasks', { params })),
  createTask: (body: Partial<AITask>) => unwrap<AITask>(client.post('/ai/tasks', body)),
  updateTask: (id: number, body: Partial<AITask>) => unwrap<AITask>(client.put(`/ai/tasks/${id}`, body)),
  removeTask: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/tasks/${id}`)),
  startTask: (id: number) => unwrap<{ id: number }>(client.post(`/ai/tasks/${id}/start`)),
  stopTask: (id: number) => unwrap<{ id: number }>(client.post(`/ai/tasks/${id}/stop`)),
  runTask: (id: number) => unwrap<{ events: AIEvent[]; count: number }>(client.post(`/ai/tasks/${id}/run`)),
  taskSchedule: (id: number) => unwrap<AITaskSchedule>(client.get(`/ai/tasks/${id}/schedule`)),
}

export const modelApi = {
  list: (params?: Record<string, unknown>) => unwrap<AIModel[]>(client.get('/ai/models', { params })),
  create: (body: Partial<AIModel>) => unwrap<AIModel>(client.post('/ai/models', body)),
  get: (id: number) =>
    unwrap<{ model: AIModel; versions: AIModelVersion[]; deployments: AIModelDeployment[] }>(client.get(`/ai/models/${id}`)),
  update: (id: number, body: Partial<AIModel>) => unwrap<AIModel>(client.put(`/ai/models/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/models/${id}`)),
  stats: () =>
    unwrap<{ models: number; versions: number; deployments: number; active: number }>(client.get('/ai/model-stats')),

  versions: (modelId: number) => unwrap<AIModelVersion[]>(client.get(`/ai/models/${modelId}/versions`)),
  createVersion: (modelId: number, body: Partial<AIModelVersion>) =>
    unwrap<AIModelVersion>(client.post(`/ai/models/${modelId}/versions`, body)),
  updateVersion: (modelId: number, versionId: number, body: Partial<AIModelVersion>) =>
    unwrap<AIModelVersion>(client.put(`/ai/models/${modelId}/versions/${versionId}`, body)),
  archiveVersion: (modelId: number, versionId: number) =>
    unwrap<{ id: number; status: string }>(client.post(`/ai/models/${modelId}/versions/${versionId}/archive`)),
  removeVersion: (modelId: number, versionId: number) =>
    unwrap<{ id: number }>(client.delete(`/ai/models/${modelId}/versions/${versionId}`)),

  deployments: (params?: Record<string, unknown>) =>
    unwrap<AIModelDeployment[]>(client.get('/ai/deployments', { params })),
  createDeployment: (body: { modelId: number; versionId: number; providerId: number; name?: string; replicas?: number; config?: string }) =>
    unwrap<AIModelDeployment>(client.post('/ai/deployments', body)),
  updateDeployment: (id: number, body: Partial<AIModelDeployment>) =>
    unwrap<AIModelDeployment>(client.put(`/ai/deployments/${id}`, body)),
  activateDeployment: (id: number) => unwrap<{ id: number; status: string }>(client.post(`/ai/deployments/${id}/activate`)),
  stopDeployment: (id: number) => unwrap<{ id: number; status: string }>(client.post(`/ai/deployments/${id}/stop`)),
  removeDeployment: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/deployments/${id}`)),
}

export const eventApi = {
  list: (params?: Record<string, unknown>) => unwrap<Page<AIEvent>>(client.get('/events', { params })),
  stats: () => unwrap<Record<string, unknown>>(client.get('/events/stats')),
  ingest: (body: Partial<AIEvent>) => unwrap<AIEvent>(client.post('/events/ingest', body)),
}

export const systemApi = {
  info: () => unwrap<Record<string, any>>(client.get('/system/info')),
}

export const configApi = {
  platform: () => unwrap<PlatformConfig>(client.get('/config/platform')),
  updatePlatform: (body: PlatformConfigUpdate) =>
    unwrap<{ warnings: string[] }>(client.put('/config/platform', body)),
}

export const recordingApi = {
  list: (params?: Record<string, unknown>) => unwrap<Recording[]>(client.get('/recordings', { params })),
  start: (channelId: number) => unwrap<{ id: number }>(client.post(`/channels/${channelId}/record/start`)),
  stop: (channelId: number) => unwrap<{ id: number }>(client.post(`/channels/${channelId}/record/stop`)),
  sync: (channelId: number, date: string) =>
    unwrap<{ items: Recording[]; count: number }>(client.post(`/channels/${channelId}/recordings/sync`, null, { params: { date } })),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/recordings/${id}`)),
  getPlan: (channelId: number) => unwrap<RecordingPlan>(client.get(`/channels/${channelId}/recording-plan`)),
  savePlan: (channelId: number, plan: Partial<RecordingPlan>) =>
    unwrap<RecordingPlan>(client.put(`/channels/${channelId}/recording-plan`, plan)),
  mark: (id: number, body: { marked: boolean; mark?: string }) =>
    unwrap<Recording>(client.put(`/recordings/${id}/mark`, body)),
  cleanup: (days = 0) => unwrap<{ removed: number }>(client.post('/recordings/cleanup', null, { params: { days } })),
}

export const snapshotApi = {
  list: (params?: Record<string, unknown>) =>
    unwrap<Page<Snapshot>>(client.get('/snapshots', { params })),
  capture: (channelId: number) => unwrap<Snapshot>(client.post(`/channels/${channelId}/snapshot`)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/snapshots/${id}`)),
}

export const gbApi = {
  devices: () => unwrap<GBDevice[]>(client.get('/gb/devices')),
  config: () => unwrap<Record<string, any>>(client.get('/gb/config')),
  refresh: (gbid: string) => unwrap<{ deviceId: string }>(client.post(`/gb/devices/${gbid}/refresh`)),

  cascades: () => unwrap<GBCascade[]>(client.get('/gb/cascades')),
  createCascade: (body: Partial<GBCascade> & { password?: string }) =>
    unwrap<GBCascade>(client.post('/gb/cascades', body)),
  refreshCascade: (id: number) => unwrap<{ id: number }>(client.post(`/gb/cascades/${id}/refresh`)),
  removeCascade: (id: number) => unwrap<{ id: number }>(client.delete(`/gb/cascades/${id}`)),

  whitelist: () => unwrap<GBWhiteList[]>(client.get('/gb/whitelist')),
  createWhitelist: (body: Partial<GBWhiteList> & { password?: string }) =>
    unwrap<GBWhiteList>(client.post('/gb/whitelist', body)),
  removeWhitelist: (id: number) => unwrap<{ id: number }>(client.delete(`/gb/whitelist/${id}`)),

  blacklist: () => unwrap<GBBlackList[]>(client.get('/gb/blacklist')),
  createBlacklist: (body: Partial<GBBlackList>) =>
    unwrap<GBBlackList>(client.post('/gb/blacklist', body)),
  removeBlacklist: (id: number) => unwrap<{ id: number }>(client.delete(`/gb/blacklist/${id}`)),
}

export const notifyApi = {
  channels: () => unwrap<NotificationChannel[]>(client.get('/notify/channels')),
  createChannel: (body: Partial<NotificationChannel> & { secret?: string; smtpPassword?: string }) =>
    unwrap<NotificationChannel>(client.post('/notify/channels', body)),
  updateChannel: (id: number, body: Partial<NotificationChannel> & { secret?: string; smtpPassword?: string }) =>
    unwrap<NotificationChannel>(client.put(`/notify/channels/${id}`, body)),
  removeChannel: (id: number) => unwrap<{ id: number }>(client.delete(`/notify/channels/${id}`)),
  testChannel: (id: number) => unwrap<{ id: number; sent: boolean }>(client.post(`/notify/channels/${id}/test`)),

  rules: () => unwrap<NotificationRule[]>(client.get('/notify/rules')),
  createRule: (body: Partial<NotificationRule>) => unwrap<NotificationRule>(client.post('/notify/rules', body)),
  updateRule: (id: number, body: Partial<NotificationRule>) => unwrap<NotificationRule>(client.put(`/notify/rules/${id}`, body)),
  removeRule: (id: number) => unwrap<{ id: number }>(client.delete(`/notify/rules/${id}`)),
}

export const searchApi = {
  search: (query: string, topK = 10) =>
    unwrap<{ hits: SearchHit[]; semantic: boolean; count: number }>(client.post('/ai/search', { query, topK })),
}

export const alertApi = {
  policies: () => unwrap<AlertPolicy[]>(client.get('/alert/policies')),
  policy: (id: number) => unwrap<AlertPolicy>(client.get(`/alert/policies/${id}`)),
  createPolicy: (body: Partial<AlertPolicy>) => unwrap<AlertPolicy>(client.post('/alert/policies', body)),
  updatePolicy: (id: number, body: Partial<AlertPolicy>) => unwrap<AlertPolicy>(client.put(`/alert/policies/${id}`, body)),
  removePolicy: (id: number) => unwrap<{ id: number }>(client.delete(`/alert/policies/${id}`)),
  testPolicy: (id: number) => unwrap<{ id: number; sent: number }>(client.post(`/alert/policies/${id}/test`)),
  tiers: (id: number) => unwrap<AlertPolicyTier[]>(client.get(`/alert/policies/${id}/tiers`)),
  createTier: (id: number, body: Partial<AlertPolicyTier>) =>
    unwrap<AlertPolicyTier>(client.post(`/alert/policies/${id}/tiers`, body)),
  updateTier: (id: number, tierId: number, body: Partial<AlertPolicyTier>) =>
    unwrap<AlertPolicyTier>(client.put(`/alert/policies/${id}/tiers/${tierId}`, body)),
  removeTier: (id: number, tierId: number) =>
    unwrap<{ id: number }>(client.delete(`/alert/policies/${id}/tiers/${tierId}`)),
  deliveries: (params?: Record<string, unknown>) =>
    unwrap<Page<AlertDelivery>>(client.get('/alert/deliveries', { params })),
  stats: () => unwrap<AlertStats>(client.get('/alert/stats')),
  ackEvent: (eventId: number) => unwrap<{ id: number; acked: boolean }>(client.post(`/events/${eventId}/ack`)),
}

export const pipelineApi = {
  stats: () => unwrap<PipelineStats>(client.get('/ai/pipeline-stats')),

  datasets: () => unwrap<Dataset[]>(client.get('/ai/datasets')),
  dataset: (id: number) =>
    unwrap<{ dataset: Dataset; samples: DatasetSample[] }>(client.get(`/ai/datasets/${id}`)),
  createDataset: (body: Partial<Dataset>) => unwrap<Dataset>(client.post('/ai/datasets', body)),
  updateDataset: (id: number, body: Partial<Dataset>) => unwrap<Dataset>(client.put(`/ai/datasets/${id}`, body)),
  removeDataset: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/datasets/${id}`)),

  samples: (id: number, params?: Record<string, unknown>) =>
    unwrap<DatasetSample[]>(client.get(`/ai/datasets/${id}/samples`, { params })),
  createSample: (id: number, body: Partial<DatasetSample>) =>
    unwrap<DatasetSample>(client.post(`/ai/datasets/${id}/samples`, body)),
  updateSample: (id: number, sampleId: number, body: Partial<DatasetSample>) =>
    unwrap<DatasetSample>(client.put(`/ai/datasets/${id}/samples/${sampleId}`, body)),
  removeSample: (id: number, sampleId: number) =>
    unwrap<{ id: number }>(client.delete(`/ai/datasets/${id}/samples/${sampleId}`)),
  importSamples: (id: number, body: { eventIds: number[]; split?: string }) =>
    unwrap<{ created: number; dataset: Dataset }>(client.post(`/ai/datasets/${id}/samples/import`, body)),

  annotations: (params?: Record<string, unknown>) =>
    unwrap<AnnotationTask[]>(client.get('/ai/annotations', { params })),
  createAnnotation: (body: Partial<AnnotationTask>) => unwrap<AnnotationTask>(client.post('/ai/annotations', body)),
  updateAnnotation: (id: number, body: Partial<AnnotationTask>) =>
    unwrap<AnnotationTask>(client.put(`/ai/annotations/${id}`, body)),
  completeAnnotation: (id: number) =>
    unwrap<{ id: number; status: string }>(client.post(`/ai/annotations/${id}/complete`)),
  removeAnnotation: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/annotations/${id}`)),

  jobs: (params?: Record<string, unknown>) => unwrap<TrainingJob[]>(client.get('/ai/training', { params })),
  createJob: (body: Partial<TrainingJob>) => unwrap<TrainingJob>(client.post('/ai/training', body)),
  updateJob: (id: number, body: Partial<TrainingJob>) => unwrap<TrainingJob>(client.put(`/ai/training/${id}`, body)),
  runJob: (id: number) =>
    unwrap<{ job: TrainingJob; model: AIModel; version: AIModelVersion }>(client.post(`/ai/training/${id}/run`)),
  cancelJob: (id: number) => unwrap<{ id: number; status: string }>(client.post(`/ai/training/${id}/cancel`)),
  removeJob: (id: number) => unwrap<{ id: number }>(client.delete(`/ai/training/${id}`)),
}

export const clusterApi = {
  nodes: () => unwrap<ClusterNode[]>(client.get('/cluster/nodes')),
  stats: () => unwrap<Array<ClusterNode & { devices: number }>>(client.get('/cluster/stats')),
  config: () => unwrap<Record<string, any>>(client.get('/cluster/config')),
}

export const ga1400Api = {
  cascades: () => unwrap<GA1400Cascade[]>(client.get('/ga1400/cascades')),
  createCascade: (body: Partial<GA1400Cascade> & { password?: string }) =>
    unwrap<GA1400Cascade>(client.post('/ga1400/cascades', body)),
  testCascade: (id: number) => unwrap<{ id: number; ok: boolean }>(client.post(`/ga1400/cascades/${id}/test`)),
  syncCascade: (id: number) => unwrap<{ id: number; count: number }>(client.post(`/ga1400/cascades/${id}/sync`)),
  removeCascade: (id: number) => unwrap<{ id: number }>(client.delete(`/ga1400/cascades/${id}`)),
  subscriptions: () => unwrap<GA1400Subscription[]>(client.get('/ga1400/subscriptions')),
}

export const gb35114Api = {
  config: () => unwrap<Record<string, any>>(client.get('/gb35114/config')),
  generateCert: () => unwrap<{ cert: string; key: string }>(client.post('/gb35114/platform-cert')),
  signCsr: (csr: string) => unwrap<{ cert: string }>(client.post('/gb35114/sign-csr', { csr })),
  enroll: (csr: string, deviceId?: string) =>
    unwrap<{ cert: string; record: GB35114Cert }>(client.post('/gb35114/enroll', { csr, deviceId })),
  verify: (cert: string) =>
    unwrap<{ valid: boolean; fingerprint: string; info: Record<string, any> }>(
      client.post('/gb35114/verify', { cert }),
    ),
  certs: () => unwrap<GB35114Cert[]>(client.get('/gb35114/certs')),
  revoke: (id: number) => unwrap<{ id: number }>(client.post(`/gb35114/certs/${id}/revoke`)),
  sm3: (data: string) => unwrap<{ sm3: string }>(client.post('/gb35114/sm3', { data })),
}

export const apiKeyApi = {
  list: () => unwrap<APIKey[]>(client.get('/apikeys')),
  create: (body: { name: string; scopes?: string; expiresAt?: string; rateLimit?: number; quotaPerDay?: number }) =>
    unwrap<{ key: APIKey; secret: string }>(client.post('/apikeys', body)),
  update: (id: number, body: Partial<APIKey>) => unwrap<APIKey>(client.put(`/apikeys/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/apikeys/${id}`)),
  stats: () => unwrap<APIKeyStats>(client.get('/apikeys/stats')),
  logs: (id?: number, limit = 100) =>
    unwrap<APIRequestLog[]>(client.get(id ? `/apikeys/${id}/logs` : '/apikeys/logs', { params: { limit } })),
  openapi: () => client.get('/openapi.json').then((r) => r.data),
}

export const groupApi = {
  list: () => unwrap<DeviceGroup[]>(client.get('/groups')),
  create: (body: { name: string; description?: string; parentId?: number; sort?: number; longitude?: number; latitude?: number }) =>
    unwrap<DeviceGroup>(client.post('/groups', body)),
  update: (id: number, body: { name?: string; description?: string; parentId?: number; sort?: number; longitude?: number; latitude?: number }) =>
    unwrap<DeviceGroup>(client.put(`/groups/${id}`, body)),
  remove: (id: number) => unwrap<{ id: number }>(client.delete(`/groups/${id}`)),

  bindDevices: (body: { groupId: number; deviceIds: number[] }) =>
    unwrap<{ count: number }>(client.post('/groups/devices/bind', body)),
  unbindDevices: (body: { groupId: number; deviceIds: number[] }) =>
    unwrap<{ groupId: number }>(client.post('/groups/devices/unbind', body)),
  groupDevices: (id: number) => unwrap<Device[]>(client.get(`/groups/${id}/devices`)),

  bindChannels: (body: { groupId: number; channelIds: number[] }) =>
    unwrap<{ count: number }>(client.post('/groups/channels/bind', body)),
  unbindChannels: (body: { groupId: number; channelIds: number[] }) =>
    unwrap<{ groupId: number }>(client.post('/groups/channels/unbind', body)),
  groupChannels: (id: number) => unwrap<Channel[]>(client.get(`/groups/${id}/channels`)),

  userGroups: () => unwrap<UserGroup[]>(client.get('/groups/user-groups')),
  assignUserGroup: (body: { userId: number; groupId: number; permissions?: string }) =>
    unwrap<UserGroup>(client.post('/groups/user-groups', body)),
  updateUserGroup: (id: number, body: { permissions?: string }) =>
    unwrap<UserGroup>(client.put(`/groups/user-groups/${id}`, body)),
  removeUserGroup: (id: number) => unwrap<{ id: number }>(client.delete(`/groups/user-groups/${id}`)),
}

export const mapApi = {
  devices: (params?: Record<string, unknown>) =>
    unwrap<Device[]>(client.get('/map/devices', { params })),
  channels: (params?: Record<string, unknown>) =>
    unwrap<Channel[]>(client.get('/map/channels', { params })),
  updateDeviceGPS: (id: number, body: { longitude: number; latitude: number; altitude?: number; heading?: number; speed?: number; gpsTime?: string; accuracy?: number; source?: string }) =>
    unwrap<Device>(client.post(`/map/devices/${id}/gps`, body)),
  updateChannelGPS: (id: number, body: { longitude: number; latitude: number; altitude?: number; heading?: number; speed?: number; gpsTime?: string; accuracy?: number; source?: string }) =>
    unwrap<Channel>(client.post(`/map/channels/${id}/gps`, body)),
  tracks: (params?: { deviceId?: number; channelId?: number; start?: string; end?: string; limit?: number }) =>
    unwrap<TrackPoint[]>(client.get('/map/tracks', { params })),
  trackStats: (params?: { deviceId?: number; channelId?: number; start?: string; end?: string }) =>
    unwrap<TrackStats>(client.get('/map/tracks/stats', { params })),
  events: (params?: Record<string, unknown>) =>
    unwrap<MapEvent[]>(client.get('/map/events', { params })),
  eventStats: (params?: Record<string, unknown>) =>
    unwrap<MapEventStats>(client.get('/map/events/stats', { params })),
  geocode: (q: string, limit = 8) =>
    unwrap<{ provider: string; items: GeoSuggestion[] }>(client.get('/map/geocode', { params: { q, limit } })),
  importCoordinates: (body: { target: 'device' | 'channel'; csv: string }) =>
    unwrap<{ updated: number; skipped: number }>(client.post('/map/import', body)),
}

export const auditApi = {
  list: (params?: Record<string, unknown>) =>
    unwrap<AuditLogPage>(client.get('/audit/logs', { params })),
  get: (id: number) => unwrap<AuditLog>(client.get(`/audit/logs/${id}`)),
  export: (params?: Record<string, unknown>) =>
    client.get('/audit/logs/export', { params, responseType: 'blob' }).then((r) => r.data),
}

export const policyApi = {
  list: () => unwrap<PolicyRule[]>(client.get('/policy')),
  add: (body: { pType: 'p' | 'g'; params: string[] }) =>
    unwrap<{ added: boolean }>(client.post('/policy', body)),
  remove: (body: { pType: 'p' | 'g'; params: string[] }) =>
    unwrap<{ removed: boolean }>(client.delete('/policy', { data: body })),
  test: (body: { userId: number; username: string; role: string; resource: string; action: string; domain: string }) =>
    unwrap<PolicyTestResult>(client.post('/policy/test', body)),
  userRoles: (id: number) =>
    unwrap<{ userId: number; username: string; roles: string[] }>(client.get(`/policy/user/${id}/roles`)),
  seed: () => unwrap<{ message: string }>(client.post('/policy/seed')),
}
