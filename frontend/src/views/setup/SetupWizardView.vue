<template>
  <div class="flex min-h-screen items-center justify-center bg-gradient-to-br from-gray-50 to-gray-100 p-4 dark:from-dark-900 dark:to-dark-800">
    <div class="w-full max-w-3xl">
      <div class="mb-8 text-center">
        <div class="mb-4 inline-flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-primary-500 to-primary-600 shadow-lg">
          <Icon name="cog" size="xl" class="text-white" />
        </div>
        <h1 class="text-3xl font-bold text-gray-900 dark:text-white">{{ t('setup.title') }}</h1>
        <p class="mt-2 text-gray-500 dark:text-dark-400">{{ t('setup.description') }}</p>
      </div>

      <div class="mb-8 overflow-x-auto pb-1">
        <div class="flex min-w-max items-center justify-center">
          <template v-for="(step, index) in steps" :key="step.id">
            <div class="flex items-center">
              <div
                :class="[
                  'flex h-10 w-10 items-center justify-center rounded-full text-sm font-semibold transition-colors',
                  currentStep > index
                    ? 'bg-primary-500 text-white'
                    : currentStep === index
                      ? 'bg-primary-500 text-white ring-4 ring-primary-100 dark:ring-primary-900'
                      : 'bg-gray-200 text-gray-500 dark:bg-dark-700 dark:text-dark-400'
                ]"
              >
                <Icon v-if="currentStep > index" name="check" size="md" :stroke-width="2" />
                <span v-else>{{ index + 1 }}</span>
              </div>
              <span
                class="ml-2 hidden text-sm font-medium md:inline"
                :class="currentStep >= index ? 'text-gray-900 dark:text-white' : 'text-gray-400 dark:text-dark-500'"
              >
                {{ step.title }}
              </span>
            </div>
            <div
              v-if="index < steps.length - 1"
              class="mx-2 h-0.5 w-6 lg:mx-3 lg:w-10"
              :class="currentStep > index ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-700'"
            ></div>
          </template>
        </div>
      </div>

      <div class="rounded-2xl bg-white p-6 shadow-xl dark:bg-dark-800 sm:p-8">
        <section v-if="currentStep === 0" aria-labelledby="setup-mode-title">
          <div class="mb-6 text-center">
            <h2 id="setup-mode-title" class="text-xl font-semibold text-gray-900 dark:text-white">
              {{ t('setup.mode.title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">
              {{ t('setup.mode.description') }}
            </p>
          </div>

          <div class="mb-5 rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900/40" aria-live="polite">
            <div class="flex items-center justify-between gap-3">
              <p class="text-sm font-semibold text-gray-800 dark:text-gray-100">
                {{ environmentChecking ? t('setup.mode.checking') : environment?.docker.message }}
              </p>
              <button
                type="button"
                class="btn btn-secondary min-h-9 flex-none px-3 text-xs"
                :disabled="environmentChecking || provisioning"
                @click="refreshEnvironment"
              >
                {{ t('setup.mode.checkAgain') }}
              </button>
            </div>

            <div v-if="environment" class="mt-4 grid gap-2 sm:grid-cols-3">
              <EnvironmentFact
                :label="t('setup.mode.docker')"
                :available="environment.docker.running"
                :value="environment.docker.installed
                  ? (environment.docker.running ? t('setup.mode.running') : t('setup.mode.notRunning'))
                  : t('setup.mode.notDetected')"
              />
              <EnvironmentFact
                :label="t('setup.mode.localPostgres')"
                :available="environment.postgres.reachable"
                :value="environment.postgres.reachable ? t('setup.mode.detected') : t('setup.mode.notDetected')"
              />
              <EnvironmentFact
                :label="t('setup.mode.localRedis')"
                :available="environment.redis.reachable"
                :value="environment.redis.reachable ? t('setup.mode.detected') : t('setup.mode.notDetected')"
              />
            </div>
          </div>

          <div v-if="hasExistingServices" class="mb-5 rounded-xl border border-blue-200 bg-blue-50 p-4 text-sm text-blue-800 dark:border-blue-800/60 dark:bg-blue-900/20 dark:text-blue-300">
            {{ t('setup.mode.existingServices') }}
          </div>

          <div class="grid gap-4 md:grid-cols-2">
            <article
              class="relative flex min-h-[300px] flex-col rounded-2xl border p-5"
              :class="recommendedMode === 'quick'
                ? 'border-primary-400 bg-primary-50/50 ring-2 ring-primary-100 dark:border-primary-600 dark:bg-primary-900/10 dark:ring-primary-900/40'
                : 'border-gray-200 dark:border-dark-700'"
            >
              <div class="flex items-start justify-between gap-3">
                <div>
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('setup.mode.quickTitle') }}</h3>
                    <span class="rounded-full bg-primary-100 px-2 py-0.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/50 dark:text-primary-300">
                      {{ t('setup.mode.quickBadge') }}
                    </span>
                  </div>
                  <p v-if="recommendedMode === 'quick'" class="mt-1 text-xs font-medium text-primary-700 dark:text-primary-300">
                    {{ t('setup.mode.recommended') }}
                  </p>
                </div>
                <Icon name="database" size="lg" class="text-primary-500" />
              </div>

              <p class="mt-4 text-sm leading-6 text-gray-600 dark:text-dark-300">
                {{ t('setup.mode.quickDescription') }}
              </p>
              <ul class="mt-3 space-y-2 text-sm text-gray-500 dark:text-dark-400">
                <li class="flex gap-2"><span class="text-primary-500">✓</span><span>PostgreSQL {{ quickImages.postgres }}</span></li>
                <li class="flex gap-2"><span class="text-primary-500">✓</span><span>Valkey {{ quickImages.valkey }}</span></li>
                <li class="flex gap-2"><span class="text-primary-500">✓</span><span>{{ t('setup.mode.quickNoOverwrite') }}</span></li>
              </ul>

              <div v-if="quickBlockerMessage" class="mt-4 rounded-lg bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300" role="status">
                {{ quickBlockerMessage }}
              </div>

              <button
                type="button"
                class="btn btn-primary mt-auto w-full"
                :disabled="environmentChecking || provisioning || Boolean(quickBlocker)"
                @click="startQuickSetup"
              >
                {{ provisioning ? provisionProgress.message : t('setup.mode.quickAction') }}
              </button>
            </article>

            <article
              class="flex min-h-[300px] flex-col rounded-2xl border p-5"
              :class="recommendedMode === 'advanced'
                ? 'border-primary-400 bg-primary-50/50 ring-2 ring-primary-100 dark:border-primary-600 dark:bg-primary-900/10 dark:ring-primary-900/40'
                : 'border-gray-200 dark:border-dark-700'"
            >
              <div class="flex items-start justify-between gap-3">
                <div>
                  <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('setup.mode.advancedTitle') }}</h3>
                  <p v-if="recommendedMode === 'advanced'" class="mt-1 text-xs font-medium text-primary-700 dark:text-primary-300">
                    {{ t('setup.mode.recommended') }}
                  </p>
                </div>
                <Icon name="cog" size="lg" class="text-gray-500 dark:text-dark-300" />
              </div>

              <p class="mt-4 text-sm leading-6 text-gray-600 dark:text-dark-300">
                {{ t('setup.mode.advancedDescription') }}
              </p>
              <ul class="mt-3 space-y-2 text-sm text-gray-500 dark:text-dark-400">
                <li class="flex gap-2"><span>•</span><span>{{ t('setup.mode.advancedRequirement') }}</span></li>
                <li class="flex gap-2"><span>•</span><span>{{ t('setup.mode.dockerExistingHint') }}</span></li>
                <li class="flex gap-2"><span>•</span><span>{{ t('setup.mode.advancedDockerRedirect') }}</span></li>
              </ul>

              <button type="button" class="btn btn-secondary mt-auto w-full" :disabled="provisioning" @click="selectAdvancedMode">
                {{ t('setup.mode.advancedAction') }}
              </button>
            </article>
          </div>

          <div v-if="quickBlocker" class="mt-5 rounded-xl border border-amber-200 bg-amber-50 p-4 dark:border-amber-800/60 dark:bg-amber-900/20">
            <p class="font-medium text-amber-900 dark:text-amber-200">{{ t('setup.mode.manualTitle') }}</p>
            <p class="mt-1 text-sm leading-6 text-amber-800 dark:text-amber-300">{{ t('setup.mode.manualDescription') }}</p>
          </div>

          <div v-if="provisioning" class="mt-5 rounded-xl border border-primary-200 bg-primary-50 p-4 dark:border-primary-800/60 dark:bg-primary-900/20" aria-live="polite">
            <div class="flex items-center justify-between gap-3">
              <div>
                <p class="font-medium text-primary-900 dark:text-primary-200">{{ t('setup.mode.progressTitle') }}</p>
                <p class="mt-1 text-sm text-primary-700 dark:text-primary-300">{{ provisionProgress.message }}</p>
              </div>
              <span class="font-mono text-sm text-primary-700 dark:text-primary-300">{{ provisionProgress.percent }}%</span>
            </div>
            <div class="mt-3 h-2 overflow-hidden rounded-full bg-primary-100 dark:bg-primary-950">
              <div class="h-full bg-primary-500 transition-[width] duration-200" :style="{ width: `${provisionProgress.percent}%` }"></div>
            </div>
            <p class="mt-2 text-xs text-primary-700 dark:text-primary-400">{{ t('setup.mode.progressHint') }}</p>
          </div>
        </section>

        <section v-else-if="currentStep === 1" class="space-y-6" aria-labelledby="database-step-title">
          <div class="mb-6 text-center">
            <h2 id="database-step-title" class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('setup.database.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('setup.database.description') }}</p>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div><label class="input-label">{{ t('setup.database.host') }}</label><input v-model="formData.database.host" type="text" class="input" placeholder="localhost" /></div>
            <div><label class="input-label">{{ t('setup.database.port') }}</label><input v-model.number="formData.database.port" type="number" class="input" placeholder="5432" /></div>
            <div><label class="input-label">{{ t('setup.database.username') }}</label><input v-model="formData.database.user" type="text" class="input" placeholder="postgres" /></div>
            <div><label class="input-label">{{ t('setup.database.password') }}</label><input v-model="formData.database.password" type="password" class="input" :placeholder="t('setup.database.passwordPlaceholder')" /></div>
            <div><label class="input-label">{{ t('setup.database.databaseName') }}</label><input v-model="formData.database.dbname" type="text" class="input" placeholder="sub2api" /></div>
            <div>
              <label class="input-label">{{ t('setup.database.sslMode') }}</label>
              <Select v-model="formData.database.sslmode" :options="[
                { value: 'disable', label: t('setup.database.ssl.disable') },
                { value: 'require', label: t('setup.database.ssl.require') },
                { value: 'verify-ca', label: t('setup.database.ssl.verifyCa') },
                { value: 'verify-full', label: t('setup.database.ssl.verifyFull') }
              ]" />
            </div>
          </div>

          <button type="button" class="btn btn-secondary w-full" :disabled="testingDb" @click="testDatabaseConnection">
            <span v-if="testingDb" class="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-r-transparent"></span>
            <Icon v-else-if="dbConnected" name="check" size="md" class="mr-2 text-green-500" :stroke-width="2" />
            {{ testingDb ? t('setup.status.testing') : dbConnected ? t('setup.status.success') : t('setup.status.testConnection') }}
          </button>
        </section>

        <section v-else-if="currentStep === 2" class="space-y-6" aria-labelledby="redis-step-title">
          <div class="mb-6 text-center">
            <h2 id="redis-step-title" class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('setup.redis.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('setup.redis.description') }}</p>
          </div>

          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div><label class="input-label">{{ t('setup.redis.host') }}</label><input v-model="formData.redis.host" type="text" class="input" placeholder="localhost" /></div>
            <div><label class="input-label">{{ t('setup.redis.port') }}</label><input v-model.number="formData.redis.port" type="number" class="input" placeholder="6379" /></div>
            <div><label class="input-label">{{ t('setup.redis.username') }}</label><input v-model="formData.redis.username" type="text" class="input" :placeholder="t('setup.redis.usernamePlaceholder')" /></div>
            <div><label class="input-label">{{ t('setup.redis.password') }}</label><input v-model="formData.redis.password" type="password" class="input" :placeholder="t('setup.redis.passwordPlaceholder')" /></div>
            <div><label class="input-label">{{ t('setup.redis.database') }}</label><input v-model.number="formData.redis.db" type="number" class="input" placeholder="0" /></div>
          </div>

          <div class="flex items-center justify-between rounded-xl border border-gray-200 p-3 dark:border-dark-700">
            <div><p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('setup.redis.enableTls') }}</p><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('setup.redis.enableTlsHint') }}</p></div>
            <Toggle v-model="formData.redis.enable_tls" />
          </div>

          <button type="button" class="btn btn-secondary w-full" :disabled="testingRedis" @click="testRedisConnection">
            <span v-if="testingRedis" class="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-r-transparent"></span>
            <Icon v-else-if="redisConnected" name="check" size="md" class="mr-2 text-green-500" :stroke-width="2" />
            {{ testingRedis ? t('setup.status.testing') : redisConnected ? t('setup.status.success') : t('setup.status.testConnection') }}
          </button>
        </section>

        <section v-else-if="currentStep === 3" class="space-y-6" aria-labelledby="admin-step-title">
          <div class="mb-6 text-center">
            <h2 id="admin-step-title" class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('setup.admin.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('setup.admin.description') }}</p>
          </div>
          <div><label class="input-label">{{ t('setup.admin.email') }}</label><input v-model="formData.admin.email" type="email" class="input" placeholder="admin@example.com" autocomplete="email" /></div>
          <div><label class="input-label">{{ t('setup.admin.password') }}</label><input v-model="formData.admin.password" type="password" class="input" :placeholder="t('setup.admin.passwordPlaceholder')" autocomplete="new-password" /></div>
          <div>
            <label class="input-label">{{ t('setup.admin.confirmPassword') }}</label>
            <input v-model="confirmPassword" type="password" class="input" :placeholder="t('setup.admin.confirmPasswordPlaceholder')" autocomplete="new-password" />
            <p v-if="confirmPassword && formData.admin.password !== confirmPassword" class="input-error-text">{{ t('setup.admin.passwordMismatch') }}</p>
          </div>
        </section>

        <section v-else class="space-y-6" aria-labelledby="ready-step-title">
          <div class="mb-6 text-center">
            <h2 id="ready-step-title" class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('setup.ready.title') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('setup.ready.description') }}</p>
          </div>
          <div class="space-y-4">
            <ReadyFact :label="t('setup.ready.setupMode')" :value="selectedMode === 'quick' ? t('setup.ready.quickMode') : t('setup.ready.advancedMode')" />
            <ReadyFact :label="t('setup.ready.database')" :value="`${formData.database.user}@${formData.database.host}:${formData.database.port}/${formData.database.dbname}`" />
            <ReadyFact :label="t('setup.ready.redis')" :value="`${formData.redis.host}:${formData.redis.port}`" />
            <ReadyFact :label="t('setup.ready.adminEmail')" :value="formData.admin.email" />
          </div>
        </section>

        <div v-if="errorMessage" class="mt-6 rounded-xl border border-red-200 bg-red-50 p-4 dark:border-red-800/50 dark:bg-red-900/20" role="alert">
          <div class="flex items-start gap-3"><Icon name="exclamationCircle" size="md" class="flex-shrink-0 text-red-500" /><p class="text-sm leading-6 text-red-700 dark:text-red-400">{{ errorMessage }}</p></div>
        </div>

        <div v-if="installSuccess" class="mt-6 rounded-xl border border-green-200 bg-green-50 p-4 dark:border-green-800/50 dark:bg-green-900/20" aria-live="polite">
          <div class="flex items-start gap-3">
            <span v-if="!serviceReady" class="mt-0.5 inline-block h-5 w-5 flex-shrink-0 animate-spin rounded-full border-2 border-green-500 border-r-transparent"></span>
            <Icon v-else name="checkCircle" size="md" class="flex-shrink-0 text-green-500" />
            <div><p class="text-sm font-medium text-green-700 dark:text-green-400">{{ t('setup.status.completed') }}</p><p class="mt-1 text-sm text-green-600 dark:text-green-500">{{ serviceReady ? t('setup.status.redirecting') : t('setup.status.restarting') }}</p></div>
          </div>
        </div>

        <div v-if="currentStep > 0" class="mt-8 flex justify-between gap-3">
          <button v-if="!installSuccess" type="button" class="btn btn-secondary" @click="previousStep"><Icon name="chevronLeft" size="sm" class="mr-2" :stroke-width="2" />{{ t('common.back') }}</button>
          <div v-else></div>
          <button v-if="currentStep < 4" type="button" class="btn btn-primary" :disabled="!canProceed" @click="nextStep">{{ t('common.next') }}<Icon name="chevronRight" size="sm" class="ml-2" :stroke-width="2" /></button>
          <button v-else-if="!installSuccess" type="button" class="btn btn-primary" :disabled="installing" @click="performInstall">
            <span v-if="installing" class="mr-2 inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-r-transparent"></span>
            {{ installing ? t('setup.status.installing') : t('setup.status.completeInstallation') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { listen, type UnlistenFn } from '@tauri-apps/api/event'
import {
  detectSetupEnvironment,
  install,
  provisionQuickSetup,
  testDatabase,
  testRedis,
  type InstallRequest,
  type SetupProvisionProgress,
} from '@/api/setup'
import { buildGatewayUrl } from '@/api/client'
import { isDesktopRuntime } from '@/api/url'
import {
  hasExistingLocalServices,
  quickSetupBlocker,
  recommendedSetupMode,
  type QuickSetupBlocker,
  type SetupEnvironment,
  type SetupMode,
} from '@/features/desktop/setupEnvironment'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'

const EnvironmentFact = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true }, available: Boolean },
  setup(props) {
    return () => h('div', { class: 'rounded-lg border border-gray-200 bg-white px-3 py-2 dark:border-dark-700 dark:bg-dark-800' }, [
      h('p', { class: 'text-[11px] text-gray-500 dark:text-dark-400' }, props.label),
      h('p', { class: ['mt-1 text-xs font-semibold', props.available ? 'text-green-600 dark:text-green-400' : 'text-gray-600 dark:text-dark-300'] }, props.value),
    ])
  },
})

