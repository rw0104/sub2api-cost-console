<template>
  <section class="mt-4 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="official-models-panel">
    <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.officialModels.title') }}</p>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.officialModels.hint') }}</p>
    <div class="mt-3 flex flex-wrap gap-2">
      <button
        type="button"
        data-testid="verify-official-models"
        class="btn btn-secondary text-sm"
        :disabled="loading || disabled || !accountId"
        @click="verify"
      >
        {{ loading ? t('admin.accounts.officialModels.loading') : t('admin.accounts.officialModels.fetch') }}
      </button>
      <button
        v-if="result"
        type="button"
        data-testid="add-official-models"
        class="btn btn-primary text-sm"
        :disabled="disabled || additions.length === 0"
        @click="addConfirmedModels"
      >
        {{ t('admin.accounts.officialModels.addConfirmed', { count: additions.length }) }}
      </button>
    </div>
    <p v-if="error" role="alert" class="mt-2 break-words text-xs text-red-600 dark:text-red-400">{{ error }}</p>
    <div v-if="result" class="mt-3 space-y-3 text-xs" aria-live="polite">
      <div class="space-y-1 text-gray-500 dark:text-gray-400">
        <p class="font-medium text-emerald-700 dark:text-emerald-400">{{ t('admin.accounts.officialModels.fetched', { count: result.models.length }) }}</p>
        <p class="break-all">{{ result.source }}</p>
        <p>{{ new Date(result.fetched_at).toLocaleString() }}</p>
      </div>
      <div v-if="unavailableChecks.length" class="rounded bg-amber-50 p-2 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">
        <p class="font-medium">{{ t('admin.accounts.officialModels.unlistedTargets') }}</p>
        <ul class="mt-1 space-y-1">
          <li v-for="check in unavailableChecks" :key="check.from" class="break-all">{{ check.from }} → {{ check.to }}</li>
        </ul>
      </div>
      <p v-else class="text-emerald-700 dark:text-emerald-400">{{ t('admin.accounts.officialModels.targetsListed', { count: result.checks.length }) }}</p>
      <p v-if="result.missing_models.length" class="break-words text-amber-700 dark:text-amber-300">
        {{ t('admin.accounts.officialModels.missingRecommended') }} {{ result.missing_models.join(', ') }}
      </p>
      <details>
        <summary class="cursor-pointer text-gray-700 dark:text-gray-300">{{ t('admin.accounts.officialModels.catalog') }}</summary>
        <ul class="mt-2 max-h-48 space-y-1 overflow-auto text-gray-600 dark:text-gray-400">
          <li v-for="model in result.models" :key="model" class="break-all">
            <code>{{ model }}</code>
            <span v-if="result.metadata[model]?.display_name"> · {{ result.metadata[model].display_name }}</span>
          </li>
        </ul>
      </details>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountsAPI, type VerifyOfficialModelsResult } from '@/api/admin/accounts'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{
  accountId: number
  modelMapping: Record<string, string>
  mode: 'whitelist' | 'mapping'
  disabled?: boolean
}>()
const emit = defineEmits<{ 'add-models': [models: string[]] }>()
const { t } = useI18n()
const loading = ref(false)
const error = ref('')
const result = ref<VerifyOfficialModelsResult | null>(null)
let generation = 0

const contextKey = computed(() => JSON.stringify([
  props.accountId,
  props.mode,
  Object.entries(props.modelMapping).sort(([left], [right]) => left.localeCompare(right))
]))

watch(contextKey, () => {
  generation++
  loading.value = false
  result.value = null
  error.value = ''
}, { flush: 'sync' })

onBeforeUnmount(() => { generation++ })

const unavailableChecks = computed(() => result.value?.checks.filter(check => !check.available) ?? [])
const additions = computed(() => {
  if (!result.value) return []
  const suggested = result.value.suggested_model_mapping
  const existing = Object.keys(props.modelMapping)
  return [...new Set(result.value.models)].filter(model =>
    suggested[model] === model &&
    !existing.some(from => from === model || (from.endsWith('*') && model.startsWith(from.slice(0, -1))))
  )
})

async function verify() {
  if (loading.value || props.disabled || !props.accountId) return
  const requestGeneration = ++generation
  const requestedContext = contextKey.value
  loading.value = true
  result.value = null
  error.value = ''
  try {
    const fetched = await accountsAPI.verifyOfficialModels(props.accountId, { model_mapping: { ...props.modelMapping } })
    if (requestGeneration === generation && requestedContext === contextKey.value) result.value = fetched
  } catch (cause) {
    if (requestGeneration === generation && requestedContext === contextKey.value) {
      error.value = t('admin.accounts.officialModels.failed', { message: extractApiErrorMessage(cause) })
    }
  } finally {
    if (requestGeneration === generation) loading.value = false
  }
}

function addConfirmedModels() {
  if (props.disabled || !result.value || additions.value.length === 0) return
  const models = [...additions.value]
  generation++
  result.value = null
  emit('add-models', models)
}
</script>
