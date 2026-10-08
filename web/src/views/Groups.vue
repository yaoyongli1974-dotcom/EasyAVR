<template>
  <div class="page">
    <div class="page-header">
      <h2>设备分组</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-row :gutter="20">
      <el-col :span="7">
        <el-card>
          <template #header>
            <span>分组树</span>
            <el-button type="primary" size="small" style="float: right" @click="openGroup()">添加分组</el-button>
          </template>
          <el-tree
            :data="groups"
            :props="treeProps"
            :default-expanded-keys="[0]"
            highlight-current
            @node-click="onGroupClick"
          >
            <template #default="{ node, data }">
              <span class="group-node">
                <span>{{ data.name }}</span>
                <el-tag v-if="data.parentId === 0" size="small" type="primary">根</el-tag>
              </span>
            </template>
          </el-tree>
        </el-card>
      </el-col>

      <el-col :span="17">
        <el-card v-if="selectedGroup" :body-style="{ padding: '0' }">
          <el-tabs v-model="tab" @tab-change="onTabChange">
            <el-tab-pane label="基本信息" name="info">
              <el-form :model="groupForm" label-width="90px" style="padding: 20px">
                <el-form-item label="分组名"><el-input v-model="groupForm.name" /></el-form-item>
                <el-form-item label="描述"><el-input v-model="groupForm.description" type="textarea" /></el-form-item>
                <el-form-item label="父分组">
                  <el-select v-model="groupForm.parentId" style="width: 100%" placeholder="无（根分组）">
                    <el-option v-for="g in flatGroups" :key="g.id" :label="g.name" :value="g.id" />
                  </el-select>
                </el-form-item>
                <el-form-item label="排序"><el-input-number v-model.number="groupForm.sort" :min="0" /></el-form-item>
                <el-form-item label="基准经度"><el-input-number v-model.number="groupForm.longitude" :precision="6" :step="0.000001" :min="-180" :max="180" /></el-form-item>
                <el-form-item label="基准纬度"><el-input-number v-model.number="groupForm.latitude" :precision="6" :step="0.000001" :min="-90" :max="90" /></el-form-item>
                <el-form-item label=" ">
                  <span class="hint">分组基准坐标：组内无自身 GPS 的设备在电子地图上按此坐标显示</span>
                </el-form-item>
              </el-form>
              <div style="padding: 0 20px 20px; border-top: 1px solid var(--el-border-color)">
                <el-button type="primary" :loading="saving" @click="saveGroup">保存</el-button>
                <el-button type="danger" style="margin-left: 8px" @click="deleteGroup">删除</el-button>
              </div>
            </el-tab-pane>

            <el-tab-pane label="绑定设备" name="devices">
              <div style="padding: 16px">
                <el-button type="primary" size="small" @click="openBindDevices">绑定设备</el-button>
                <el-table :data="groupDevices" border style="margin-top: 12px">
                  <el-table-column prop="id" label="ID" width="60" />
                  <el-table-column prop="name" label="设备名" min-width="150" />
                  <el-table-column prop="protocol" label="协议" width="100" />
                  <el-table-column prop="ip" label="IP" width="130" />
                  <el-table-column prop="status" label="状态" width="100">
                    <template #default="{ row }">
                      <el-tag :type="row.status === 'online' ? 'success' : 'info'">{{ row.status }}</el-tag>
                    </template>
                  </el-table-column>
                  <el-table-column label="操作" width="100">
                    <template #default="{ row }">
                      <el-button link type="danger" size="small" @click="unbindDevice(row)">解绑</el-button>
                    </template>
                  </el-table-column>
                </el-table>
              </div>
            </el-tab-pane>

            <el-tab-pane label="绑定通道" name="channels">
              <div style="padding: 16px">
                <el-button type="primary" size="small" @click="openBindChannels">绑定通道</el-button>
                <el-table :data="groupChannels" border style="margin-top: 12px">
                  <el-table-column prop="id" label="ID" width="60" />
                  <el-table-column prop="name" label="通道名" min-width="150" />
                  <el-table-column prop="deviceId" label="设备ID" width="90" />
                  <el-table-column prop="streamType" label="码流" width="90" />
                  <el-table-column prop="online" label="在线" width="80">
                    <template #default="{ row }">
                      <el-tag :type="row.online ? 'success' : 'info'">{{ row.online ? '是' : '否' }}</el-tag>
                    </template>
                  </el-table-column>
                  <el-table-column label="操作" width="100">
                    <template #default="{ row }">
                      <el-button link type="danger" size="small" @click="unbindChannel(row)">解绑</el-button>
                    </template>
                  </el-table-column>
                </el-table>
              </div>
            </el-tab-pane>

            <el-tab-pane label="用户权限" name="userGroups">
              <div style="padding: 16px">
                <el-button type="primary" size="small" @click="openAssignUser">分配用户</el-button>
                <el-table :data="userGroups" border style="margin-top: 12px">
                  <el-table-column prop="user.username" label="用户" min-width="140" />
                  <el-table-column prop="user.nickname" label="昵称" min-width="140" />
                  <el-table-column prop="user.role" label="角色" width="100" />
                  <el-table-column label="分组权限" min-width="200">
                    <template #default="{ row }">
                      <el-tag v-for="p in splitPerms(row.permissions)" :key="p" size="small" style="margin: 2px">{{ permLabel(p) }}</el-tag>
                      <span v-if="!row.permissions">继承角色权限</span>
                    </template>
                  </el-table-column>
                  <el-table-column label="操作" width="160">
                    <template #default="{ row }">
                      <el-button link type="primary" size="small" @click="openAssignUser(row)">修改权限</el-button>
                      <el-button link type="danger" size="small" @click="removeUserGroup(row)">移除</el-button>
                    </template>
                  </el-table-column>
                </el-table>
              </div>
            </el-tab-pane>
          </el-tabs>
        </el-card>

        <el-card v-else class="empty-state">
          <span>请在左侧选择一个分组</span>
        </el-card>
      </el-col>
    </el-row>

    <el-dialog v-model="groupVisible" :title="editGroup ? '编辑分组' : '添加分组'" width="480px">
      <el-form :model="groupForm" label-width="90px">
        <el-form-item label="分组名"><el-input v-model="groupForm.name" /></el-form-item>
        <el-form-item label="描述"><el-input v-model="groupForm.description" type="textarea" /></el-form-item>
        <el-form-item label="父分组">
          <el-select v-model="groupForm.parentId" style="width: 100%" placeholder="无（根分组）">
            <el-option v-for="g in flatGroups" :key="g.id" :label="g.name" :value="g.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="排序"><el-input-number v-model.number="groupForm.sort" :min="0" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="groupVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveGroup">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="bindDevicesVisible" title="绑定设备" width="700px">
      <el-form :model="bindDevicesForm" label-width="90px" style="margin-bottom: 16px">
        <el-form-item label="分组"><el-input :value="selectedGroup?.name" disabled /></el-form-item>
        <el-form-item label="设备">
          <el-select v-model="bindDevicesForm.deviceIds" multiple placeholder="请选择设备" style="width: 100%">
            <el-option v-for="d in allDevices" :key="d.id" :label="d.name + ' (' + d.ip + ')'" :value="d.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="bindDevicesVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="doBindDevices">绑定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="bindChannelsVisible" title="绑定通道" width="700px">
      <el-form :model="bindChannelsForm" label-width="90px" style="margin-bottom: 16px">
        <el-form-item label="分组"><el-input :value="selectedGroup?.name" disabled /></el-form-item>
        <el-form-item label="通道">
          <el-select v-model="bindChannelsForm.channelIds" multiple placeholder="请选择通道" style="width: 100%">
            <el-option v-for="c in allChannels" :key="c.id" :label="c.name + ' (设备' + c.deviceId + ')'" :value="c.id" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="bindChannelsVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="doBindChannels">绑定</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="assignUserVisible" title="分配用户" width="560px">
      <el-form :model="assignUserForm" label-width="100px">
        <el-form-item label="分组"><el-input :value="selectedGroup?.name" disabled /></el-form-item>
        <el-form-item label="用户">
          <el-select v-model="assignUserForm.userId" placeholder="请选择用户" style="width: 100%">
            <el-option v-for="u in allUsers" :key="u.id" :label="u.username + ' (' + u.nickname + ')'" :value="u.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="分组权限">
          <el-checkbox-group v-model="assignUserForm.permList">
            <el-checkbox v-for="p in PERMS" :key="p.key" :label="p.key">{{ p.label }}</el-checkbox>
          </el-checkbox-group>
          <p class="hint">留空则继承用户角色权限</p>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="assignUserVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="doAssignUser">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, reactive, computed } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { groupApi, deviceApi, channelApi, userApi, roleApi } from '../api'