const ReadyFact = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(props) {
    return () => h('div', { class: 'rounded-xl bg-gray-50 p-4 dark:bg-dark-700' }, [
      h('h3', { class: 'mb-2 text-sm font-medium text-gray-500 dark:text-dark-400' }, props.label),
      h('p', { class: 'break-all text-gray-900 dark:text-white' }, props.value),
    ])
  },
})

const { t } = useI18n()
const desktop = isDesktopRuntime()
const steps = computed(() => [
  { id: 'mode', title: t('setup.mode.title') },
  { id: 'database', title: t('setup.database.title') },
  { id: 'redis', title: t('setup.redis.title') },
  { id: 'admin', title: t('setup.admin.title') },
  { id: 'complete', title: t('setup.ready.title') },
])

const currentStep = ref(0)
const selectedMode = ref<SetupMode | null>(null)
const environment = ref<SetupEnvironment | null>(null)
const environmentChecking = ref(false)
const provisioning = ref(false)
const provisionProgress = reactive<SetupProvisionProgress>({ stage: '', message: '', percent: 0 })
const quickImages = reactive({ postgres: '16.14', valkey: '8.1.9' })
const errorMessage = ref('')
const installSuccess = ref(false)
const testingDb = ref(false)
const testingRedis = ref(false)
const dbConnected = ref(false)
const redisConnected = ref(false)
const installing = ref(false)
const confirmPassword = ref('')
const serviceReady = ref(false)
let unlistenProgress: UnlistenFn | null = null

