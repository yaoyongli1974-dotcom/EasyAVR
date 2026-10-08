<template>
  <div class="page">
    <div class="page-header">
      <h2>用户与角色</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-tabs v-model="tab" @tab-change="load">
      <el-tab-pane label="用户管理" name="users">
        <el-card>
          <template #header>
            <span>用户列表</span>
            <div style="float: right">
              <el-button size="small" @click="exportUsers">导出 CSV</el-button>
              <el-button size="small" @click="userFileInput?.click()">导入 CSV</el-button>
              <el-button type="primary" size="small" @click="openUser()">添加用户</el-button>
            </div>
            <input ref="userFileInput" type="file" accept=".csv,text/csv" style="display: none" @change="importUsers" />
          </template>
          <el-table :data="users" border>
            <el-table-column prop="id" label="ID" width="60" />
            <el-table-column prop="username" label="登录名" min-width="140" />
            <el-table-column prop="nickname" label="昵称" min-width="140" />
            <el-table-column label="角色" width="140">
              <template #default="{ row }">
                <el-tag>{{ row.role }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="状态" width="90">
              <template #default="{ row }">
                <el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="180">
              <template #default="{ row }">
                <el-button link type="primary" @click="openUser(row)">编辑</el-button>
                <el-button link type="danger" @click="removeUser(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>

      <el-tab-pane label="角色管理" name="roles">
        <el-card>
          <template #header>
            <span>角色列表</span>
            <el-button type="primary" size="small" style="float: right" @click="openRole()">添加角色</el-button>
          </template>
          <el-table :data="roles" border>
            <el-table-column prop="name" label="角色" width="150" />
            <el-table-column prop="description" label="描述" min-width="220" />
            <el-table-column label="权限" min-width="260">
              <template #default="{ row }">
                <el-tag v-for="p in splitPerms(row.permissions)" :key="p" size="small" style="margin: 2px">{{ permLabel(p) }}</el-tag>
                <span v-if="!row.permissions">-</span>
              </template>
            </el-table-column>
            <el-table-column label="类型" width="90">
              <template #default="{ row }">
                <el-tag :type="row.builtin ? 'warning' : 'info'">{{ row.builtin ? '内置' : '自定义' }}</el-tag>
              </template>
            </el-table-column>
            <el-table-column label="操作" width="180">
              <template #default="{ row }">
                <el-button link type="primary" @click="openRole(row)">编辑</el-button>
                <el-button link type="danger" :disabled="row.builtin" @click="removeRole(row)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
        </el-card>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="userVisible" :title="editUser ? '编辑用户' : '添加用户'" width="480px">
      <el-form :model="userForm" label-width="90px">
        <el-form-item label="登录名">
          <el-input v-model="userForm.username" :disabled="!!editUser" />
        </el-form-item>
        <el-form-item label="昵称"><el-input v-model="userForm.nickname" /></el-form-item>
        <el-form-item :label="editUser ? '重置密码' : '密码'">
          <el-input v-model="userForm.password" type="password" show-password :placeholder="editUser ? '留空则不修改' : ''" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="userForm.role" style="width: 100%">
            <el-option v-for="r in roles" :key="r.id" :label="r.name" :value="r.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用"><el-switch v-model="userForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="userVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveUser">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="roleVisible" :title="editRole ? '编辑角色' : '添加角色'" width="520px">
      <el-form :model="roleForm" label-width="90px">
        <el-form-item label="角色名">
          <el-input v-model="roleForm.name" :disabled="!!editRole?.builtin" />
        </el-form-item>
        <el-form-item label="描述"><el-input v-model="roleForm.description" /></el-form-item>
        <el-form-item label="权限">
          <el-checkbox-group v-model="roleForm.permList">
            <el-checkbox v-for="p in PERMS" :key="p.key" :label="p.key">{{ p.label }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="roleVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveRole">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { roleApi, userApi } from '../api'
import type { Role, User } from '../types'

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
  { key: '*', label: '全部权限' },
]

const tab = ref('users')
const users = ref<User[]>([])
const roles = ref<Role[]>([])
const userVisible = ref(false)
const roleVisible = ref(false)
const editUser = ref<User | null>(null)
const editRole = ref<Role | null>(null)
const saving = ref(false)
const userForm = reactive({ username: '', nickname: '', password: '', role: 'viewer', enabled: true })
const roleForm = reactive<{ name: string; description: string; permList: string[] }>({ name: '', description: '', permList: [] })
const userFileInput = ref<HTMLInputElement>()

async function exportUsers() {
  try {
    const blob = await userApi.exportCSV()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'users.csv'
    a.click()
    URL.revokeObjectURL(url)
  } catch {
    ElMessage.error('导出失败')
  }
}

async function importUsers(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const csv = await file.text()
  try {
    const r = await userApi.importCSV(csv)
    ElMessage.success(`导入完成：新增 ${r.created}，跳过 ${r.skipped}`)
    load()
  } catch (err: any) {
    ElMessage.error(err?.response?.data?.message || '导入失败')
  } finally {
    input.value = ''
  }
}

function splitPerms(s: string) {
  if (!s || s === '*') return s === '*' ? ['*'] : []
  return s.split(',').map((x) => x.trim()).filter(Boolean)
}
function permLabel(key: string) {
  return PERMS.find((p) => p.key === key)?.label || key
}

async function load() {
  roles.value = await roleApi.list()
  if (tab.value === 'users') users.value = await userApi.list()
}

function openUser(row?: User) {
  editUser.value = row || null
  Object.assign(userForm, row
    ? { username: row.username, nickname: row.nickname, password: '', role: row.role, enabled: row.enabled }
    : { username: '', nickname: '', password: '', role: 'viewer', enabled: true })
  userVisible.value = true
}

async function saveUser() {
  saving.value = true
  try {
    if (editUser.value) {
      const body: any = { nickname: userForm.nickname, role: userForm.role, enabled: userForm.enabled }
      if (userForm.password) body.password = userForm.password
      await userApi.update(editUser.value.id, body)
    } else {
      if (!userForm.username || !userForm.password) {
        ElMessage.warning('请填写登录名与密码')
        return
      }
      await userApi.create({ ...userForm })
    }
    ElMessage.success('已保存')
    userVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function removeUser(row: User) {
  await ElMessageBox.confirm(`删除用户「${row.username}」？`, '确认', { type: 'warning' })
  try {
    await userApi.remove(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

function openRole(row?: Role) {
  editRole.value = row || null
  if (row) Object.assign(roleForm, { name: row.name, description: row.description, permList: splitPerms(row.permissions) })
  else Object.assign(roleForm, { name: '', description: '', permList: [] })
  roleVisible.value = true
}

async function saveRole() {
  if (!roleForm.name) {
    ElMessage.warning('请输入角色名')
    return
  }
  saving.value = true
  try {
    const body = { name: roleForm.name, description: roleForm.description, permissions: roleForm.permList.join(',') }
    if (editRole.value) await roleApi.update(editRole.value.id, body)
    else await roleApi.create(body)
    ElMessage.success('已保存')
    roleVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function removeRole(row: Role) {
  await ElMessageBox.confirm(`删除角色「${row.name}」？`, '确认', { type: 'warning' })
  try {
    await roleApi.remove(row.id)
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败（该角色下可能仍有用户）')
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
</style>
