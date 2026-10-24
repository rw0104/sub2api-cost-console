<template>
  <label :class="labelClass">
    <span v-if="label">{{ label }}</span>
    <Select
      :model-value="modelValue"
      :options="selectOptions"
      :aria-label="ariaLabel || label"
      :disabled="disabled"
      :class="['time-range-select', selectClass]"
      @update:model-value="onSelect"
    />
  </label>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'

export interface TimeRangeOption {
  value: string
  label: string
  disabled?: boolean
}

const props = withDefaults(defineProps<{
  modelValue: string
  options: readonly TimeRangeOption[]
  label?: string
  ariaLabel?: string
  labelClass?: string
  selectClass?: string
  disabled?: boolean
}>(), {
  label: '',
  ariaLabel: '',
  labelClass: '',
  selectClass: '',
  disabled: false,
})

const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void
}>()

const selectOptions = computed<SelectOption[]>(() => props.options.map((option) => ({
  value: option.value,
  label: option.label,
  disabled: option.disabled,
})))

function onSelect(value: string | number | boolean | null) {
  if (typeof value === 'string') emit('update:modelValue', value)
}
</script>

<style scoped>
.time-range-select { min-width: 0; }
</style>