const formData = reactive<InstallRequest>({
  database: { host: 'localhost', port: 5432, user: 'postgres', password: '', dbname: 'sub2api', sslmode: 'disable' },
  redis: { host: 'localhost', port: 6379, username: '', password: '', db: 0, enable_tls: false },
  admin: { email: '', password: '' },
  server: {
    host: desktop ? '127.0.0.1' : '0.0.0.0',
    port: desktop ? 18765 : Number(window.location.port || (window.location.protocol === 'https:' ? 443 : 80)),
    mode: 'release',
  },
})

const quickBlocker = computed(() => quickSetupBlocker(environment.value))
const recommendedMode = computed(() => recommendedSetupMode(environment.value))
const hasExistingServices = computed(() => hasExistingLocalServices(environment.value))
const quickBlockerMessage = computed(() => {
  const keys: Record<QuickSetupBlocker, string> = {
    desktop_only: 'setup.mode.blocker.desktopOnly',
    docker_missing: 'setup.mode.blocker.dockerMissing',
    docker_stopped: 'setup.mode.blocker.dockerStopped',
    managed_port_conflict: 'setup.mode.blocker.portConflict',
  }
  return quickBlocker.value ? t(keys[quickBlocker.value]) : ''
})

const canProceed = computed(() => {
  if (currentStep.value === 1) return dbConnected.value
  if (currentStep.value === 2) return redisConnected.value
  if (currentStep.value === 3) return Boolean(
    formData.admin.email
      && formData.admin.password.length >= 8
      && formData.admin.password === confirmPassword.value,
  )
  return true
})

