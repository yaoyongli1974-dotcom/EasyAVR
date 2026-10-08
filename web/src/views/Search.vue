<template>
  <div class="page">
    <div class="page-header">
      <h2>智能检索</h2>
      <el-tag :type="semantic ? 'success' : 'info'">{{ semantic ? '向量语义检索' : '关键词检索（未配置嵌入模型）' }}</el-tag>
    </div>

    <el-card>
      <div style="display: flex; gap: 8px">
        <el-input v-model="query" placeholder="用自然语言检索 AI 事件，如：有人翻越围墙 / 火焰和烟雾" @keyup.enter="run" />
        <el-button type="primary" :loading="loading" @click="run">检索</el-button>
      </div>
      <p class="hint">
        语义检索需要配置一个 kind=embedding 的 AI 能力（OpenAI 兼容 /v1/embeddings）；未配置时自动退化为关键词匹配。
      </p>
    </el-card>

    <el-table :data="hits" border style="margin-top: 16px" v-loading="loading">
      <el-table-column label="相关度" width="100">
        <template #default="{ row }">
          <el-progress :percentage="Math.round((row.score || 0) * 100)" :show-text="false" style="width: 70px" />
        </template>
      </el-table-column>
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ format(row.event.occurredAt) }}</template>
      </el-table-column>
      <el-table-column prop="event.kind" label="能力" width="90" />
      <el-table-column prop="event.eventType" label="类型" width="150" />
      <el-table-column label="级别" width="100">
        <template #default="{ row }">
          <el-tag :type="row.event.level === 'critical' ? 'danger' : row.event.level === 'warning' ? 'warning' : 'info'">
            {{ row.event.level }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="event.summary" label="摘要" min-width="260" show-overflow-tooltip />
    </el-table>
    <el-empty v-if="searched && !hits.length" description="无匹配结果" />
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { searchApi } from '../api'
import type { SearchHit } from '../types'

const query = ref('')
const hits = ref<SearchHit[]>([])
const semantic = ref(false)
const loading = ref(false)
const searched = ref(false)

async function run() {
  if (!query.value) return
  loading.value = true
  try {
    const res = await searchApi.search(query.value, 20)
    hits.value = res.hits
    semantic.value = res.semantic
    searched.value = true
  } finally {
    loading.value = false
  }
}

function format(t: string) {
  return t ? new Date(t).toLocaleString() : ''
}
</script>

<style scoped>
.hint {
  color: #94a3b8;
  font-size: 12px;
  margin: 10px 0 0;
}
</style>