import type { DeviceGroup, Device, Channel, User, UserGroup, Role } from '../types'

const PERMS = [
  { key: 'device', label: '设备接入' },
  { key: 'video', label: '视频调阅' },
  { key: 'recording', label: '录像回看' },
  { key: 'snapshot', label: '快照图库' },
  { key: 'ai', label: 'AI 分析' },
  { key: 'event', label: '事件中心' },
  { key: 'search', label: '智能检索' },
  { key: 'notify', label: '告警通知' },
  { key: 'cluster', label: '集群' },
  { key: 'apikey', label: '开放 API' },
  { key: 'config', label: '平台配置' },
]

const groups = ref<DeviceGroup[]>([])
const flatGroups = ref<DeviceGroup[]>([])
const selectedGroup = ref<DeviceGroup | null>(null)
const groupDevices = ref<Device[]>([])
const groupChannels = ref<Channel[]>([])
const userGroups = ref<UserGroup[]>([])
const allDevices = ref<Device[]>([])
const allChannels = ref<Channel[]>([])
const allUsers = ref<User[]>([])
const allRoles = ref<Role[]>([])

const tab = ref('info')
const groupVisible = ref(false)
const bindDevicesVisible = ref(false)
const bindChannelsVisible = ref(false)
const assignUserVisible = ref(false)
const saving = ref(false)
const editGroup = ref<DeviceGroup | null>(null)