function errorText(error: unknown): string {
  const candidate = error as { response?: { data?: { detail?: string; message?: string } }; message?: string }
  return candidate.response?.data?.detail || candidate.response?.data?.message || candidate.message || String(error)
}

async function refreshEnvironment() {
  environmentChecking.value = true
  errorMessage.value = ''
  try {
    environment.value = await detectSetupEnvironment()
  } catch (error) {
    errorMessage.value = errorText(error)
  } finally {
    environmentChecking.value = false
  }
}

function selectAdvancedMode() {
  selectedMode.value = 'advanced'
  dbConnected.value = false
  redisConnected.value = false
  currentStep.value = 1
  errorMessage.value = ''
}

async function verifyManagedConnections() {
  let databaseError = ''
  let redisError = ''
  for (let attempt = 0; attempt < 12; attempt++) {
    const [databaseResult, redisResult] = await Promise.allSettled([
      testDatabase(formData.database),
      testRedis(formData.redis),
    ])
    dbConnected.value = databaseResult.status === 'fulfilled'
    redisConnected.value = redisResult.status === 'fulfilled'
    if (databaseResult.status === 'rejected') databaseError = errorText(databaseResult.reason)
    if (redisResult.status === 'rejected') redisError = errorText(redisResult.reason)
    if (dbConnected.value && redisConnected.value) return
    await new Promise((resolve) => window.setTimeout(resolve, 1500))
  }
  throw new Error(t('setup.status.managedVerificationFailed', {
    database: databaseError || t('setup.status.notReady'),
    redis: redisError || t('setup.status.notReady'),
  }))
}

