<template>
  <div class="page">
    <div class="page-header">
      <h2>归档列表</h2>
      <div>
        <el-select
          v-model="status"
          style="width: 140px; margin-right: 8px"
          @change="() => { page = 1; load() }"
        >
          <el-option label="全部状态" value="" />
          <el-option label="已上传" value="uploaded" />
          <el-option label="失败" value="failed" />
        </el-select>
        <el-button @click="load">刷新</el-button>
      </div>
    </div>

    <el-card v-loading="loading">
      <el-table :data="rows" border>
        <el-table-column prop="id" label="ID" width="60" />
        <el-table-column prop="streamKey" label="流标识" min-width="160" show-overflow-tooltip />
        <el-table-column prop="bucket" label="存储桶" min-width="120" show-overflow-tooltip />
        <el-table-column prop="object" label="对象" min-width="240" show-overflow-tooltip />
        <el-table-column label="大小" width="110">
          <template #default="{ row }">{{ formatSize(row.size) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tooltip :content="row.error" placement="top" :disabled="!row.error">
              <el-tag :type="row.status === 'uploaded' ? 'success' : 'danger'" size="small">
                {{ row.status === 'uploaded' ? '已上传' : '失败' }}
              </el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="归档时间" width="180">
          <template #default="{ row }">{{ format(row.createdAt) }}</template>
        </el-table-column>
        <template #empty>暂无归档记录，可在「录像回看」中将录像归档到云</template>
      </el-table>
      <div class="pager" v-if="total > pageSize">
        <el-pagination
          layout="prev, pager, next, total"
          :total="total"
          :page-size="pageSize"
          v-model:current-page="page"
          @current-change="load"
        />
      </div>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { archiveApi } from '../api'
import type { RecordingArchive } from '../types'

const rows = ref<RecordingArchive[]>([])
const status = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await archiveApi.list({ status: status.value || undefined, page: page.value, pageSize: pageSize.value })
    rows.value = res.items || []
    total.value = res.total
    pageSize.value = res.pageSize || pageSize.value
  } catch (e: any) {
    ElMessage.error(e?.response?.data?.message || '加载失败')
  } finally {
    loading.value = false
  }
}

function formatSize(n?: number) {
  if (n === undefined || n === null) return '-'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i ? 1 : 0)} ${units[i]}`
}

function format(t?: string) {
  return t ? new Date(t).toLocaleString() : '-'
}

onMounted(load)
</script>

<style scoped>
.pager {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}
</style>