const groupForm = reactive<Partial<DeviceGroup>>({
  name: '', description: '', parentId: 0, sort: 0, longitude: 0, latitude: 0
})

const bindDevicesForm = reactive({ deviceIds: [] as number[] })
const bindChannelsForm = reactive({ channelIds: [] as number[] })
const assignUserForm = reactive({ userId: 0 as number, permList: [] as string[] })

const treeProps = {
  children: 'children',
  label: 'name',
  value: 'id',
}

function splitPerms(s: string) {
  if (!s) return []
  return s.split(',').map((x) => x.trim()).filter(Boolean)
}

function permLabel(key: string) {
  return PERMS.find((p) => p.key === key)?.label || key
}

function buildTree(list: DeviceGroup[]) {
  const map = new Map<number, DeviceGroup & { children?: (DeviceGroup & { children?: any[] })[] }>()
  list.forEach((g) => map.set(g.id, { ...g, children: [] }))
  const roots: (DeviceGroup & { children?: any[] })[] = []
  list.forEach((g) => {
    const node = map.get(g.id)!
    if (g.parentId === 0) {
      roots.push(node)
    } else {
      const parent = map.get(g.parentId)
      if (parent) parent.children!.push(node)
    }
  })
  return roots
}

async function load() {
  const [gs, dsRes, csRes, us, rs] = await Promise.all([
    groupApi.list(),
    deviceApi.list({ pageSize: 10000 }),
    channelApi.list({ pageSize: 10000 }),
    userApi.list(),
    roleApi.list(),
  ])
  groups.value = buildTree(gs)
  flatGroups.value = gs
  allDevices.value = (dsRes as any).items ?? dsRes
  allChannels.value = (csRes as any).items ?? csRes
  allUsers.value = us
  allRoles.value = rs
}

function onGroupClick(data: DeviceGroup) {
  selectedGroup.value = data
  editGroup.value = null
  loadGroupDetails(data.id)
}

function onTabChange(name: string) {
  tab.value = name
  if (selectedGroup.value) loadGroupDetails(selectedGroup.value.id)
}

async function loadGroupDetails(id: number) {
  const [devs, chs, ugs] = await Promise.all([
    groupApi.groupDevices(id),
    groupApi.groupChannels(id),
    groupApi.userGroups(),
  ])
  groupDevices.value = devs
  groupChannels.value = chs
  userGroups.value = ugs.filter((ug) => ug.groupId === id)
}

function openGroup(row?: DeviceGroup) {
  editGroup.value = row || null
  if (row) {
    Object.assign(groupForm, { name: row.name, description: row.description, parentId: row.parentId, sort: row.sort, longitude: row.longitude || 0, latitude: row.latitude || 0 })
  } else {
    Object.assign(groupForm, { name: '', description: '', parentId: selectedGroup.value?.id || 0, sort: 0, longitude: 0, latitude: 0 })
  }
  groupVisible.value = true
}

