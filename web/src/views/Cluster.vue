<template>
  <div class="page">
    <div class="page-header">
      <h2>集群</h2>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-card style="margin-bottom: 16px">
      <template #header>本节点配置</template>
      <el-descriptions :column="2" border>
        <el-descriptions-item label="集群开关">
          <el-tag :type="config.enabled ? 'success' : 'info'">{{ config.enabled ? '已启用' : '未启用' }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="节点 ID">{{ config.nodeId }}</el-descriptions-item>
        <el-descriptions-item label="节点名称">{{ config.name }}</el-descriptions-item>
        <el-descriptions-item label="对外 API 地址">{{ config.apiBase || '（未设置）' }}</el-descriptions-item>
        <el-descriptions-item label="数据库">{{ config.database }}</el-descriptions-item>
        <el-descriptions-item label="已知节点">{{ nodes.length }}</el-descriptions-item>
      </el-descriptions>
      <el-alert
        type="info"
        :closable="false"
        style="margin-top: 12px"
        title="集群通过共享数据库 + 节点心跳协作；设备创建设置 nodeId 归属。经环境变量 EASYAVR_CLUSTER_* 配置。"
      />
    </el-card>

    <el-table :data="nodes" border>
      <el-table-column prop="nodeId" label="节点 ID" min-width="140" />
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="apiBase" label="API" min-width="180" />
      <el-table-column label="本机" width="80">
        <template #default="{ row }">
          <el-tag v-if="row.isSelf" type="primary">本机</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="devices" label="设备数" width="100" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'online' ? 'success' : 'danger'">{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="最后心跳" width="200">
        <template #default="{ row }">{{ format(row.lastHeartbeat) }}</template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { clusterApi } from '../api'
import type { ClusterNode } from '../types'

const config = ref<Record<string, any>>({})
const nodes = ref<Array<ClusterNode & { devices: number }>>([])

async function load() {
  const [c, n] = await Promise.all([clusterApi.config(), clusterApi.stats()])
  config.value = c
  nodes.value = n
}
function format(t: string) {
  return t && t !== '0001-01-01T00:00:00Z' ? new Date(t).toLocaleString() : '-'
}

onMounted(load)
</script>
