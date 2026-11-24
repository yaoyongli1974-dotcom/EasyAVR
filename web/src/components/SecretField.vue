<template>
  <div class="secret-field">
    <div class="secret-field-head">
      <el-tag :type="configured ? 'success' : 'info'" size="small" effect="plain">
        {{ configured ? '已配置' : '未配置' }}
      </el-tag>
      <span class="secret-field-title">{{ editing ? '修改密钥' : '设置密钥' }}</span>
      <el-checkbox v-if="editing" :model-value="clear" class="secret-field-clear" @update:model-value="onClear">
        清除密钥
      </el-checkbox>
    </div>
    <el-input
      :model-value="modelValue || ''"
      type="password"
      show-password
      autocomplete="new-password"
      :placeholder="placeholder || (editing ? '留空表示保持原值，输入新值即修改' : '留空则不设置')"
      :disabled="clear"
      @update:model-value="onInput"
    />
    <div class="secret-field-hint">密钥仅在此处输入，保存后不再显示</div>
  </div>
</template>

<script setup lang="ts">
withDefaults(defineProps<{
  modelValue?: string
  clear?: boolean
  configured?: boolean
  editing?: boolean
  placeholder?: string
}>(), {
  modelValue: '',
  clear: false,
  configured: false,
  editing: false,
  placeholder: '',
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
  (e: 'update:clear', value: boolean): void
}>()

function onInput(value: string | number) {
  emit('update:modelValue', value == null ? '' : String(value))
}

function onClear(value: unknown) {
  const next = Boolean(value)
  emit('update:clear', next)
  if (next) emit('update:modelValue', '')
}
</script>

<style scoped>
.secret-field-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.secret-field-title {
  color: #475569;
  font-size: 12px;
}
.secret-field-clear {
  margin-left: auto;
}
.secret-field-hint {
  margin-top: 4px;
  color: #94a3b8;
  font-size: 12px;
  line-height: 18px;
}
</style>