async function saveGroup() {
  if (!groupForm.name) {
    ElMessage.warning('请输入分组名')
    return
  }
  if (groupForm.parentId === (editGroup.value?.id || 0)) {
    ElMessage.warning('不能将分组设为自身的子分组')
    return
  }
  saving.value = true
  try {
    const body = { name: groupForm.name, description: groupForm.description, parentId: groupForm.parentId, sort: groupForm.sort, longitude: groupForm.longitude, latitude: groupForm.latitude }
    if (editGroup.value) await groupApi.update(editGroup.value.id, body)
    else await groupApi.create(body)
    ElMessage.success('已保存')
    groupVisible.value = false
    await load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function deleteGroup() {
  if (!selectedGroup.value) return
  await ElMessageBox.confirm(`删除分组「${selectedGroup.value.name}」？`, '确认', { type: 'warning' })
  try {
    await groupApi.remove(selectedGroup.value.id)
    ElMessage.success('已删除')
    selectedGroup.value = null
    await load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

function openBindDevices() {
  bindDevicesForm.deviceIds = []
  bindDevicesVisible.value = true
}

async function doBindDevices() {
  if (!selectedGroup.value || bindDevicesForm.deviceIds.length === 0) return
  saving.value = true
  try {
    await groupApi.bindDevices({ groupId: selectedGroup.value.id, deviceIds: bindDevicesForm.deviceIds })
    ElMessage.success('已绑定')
    bindDevicesVisible.value = false
    await loadGroupDetails(selectedGroup.value.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '绑定失败')
  } finally {
    saving.value = false
  }
}

async function unbindDevice(row: Device) {
  await ElMessageBox.confirm(`解绑设备「${row.name}」？`, '确认', { type: 'warning' })
  try {
    await groupApi.unbindDevices({ groupId: selectedGroup.value!.id, deviceIds: [row.id] })
    ElMessage.success('已解绑')
    await loadGroupDetails(selectedGroup.value!.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '解绑失败')
  }
}

function openBindChannels() {
  bindChannelsForm.channelIds = []
  bindChannelsVisible.value = true
}

async function doBindChannels() {
  if (!selectedGroup.value || bindChannelsForm.channelIds.length === 0) return
  saving.value = true
  try {
    await groupApi.bindChannels({ groupId: selectedGroup.value.id, channelIds: bindChannelsForm.channelIds })
    ElMessage.success('已绑定')
    bindChannelsVisible.value = false
    await loadGroupDetails(selectedGroup.value.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '绑定失败')
  } finally {
    saving.value = false
  }
}

async function unbindChannel(row: Channel) {
  await ElMessageBox.confirm(`解绑通道「${row.name}」？`, '确认', { type: 'warning' })
  try {
    await groupApi.unbindChannels({ groupId: selectedGroup.value!.id, channelIds: [row.id] })
    ElMessage.success('已解绑')
    await loadGroupDetails(selectedGroup.value!.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '解绑失败')
  }
}

function openAssignUser(row?: UserGroup) {
  if (row) {
    Object.assign(assignUserForm, { userId: row.userId, permList: splitPerms(row.permissions) })
  } else {
    Object.assign(assignUserForm, { userId: 0, permList: [] })
  }
  assignUserVisible.value = true
}

async function doAssignUser() {
  if (!selectedGroup.value || !assignUserForm.userId) {
    ElMessage.warning('请选择用户')
    return
  }
  saving.value = true
  try {
    await groupApi.assignUserGroup({
      userId: assignUserForm.userId,
      groupId: selectedGroup.value.id,
      permissions: assignUserForm.permList.join(','),
    })
    ElMessage.success('已保存')
    assignUserVisible.value = false
    await loadGroupDetails(selectedGroup.value.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function removeUserGroup(row: UserGroup) {
  await ElMessageBox.confirm(`移除用户「${row.user?.username}」的分组权限？`, '确认', { type: 'warning' })
  try {
    await groupApi.removeUserGroup(row.id)
    ElMessage.success('已移除')
    await loadGroupDetails(selectedGroup.value!.id)
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '移除失败')
  }
}

onMounted(load)
</script>

<style scoped>
.page-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.group-node {
  display: flex;
  align-items: center;
  gap: 6px;
}
.empty-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 300px;
  color: var(--el-text-color-placeholder);
}
.hint {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--el-text-color-placeholder);
}
</style>