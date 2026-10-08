import client from './client'
import type {
  AIEvent,
  AIProvider,
  AITask,
  APIKey,
  APIKeyStats,
  APIRequestLog,
  ApiResult,
  AuditLog,
  AuditLogPage,
  Channel,
  ChannelGroupChannel,
  ClusterNode,
  Device,
  DeviceGroup,
  DeviceGroupDevice,
  DiscoveredDevice,
  GA1400Cascade,
  GA1400Subscription,
  GB35114Cert,
  GBCascade,
  GBDevice,
  GBWhiteList,
  IsapiProbeResult,
  NotificationChannel,
  NotificationRule,
  OnvifProbeResult,
  Page,
  Recording,
  RecordingPlan,
  Role,
  SearchHit,
  Snapshot,
  TrackPoint,
  TrackStats,
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
}

export const eventApi = {
  list: (params?: Record<string, unknown>) => unwrap<Page<AIEvent>>(client.get('/events', { params })),
  stats: () => unwrap<Record<string, unknown>>(client.get('/events/stats')),
  ingest: (body: Partial<AIEvent>) => unwrap<AIEvent>(client.post('/events/ingest', body)),
}

export const systemApi = {
  info: () => unwrap<Record<string, any>>(client.get('/system/info')),
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
  create: (body: { name: string; description?: string; parentId?: number; sort?: number }) =>
    unwrap<DeviceGroup>(client.post('/groups', body)),
  update: (id: number, body: { name?: string; description?: string; parentId?: number; sort?: number }) =>
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
}

export const auditApi = {
  list: (params?: Record<string, unknown>) =>
    unwrap<AuditLogPage>(client.get('/audit/logs', { params })),
  get: (id: number) => unwrap<AuditLog>(client.get(`/audit/logs/${id}`)),
  export: (params?: Record<string, unknown>) =>
    client.get('/audit/logs/export', { params, responseType: 'blob' }).then((r) => r.data),
}
