<template>
  <div class="page">
    <div class="page-header">
      <h2>存储管理</h2>
      <div>
        <el-button @click="load">刷新</el-button>
        <el-button type="primary" @click="open()">新增存储</el-button>
      </div>
    </div>

    <el-card v-loading="loading">
      <el-table :data="stores" border>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="name" label="名称" min-width="130" show-overflow-tooltip />
        <el-table-column label="类型" width="80">
          <template #default="{ row }">
            <el-tag size="small" type="info">{{ row.type || 's3' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="endpoint" label="Endpoint" min-width="180" show-overflow-tooltip />
        <el-table-column prop="bucket" label="存储桶" min-width="120" show-overflow-tooltip />
        <el-table-column label="区域" width="100">
          <template #default="{ row }">{{ row.region || '-' }}</template>
        </el-table-column>
        <el-table-column label="路径风格" width="90">
          <template #default="{ row }">
            <span>{{ row.pathStyle ? 'path-style' : 'virtual-host' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="AccessKey" width="100">
          <template #default="{ row }">
            <el-tag :type="row.hasAccessKey ? 'success' : 'info'" size="small" effect="plain">
              {{ row.hasAccessKey ? '已配置' : '未配置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="SecretKey" width="100">
          <template #default="{ row }">
            <el-tag :type="row.hasSecret ? 'success' : 'info'" size="small" effect="plain">
              {{ row.hasSecret ? '已配置' : '未配置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="210" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" :loading="testing[row.id]" @click="test(row)">连接测试</el-button>
            <el-button link type="primary" @click="open(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>暂无云存储，点击「新增存储」接入 S3 兼容对象存储</template>
      </el-table>
    </el-card>

    <el-dialog v-model="visible" :title="editing ? '编辑存储' : '新增存储'" width="560px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="名称"><el-input v-model="form.name" placeholder="如：主站 MinIO" /></el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.type" style="width: 100%" disabled>
            <el-option label="S3 兼容（MinIO / OSS / COS / S3）" value="s3" />
          </el-select>
        </el-form-item>
        <el-form-item label="Endpoint">
          <el-input v-model="form.endpoint" placeholder="http://127.0.0.1:9000" />
        </el-form-item>
        <el-form-item label="存储桶"><el-input v-model="form.bucket" placeholder="easyavr-archive" /></el-form-item>
        <el-form-item :label="editing ? '修改 AccessKey' : 'AccessKey'">
          <SecretField
            v-model="form.accessKey"
            v-model:clear="form.clearAccessKey"
            :configured="!!editing?.hasAccessKey"
            :editing="!!editing"
            placeholder="请输入 AccessKey"
          />
        </el-form-item>
        <el-form-item :label="editing ? '修改 SecretKey' : 'SecretKey'">
          <SecretField
            v-model="form.secretKey"
            v-model:clear="form.clearSecretKey"
            :configured="!!editing?.hasSecret"
            :editing="!!editing"
            placeholder="请输入 SecretKey"
          />
        </el-form-item>
        <el-form-item label="区域">
          <el-input v-model="form.region" placeholder="如 us-east-1（可留空）" />
        </el-form-item>
        <el-form-item label="路径风格">
          <el-switch v-model="form.pathStyle" />
          <div class="form-hint">Path-style 面向前缀式地址，多数自建对象存储（MinIO 等）需要开启</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { storageApi } from '../api'
import SecretField from '../components/SecretField.vue'
import type { CloudStore } from '../types'

const stores = ref<CloudStore[]>([])
const loading = ref(false)
const testing = reactive<Record<number, boolean>>({})

const visible = ref(false)
const saving = ref(false)
const editing = ref<CloudStore | null>(null)
const form = reactive<Partial<CloudStore> & {
  accessKey?: string
  secretKey?: string
  clearAccessKey?: boolean
  clearSecretKey?: boolean
}>({})

async function load() {
  loading.value = true
  try {
    stores.value = await storageApi.list()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载失败')
  } finally {
    loading.value = false
  }
}

function open(row?: CloudStore) {
  editing.value = row || null
  if (row) {
    Object.assign(form, {
      name: row.name, type: row.type, endpoint: row.endpoint, bucket: row.bucket,
      region: row.region, pathStyle: row.pathStyle, enabled: row.enabled,
      accessKey: '', clearAccessKey: false, secretKey: '', clearSecretKey: false,
    })
  } else {
    Object.assign(form, {
      name: '', type: 's3', endpoint: '', bucket: '', region: '', pathStyle: true, enabled: true,
      accessKey: '', clearAccessKey: false, secretKey: '', clearSecretKey: false,
    })
  }
  visible.value = true
}

async function save() {
  if (!form.name || !form.endpoint || !form.bucket) {
    ElMessage.warning('请填写名称、Endpoint 和存储桶')
    return
  }
  if (!editing.value && (!form.accessKey || !form.secretKey)) {
    ElMessage.warning('请填写 AccessKey 与 SecretKey')
    return
  }
  saving.value = true
  try {
    const body = {
      name: form.name, type: form.type, endpoint: form.endpoint, bucket: form.bucket,
      accessKey: form.clearAccessKey ? '' : (form.accessKey || ''),
      secretKey: form.clearSecretKey ? '' : (form.secretKey || ''),
      clearAccessKey: !!form.clearAccessKey,
      clearSecretKey: !!form.clearSecretKey,
      region: form.region, pathStyle: form.pathStyle, enabled: form.enabled,
    }
    if (editing.value) await storageApi.update(editing.value.id, body)
    else await storageApi.create(body)
    ElMessage.success('已保存')
    visible.value = false
    load()
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function test(row: CloudStore) {
  testing[row.id] = true
  try {
    await storageApi.test(row.id)
    ElMessage.success('连接成功，存储桶已就绪')
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '连接失败')
  } finally {
    testing[row.id] = false
  }
}

async function remove(row: CloudStore) {
  await ElMessageBox.confirm(`删除存储「${row.name}」？已归档的记录仍会保留。`, '确认', { type: 'warning' })
  await storageApi.remove(row.id)
  ElMessage.success('已删除')
  load()
}

onMounted(load)
</script>

<style scoped>
.form-hint {
  font-size: 12px;
  color: #94a3b8;
  line-height: 18px;
}
</style>
