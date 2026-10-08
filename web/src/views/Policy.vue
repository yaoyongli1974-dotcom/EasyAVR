<template>
  <div class="page">
    <div class="page-header">
      <h2>权限策略</h2>
      <div>
        <el-button @click="load">刷新</el-button>
        <el-button type="warning" @click="reseed">重置默认策略</el-button>
        <el-button type="primary" @click="openAdd">添加策略</el-button>
      </div>
    </div>

    <el-card>
      <template #header><span>策略列表（{{ items.length }}）</span></template>
      <el-table :data="items" border>
        <el-table-column label="类型" width="100">
          <template #default="{ row }">
            <el-tag :type="row.pType === 'p' ? 'primary' : 'success'">{{ row.pType === 'p' ? '策略 P' : '分组 G' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="主体 / 用户" min-width="160">
          <template #default="{ row }">{{ row.params[0] }}</template>
        </el-table-column>
        <el-table-column label="资源 / 角色" min-width="160">
          <template #default="{ row }">{{ row.params[1] }}</template>
        </el-table-column>
        <el-table-column label="动作 / 域" min-width="140">
          <template #default="{ row }">{{ row.params[2] }}</template>
        </el-table-column>
        <el-table-column label="域" min-width="120">
          <template #default="{ row }">{{ row.params[3] || '*' }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100">
          <template #default="{ row }">
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card style="margin-top: 16px">
      <template #header><span>权限测试器</span></template>
      <el-form :inline="true" :model="testForm">
        <el-form-item label="用户ID"><el-input-number v-model="testForm.userId" :min="0" /></el-form-item>
        <el-form-item label="登录名"><el-input v-model="testForm.username" style="width: 120px" /></el-form-item>
        <el-form-item label="角色"><el-input v-model="testForm.role" style="width: 120px" /></el-form-item>
        <el-form-item label="资源"><el-input v-model="testForm.resource" style="width: 140px" /></el-form-item>
        <el-form-item label="动作"><el-input v-model="testForm.action" style="width: 120px" /></el-form-item>
        <el-form-item label="域"><el-input v-model="testForm.domain" style="width: 100px" /></el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="testing" @click="runTest">测试</el-button>
        </el-form-item>
      </el-form>
      <el-alert v-if="testResult !== null" :type="testResult ? 'success' : 'error'" :closable="false" show-icon
        :title="testResult ? '允许访问' : '拒绝访问'" />
    </el-card>

    <el-dialog v-model="addVisible" title="添加策略" width="520px">
      <el-form label-width="90px">
        <el-form-item label="类型">
          <el-radio-group v-model="addForm.pType">
            <el-radio label="p">策略 P</el-radio>
            <el-radio label="g">分组 G</el-radio>
          </el-radio-group>
        </el-form-item>
        <template v-if="addForm.pType === 'p'">
          <el-form-item label="主体">
            <el-input v-model="addForm.sub" placeholder="如 role:operator 或 user:1" />
          </el-form-item>
          <el-form-item label="资源">
            <el-input v-model="addForm.obj" placeholder="如 ai:task 或 *" />
          </el-form-item>
          <el-form-item label="动作">
            <el-input v-model="addForm.act" placeholder="如 read / * " />
          </el-form-item>
          <el-form-item label="域">
            <el-input v-model="addForm.dom" placeholder="留空表示 *" />
          </el-form-item>
        </template>
        <template v-else>
          <el-form-item label="用户">
            <el-input v-model="addForm.sub" placeholder="如 user:1" />
          </el-form-item>
          <el-form-item label="角色">
            <el-input v-model="addForm.obj" placeholder="如 role:operator" />
          </el-form-item>
          <el-form-item label="域">
            <el-input v-model="addForm.act" placeholder="留空表示 *" />
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="addVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { policyApi } from '../api'
import type { PolicyRule } from '../types'

const items = ref<PolicyRule[]>([])
const addVisible = ref(false)
const saving = ref(false)
const testing = ref(false)
const testResult = ref<boolean | null>(null)

const addForm = reactive({ pType: 'p' as 'p' | 'g', sub: '', obj: '', act: '', dom: '' })
const testForm = reactive({ userId: 1, username: '', role: '', resource: '', action: '', domain: '' })

async function load() {
  items.value = await policyApi.list()
}

function openAdd() {
  Object.assign(addForm, { pType: 'p', sub: '', obj: '', act: '', dom: '' })
  addVisible.value = true
}

async function save() {
  const params = addForm.pType === 'p'
    ? [addForm.sub, addForm.obj, addForm.act || '*', addForm.dom || '*']
    : [addForm.sub, addForm.obj, addForm.act || '*']
  if (!params[0] || !params[1]) {
    ElMessage.warning('请填写主体与资源')
    return
  }
  saving.value = true
  try {
    await policyApi.add({ pType: addForm.pType, params })
    ElMessage.success('已添加')
    addVisible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '添加失败')
  } finally {
    saving.value = false
  }
}

async function remove(row: PolicyRule) {
  await ElMessageBox.confirm(`删除策略「${row.params.join(' , ')}」？`, '确认', { type: 'warning' })
  try {
    await policyApi.remove({ pType: row.pType, params: row.params })
    ElMessage.success('已删除')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '删除失败')
  }
}

async function runTest() {
  testing.value = true
  testResult.value = null
  try {
    const r = await policyApi.test({ ...testForm })
    testResult.value = r.allowed
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '测试失败')
  } finally {
    testing.value = false
  }
}

async function reseed() {
  await ElMessageBox.confirm('将清空后重建内置角色默认策略，自定义策略会丢失，继续？', '确认', { type: 'warning' })
  try {
    await policyApi.seed()
    ElMessage.success('已重置')
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '重置失败')
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
