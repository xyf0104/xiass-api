<template>
  <div class="relative flex min-h-screen flex-col bg-gray-50 dark:bg-dark-950">
    <!-- Header (same pattern as HomeView) -->
    <header class="relative z-20 px-6 py-4">
      <nav class="mx-auto flex max-w-6xl items-center justify-between">
        <router-link to="/home" class="flex items-center gap-3">
          <div class="h-10 w-10 overflow-hidden rounded-xl shadow-md">
            <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <span class="text-lg font-semibold tracking-tight text-gray-900 dark:text-white">{{ siteName }}</span>
        </router-link>
        <div class="flex items-center gap-3">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            @click="toggleTheme"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="flex-1 w-full max-w-5xl mx-auto px-6 py-12">
      <!-- Hero -->
      <div class="text-center mb-12">
        <h1 class="text-3xl sm:text-4xl font-bold tracking-tight mb-3 text-gray-900 dark:text-white">
          {{ t('keyUsage.title') }}
        </h1>
        <p class="text-gray-500 dark:text-dark-400 text-base max-w-md mx-auto">
          {{ t('keyUsage.subtitle') }}
        </p>
      </div>

      <!-- Input Section -->
      <div class="max-w-xl mx-auto mb-14">
        <div class="flex gap-3">
          <div class="flex-1 relative">
            <div class="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-500">
              <svg class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>
              </svg>
            </div>
            <input
              v-model="apiKey"
              :type="keyVisible ? 'text' : 'password'"
              :placeholder="t('keyUsage.placeholder')"
              class="input-ring w-full h-12 pl-12 pr-12 rounded-xl border border-gray-200 bg-white text-sm text-gray-900 placeholder:text-gray-400 transition-all dark:border-dark-700 dark:bg-dark-900 dark:text-white dark:placeholder:text-dark-500"
              @keydown.enter="queryKey"
            />
            <button
              @click="keyVisible = !keyVisible"
              class="absolute right-4 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-700 dark:text-dark-500 dark:hover:text-white transition-colors"
            >
              <svg v-if="!keyVisible" class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/>
                <line x1="1" y1="1" x2="23" y2="23"/>
              </svg>
              <svg v-else class="w-5 h-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>
              </svg>
            </button>
          </div>
          <button
            @click="queryKey"
            :disabled="isQuerying"
            class="h-12 px-7 rounded-xl bg-primary-500 hover:bg-primary-600 text-white font-medium text-sm transition-all active:scale-[0.97] flex items-center gap-2 whitespace-nowrap disabled:opacity-60"
          >
            <svg v-if="isQuerying" class="w-4 h-4 animate-spin" viewBox="0 0 24 24" fill="none">
              <circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" opacity="0.25"/>
              <path d="M12 2a10 10 0 0 1 10 10" stroke="currentColor" stroke-width="3" stroke-linecap="round"/>
            </svg>
            <svg v-else class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            {{ isQuerying ? t('keyUsage.querying') : t('keyUsage.query') }}
          </button>
        </div>
        <p class="text-xs text-gray-400 dark:text-dark-500 mt-3 text-center">
          {{ t('keyUsage.privacyNote') }}
        </p>

        <!-- Date Range Picker -->
        <div v-if="showDatePicker" class="mt-4">
          <div class="flex flex-wrap items-center gap-2 justify-center">
            <span class="text-xs text-gray-500 dark:text-dark-400">{{ t('keyUsage.dateRange') }}</span>
            <button
              v-for="range in dateRanges"
              :key="range.key"
              @click="setDateRange(range.key)"
              class="text-xs px-3 py-1.5 rounded-lg border transition-all"
              :class="currentRange === range.key
                ? 'bg-primary-500 text-white border-primary-500'
                : 'border-gray-200 bg-white text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200 hover:border-primary-300 dark:hover:border-dark-600'"
            >{{ range.label }}</button>
            <div v-if="currentRange === 'custom'" class="flex items-center gap-2 ml-1">
              <input
                v-model="customStartDate"
                type="date"
                class="input-ring text-xs px-2 py-1.5 rounded-lg border border-gray-200 bg-white text-gray-900 dark:border-dark-700 dark:bg-dark-900 dark:text-white"
              />
              <span class="text-xs text-gray-400">-</span>
              <input
                v-model="customEndDate"
                type="date"
                class="input-ring text-xs px-2 py-1.5 rounded-lg border border-gray-200 bg-white text-gray-900 dark:border-dark-700 dark:bg-dark-900 dark:text-white"
              />
              <button
                @click="queryKey"
                class="text-xs px-3 py-1.5 rounded-lg bg-primary-500 text-white hover:bg-primary-600"
              >{{ t('keyUsage.apply') }}</button>
            </div>
          </div>
        </div>
      </div>

      <!-- Results Container -->
      <div v-if="showResults">
        <!-- Loading Skeleton -->
        <div v-if="showLoading" class="space-y-6">
          <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div class="rounded-2xl border border-gray-200 bg-white p-8 dark:border-dark-700 dark:bg-dark-900">
              <div class="skeleton h-5 w-24 mb-6"></div>
              <div class="flex justify-center"><div class="skeleton w-44 h-44 rounded-full"></div></div>
            </div>
            <div class="rounded-2xl border border-gray-200 bg-white p-8 dark:border-dark-700 dark:bg-dark-900">
              <div class="skeleton h-5 w-24 mb-6"></div>
              <div class="flex justify-center"><div class="skeleton w-44 h-44 rounded-full"></div></div>
            </div>
          </div>
          <div class="rounded-2xl border border-gray-200 bg-white p-8 dark:border-dark-700 dark:bg-dark-900">
            <div class="skeleton h-5 w-32 mb-6"></div>
            <div class="space-y-4">
              <div class="skeleton h-4 w-full"></div>
              <div class="skeleton h-4 w-3/4"></div>
              <div class="skeleton h-4 w-5/6"></div>
              <div class="skeleton h-4 w-2/3"></div>
            </div>
          </div>
        </div>

        <!-- Result Content -->
        <div v-else-if="resultData" class="space-y-6">
          <!-- Status Badge -->
          <div v-if="statusInfo" class="fade-up flex items-center justify-center mb-2">
            <div class="inline-flex items-center gap-2 px-5 py-2.5 rounded-full border border-gray-200 bg-white/90 shadow-sm backdrop-blur-sm dark:border-dark-700 dark:bg-dark-900/90">
              <span
                class="w-2.5 h-2.5 rounded-full pulse-dot"
                :class="statusInfo.isActive ? 'bg-emerald-500' : 'bg-rose-500'"
              ></span>
              <span class="text-sm font-medium text-gray-900 dark:text-white">{{ statusInfo.label }}</span>
              <span class="text-xs text-gray-400 dark:text-dark-500">|</span>
              <span class="text-xs text-gray-500 dark:text-dark-400">{{ statusInfo.statusText }}</span>
            </div>
          </div>

          <!-- Ring Cards Grid -->
          <div v-if="ringItems.length > 0" :class="ringGridClass">
            <div
              v-for="(ring, i) in ringItems"
              :key="i"
              class="fade-up rounded-2xl border border-gray-200 bg-white/90 p-8 backdrop-blur-sm transition-all duration-300 hover:shadow-lg dark:border-dark-700 dark:bg-dark-900/90"
              :class="`fade-up-delay-${Math.min(i + 1, 4)}`"
            >
              <div class="flex items-center justify-between mb-6">
                <h3 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">
                  {{ ring.title }}
                </h3>
                <!-- Clock icon -->
                <svg v-if="ring.iconType === 'clock'" class="w-5 h-5 text-gray-400 dark:text-dark-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>
                </svg>
                <!-- Calendar icon -->
                <svg v-else-if="ring.iconType === 'calendar'" class="w-5 h-5 text-gray-400 dark:text-dark-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="3" y="4" width="18" height="18" rx="2" ry="2"/><line x1="16" y1="2" x2="16" y2="6"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="3" y1="10" x2="21" y2="10"/>
                </svg>
                <!-- Dollar icon -->
                <svg v-else class="w-5 h-5 text-gray-400 dark:text-dark-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="12" y1="1" x2="12" y2="23"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>
                </svg>
              </div>
              <div class="flex justify-center">
                <div class="relative">
                  <svg class="w-44 h-44" viewBox="0 0 160 160">
                    <circle cx="80" cy="80" r="68" fill="none" :stroke="ringTrackColor" stroke-width="10"/>
                    <circle
                      class="progress-ring"
                      cx="80" cy="80" r="68" fill="none"
                      :stroke="`url(#ring-grad-${i})`"
                      stroke-width="10" stroke-linecap="round"
                      :stroke-dasharray="CIRCUMFERENCE.toFixed(2)"
                      :stroke-dashoffset="getRingOffset(ring)"
                    />
                    <defs>
                      <linearGradient :id="`ring-grad-${i}`" x1="0%" y1="0%" x2="100%" y2="100%">
                        <stop offset="0%" :stop-color="RING_GRADIENTS[i % 4].from"/>
                        <stop offset="100%" :stop-color="RING_GRADIENTS[i % 4].to"/>
                      </linearGradient>
                    </defs>
                  </svg>
                  <div class="absolute inset-0 flex flex-col items-center justify-center">
                    <template v-if="ring.isBalance">
                      <span class="text-2xl font-bold tabular-nums" :style="{ color: RING_GRADIENTS[i % 4].from }">
                        {{ ring.amount }}
                      </span>
                    </template>
                    <template v-else>
                      <span class="text-3xl font-bold tabular-nums text-gray-900 dark:text-white">
                        {{ displayPcts[i] ?? 0 }}%
                      </span>
                      <span class="text-xs text-gray-500 dark:text-dark-400 mt-0.5">{{ t('keyUsage.used') }}</span>
                      <span
                        class="text-sm font-semibold mt-1 tabular-nums"
                        :style="{ color: RING_GRADIENTS[i % 4].from }"
                      >{{ ring.amount }}</span>
                      <p v-if="ring.resetAt && formatResetTime(ring.resetAt)" class="text-xs text-gray-400 dark:text-gray-500 mt-0.5 tabular-nums">
                        ⟳ {{ formatResetTime(ring.resetAt) }}
                      </p>
                    </template>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <!-- Detail Card -->
          <div
            v-if="detailRows.length > 0"
            class="fade-up fade-up-delay-3 rounded-2xl border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="px-8 py-5 border-b border-gray-200 dark:border-dark-700">
              <h3 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.detailInfo') }}</h3>
            </div>
            <div class="divide-y divide-gray-100 dark:divide-dark-800">
              <div
                v-for="(row, i) in detailRows"
                :key="i"
                class="px-8 py-4 flex items-center justify-between"
              >
                <div class="flex items-center gap-3">
                  <div class="w-8 h-8 rounded-lg flex items-center justify-center" :class="row.iconBg">
                    <svg
                      class="w-4 h-4"
                      :class="row.iconColor"
                      viewBox="0 0 24 24" fill="none" stroke="currentColor"
                      stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
                      v-html="row.iconSvg"
                    ></svg>
                  </div>
                  <span class="text-sm text-gray-700 dark:text-dark-200">{{ row.label }}</span>
                </div>
                <span class="text-sm font-semibold tabular-nums" :class="row.valueClass || 'text-gray-900 dark:text-white'">
                  {{ row.value }}
                </span>
              </div>
            </div>
          </div>

          <!-- Usage Stats Card -->
          <div
            v-if="usageStatCells.length > 0"
            class="fade-up fade-up-delay-3 rounded-2xl border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="px-8 py-5 border-b border-gray-200 dark:border-dark-700">
              <h3 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.tokenStats') }}</h3>
            </div>
            <div class="grid grid-cols-2 md:grid-cols-4 gap-px bg-gray-100 dark:bg-dark-800">
              <div
                v-for="(cell, i) in usageStatCells"
                :key="i"
                class="bg-white px-6 py-4 dark:bg-dark-900"
              >
                <div class="text-xs text-gray-500 dark:text-dark-400 mb-1">{{ cell.label }}</div>
                <div class="text-sm font-semibold tabular-nums text-gray-900 dark:text-white">{{ cell.value }}</div>
              </div>
            </div>
          </div>

          <!-- Daily Usage Table -->
          <div
            v-if="showDailyUsage"
            class="fade-up fade-up-delay-4 rounded-2xl border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="flex flex-col gap-3 px-8 py-5 border-b border-gray-200 dark:border-dark-700 sm:flex-row sm:items-center sm:justify-between">
              <h3 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.dailyDetail') }}</h3>
              <div class="inline-flex rounded-lg border border-gray-200 bg-white p-0.5 dark:border-dark-700 dark:bg-dark-950">
                <button
                  v-for="option in dailyUsageOptions"
                  :key="option.value"
                  @click="setDailyUsageDays(option.value)"
                  class="min-w-12 rounded-md px-3 py-1.5 text-xs font-medium transition-colors"
                  :class="dailyUsageDays === option.value
                    ? 'bg-primary-500 text-white'
                    : 'text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-800'"
                >
                  {{ option.label }}
                </button>
              </div>
            </div>
            <div v-if="dailyUsageRows.length > 0" class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr class="border-b border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950">
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.date') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.requests') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.inputTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.outputTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.cacheReadTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.cacheWriteTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.cost') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="row in dailyUsageRows"
                    :key="row.date"
                    class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
                  >
                    <td class="px-4 py-3 text-sm font-medium whitespace-nowrap text-gray-900 dark:text-white">{{ row.date }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(row.requests) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(row.input_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(row.output_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(row.cache_read_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(row.cache_write_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right font-medium text-gray-900 dark:text-white">{{ usd(row.actual_cost != null ? row.actual_cost : row.cost) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div v-else class="px-8 py-8 text-center text-sm text-gray-500 dark:text-dark-400">
              {{ t('keyUsage.noDailyUsage') }}
            </div>
          </div>

          <!-- Model Stats Table -->
          <div
            v-if="modelStats.length > 0"
            class="fade-up fade-up-delay-4 rounded-2xl border border-gray-200 bg-white/90 backdrop-blur-sm overflow-hidden dark:border-dark-700 dark:bg-dark-900/90"
          >
            <div class="px-8 py-5 border-b border-gray-200 dark:border-dark-700">
              <h3 class="text-sm font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.modelStats') }}</h3>
            </div>
            <div class="overflow-x-auto">
              <table class="w-full">
                <thead>
                  <tr class="border-b border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950">
                    <th class="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.model') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.requests') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.inputTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.outputTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.cacheCreationTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.cacheReadTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.totalTokens') }}</th>
                    <th class="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-gray-500 dark:text-dark-400">{{ t('keyUsage.cost') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="(m, i) in modelStats"
                    :key="i"
                    class="border-b border-gray-100 last:border-b-0 dark:border-dark-800"
                  >
                    <td class="px-4 py-3 text-sm font-medium whitespace-nowrap text-gray-900 dark:text-white">{{ m.model || '-' }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(m.requests) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(m.input_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(m.output_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(m.cache_creation_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(m.cache_read_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right text-gray-700 dark:text-dark-200">{{ fmtNum(m.total_tokens) }}</td>
                    <td class="px-4 py-3 text-sm tabular-nums text-right font-medium text-gray-900 dark:text-white">{{ usd(m.actual_cost != null ? m.actual_cost : m.cost) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Footer (same pattern as HomeView) -->
    <footer class="relative z-10 border-t border-gray-200/50 px-6 py-8 dark:border-dark-800/50">
      <div class="mx-auto flex max-w-6xl flex-col items-center justify-center gap-4 text-center sm:flex-row sm:text-left">
        <p class="text-sm text-gray-500 dark:text-dark-400">
          &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
        </p>
        <div class="flex items-center gap-4">
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >{{ t('home.docs') }}</a>
          <a
            v-if="githubUrl"
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >GitHub</a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { buildGatewayUrl } from '@/api/client'
import { sanitizeUrl } from '@/utils/url'

const { t, locale } = useI18n()
const appStore = useAppStore()