async function startQuickSetup() {
  if (quickBlocker.value) return
  selectedMode.value = 'quick'
  provisioning.value = true
  errorMessage.value = ''
  provisionProgress.stage = 'checking'
  provisionProgress.message = t('setup.mode.checking')
  provisionProgress.percent = 3
  try {
    const managed = await provisionQuickSetup()
    Object.assign(formData.database, managed.database)
    Object.assign(formData.redis, managed.redis)
    quickImages.postgres = managed.postgres_image.replace(/^postgres:/, '')
    quickImages.valkey = managed.valkey_image.replace(/^valkey\/valkey:/, '')
    await verifyManagedConnections()
    currentStep.value = 3
  } catch (error) {
    errorMessage.value = errorText(error)
    if (formData.database.port === 15432 || formData.redis.port === 16379) currentStep.value = dbConnected.value ? 2 : 1
    await refreshEnvironment()
  } finally {
    provisioning.value = false
  }
}

async function testDatabaseConnection() {
  testingDb.value = true
  errorMessage.value = ''
  dbConnected.value = false
  try {
    await testDatabase(formData.database)
    dbConnected.value = true
  } catch (error) {
    errorMessage.value = errorText(error)
  } finally {
    testingDb.value = false
  }
}

async function testRedisConnection() {
  testingRedis.value = true
  errorMessage.value = ''
  redisConnected.value = false
  try {
    await testRedis(formData.redis)
    redisConnected.value = true
  } catch (error) {
    errorMessage.value = errorText(error)
  } finally {
    testingRedis.value = false
  }
}

function previousStep() {
  errorMessage.value = ''
  if (selectedMode.value === 'quick' && currentStep.value === 3) currentStep.value = 0
  else if (currentStep.value === 1) currentStep.value = 0
  else currentStep.value--
}

function nextStep() {
  if (!canProceed.value) return
  errorMessage.value = ''
  currentStep.value++
}

async function performInstall() {
  installing.value = true
  errorMessage.value = ''
  try {
    await install(formData)
    installSuccess.value = true
    void waitForServiceRestart()
  } catch (error) {
    errorMessage.value = errorText(error)
  } finally {
    installing.value = false
  }
}

async function waitForServiceRestart() {
  await new Promise((resolve) => window.setTimeout(resolve, 3000))
  for (let attempt = 0; attempt < 60; attempt++) {
    try {
      const response = await fetch(buildGatewayUrl('/setup/status'), { method: 'GET', cache: 'no-store' })
      if (response.ok) {
        const data = await response.json()
        if (data.data && !data.data.needs_setup) {
          serviceReady.value = true
          window.setTimeout(() => { window.location.href = '/login' }, 1500)
          return
        }
      }
    } catch {
      // The managed backend is expected to be briefly unavailable while restarting.
    }
    await new Promise((resolve) => window.setTimeout(resolve, 1000))
  }
  errorMessage.value = t('setup.status.timeout')
}

onMounted(async () => {
  if (desktop) {
    unlistenProgress = await listen<SetupProvisionProgress>('setup-provision-progress', (event) => {
      Object.assign(provisionProgress, event.payload)
    })
  }
  await refreshEnvironment()
})

onBeforeUnmount(() => unlistenProgress?.())
</script>
