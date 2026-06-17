<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="homeContent" class="min-h-screen">
    <!-- iframe mode -->
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <!-- HTML mode - SECURITY: homeContent is admin-only setting, XSS risk is acceptable -->
    <div v-else v-html="homeContent"></div>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="relative flex min-h-screen flex-col overflow-hidden bg-[#0a0e1a]"
  >
    <!-- 深色科技感背景：粒子 Canvas + 光球 -->
    <canvas ref="homeCanvasRef" class="pointer-events-none absolute inset-0 h-full w-full"></canvas>
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="absolute -right-20 -top-20 h-[500px] w-[500px] rounded-full bg-primary-500/8 blur-[100px] animate-pulse-slow"></div>
      <div class="absolute -bottom-32 -left-32 h-[400px] w-[400px] rounded-full bg-cyan-500/6 blur-[80px] animate-pulse-slow [animation-delay:2s]"></div>
      <div class="absolute left-1/2 top-1/3 h-64 w-64 -translate-x-1/2 rounded-full bg-primary-400/5 blur-[60px] animate-pulse-slow [animation-delay:4s]"></div>
    </div>

    <!-- Header -->
    <header class="relative z-20 px-6 py-4">
      <nav class="mx-auto flex max-w-6xl items-center justify-between">
        <!-- Brand: 纯文字标识（不使用 logo 图片） -->
        <div class="flex items-center">
          <span class="text-lg font-bold tracking-tight text-gray-900 dark:text-white">
            {{ siteName }}
          </span>
        </div>

        <!-- Center Nav Menu -->
        <div class="hidden items-center gap-7 md:flex">
          <a href="#top" class="text-sm font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-primary-400">首页</a>
          <a href="#pricing" class="text-sm font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-primary-400">模型价格</a>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="text-sm font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-primary-400">接入文档</a>
          <a href="#faq" class="text-sm font-medium text-gray-600 transition-colors hover:text-primary-600 dark:text-dark-300 dark:hover:text-primary-400">常见问题</a>
        </div>

        <!-- Nav Actions -->
        <div class="flex items-center gap-3">
          <!-- Language Switcher -->
          <LocaleSwitcher />

          <!-- Doc Link -->
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

          <!-- Theme Toggle -->
          <button
            @click="toggleTheme"
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>

          <!-- Login / Dashboard Button -->
          <router-link
            v-if="isAuthenticated"
            :to="dashboardPath"
            class="inline-flex items-center gap-1.5 rounded-full bg-gray-900 py-1 pl-1 pr-2.5 transition-colors hover:bg-gray-800 dark:bg-gray-800 dark:hover:bg-gray-700"
          >
            <span
              class="flex h-5 w-5 items-center justify-center rounded-full bg-gradient-to-br from-primary-400 to-primary-600 text-[10px] font-semibold text-white"
            >
              {{ userInitial }}
            </span>
            <span class="text-xs font-medium text-white">{{ t('home.dashboard') }}</span>
            <svg
              class="h-3 w-3 text-gray-400"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M4.5 19.5l15-15m0 0H8.25m11.25 0v11.25"
              />
            </svg>
          </router-link>
          <router-link
            v-else
            to="/login"
            class="inline-flex items-center rounded-full bg-gray-900 px-3 py-1 text-xs font-medium text-white transition-colors hover:bg-gray-800 dark:bg-gray-800 dark:hover:bg-gray-700"
          >
            {{ t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <!-- Main Content -->
    <main class="relative z-10 flex-1 px-6 py-16">
      <div id="top" class="mx-auto max-w-6xl">
        <!-- Hero Section - Left/Right Layout -->
        <div class="mb-12 flex flex-col items-center justify-between gap-12 lg:flex-row lg:gap-16">
          <!-- Left: Text Content -->
          <div class="flex-1 text-center lg:text-left">
            <span
              class="mb-6 inline-flex items-center rounded-full border border-primary-200 bg-primary-50 px-3.5 py-1 text-xs font-semibold uppercase tracking-[0.2em] text-primary-700 dark:border-primary-800/60 dark:bg-primary-900/20 dark:text-primary-300"
            >
              The Universal AI Gateway
            </span>
            <h1
              class="mb-7 text-5xl font-black leading-none tracking-tight text-gray-900 dark:text-white md:text-6xl lg:text-[4.25rem]"
            >
              <span class="block">连接全球顶级</span>
              <span class="mt-5 block text-primary-500 dark:text-primary-400">AI 大模型</span>
            </h1>
            <p class="mb-9 max-w-xl text-base leading-[1.9] text-gray-500 dark:text-dark-300 md:text-[1.0625rem]">
              为开发者与团队而生 —— 高速直连、稳定可靠、余额永不过期。支持支付宝 / 微信支付，低延迟调用 Claude、ChatGPT、Gemini 等主流模型。
            </p>

            <!-- CTA Buttons -->
            <div class="flex flex-col items-center gap-3 sm:flex-row lg:items-start">
              <router-link
                :to="isAuthenticated ? dashboardPath : '/login'"
                class="btn btn-primary px-8 py-3 text-base shadow-lg shadow-primary-500/30"
              >
                {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
                <Icon name="arrowRight" size="md" class="ml-2" :stroke-width="2" />
              </router-link>
              <a href="#pricing" class="btn btn-secondary px-8 py-3 text-base">查看价格</a>
            </div>
          </div>

          <!-- Right: Model Constellation -->
          <div class="flex flex-1 justify-center lg:justify-end">
            <div class="relative h-80 w-80">
              <!-- connecting lines (center → model nodes) -->
              <svg
                class="pointer-events-none absolute inset-0 h-full w-full text-primary-400/40 dark:text-primary-500/35"
                viewBox="0 0 320 320"
                fill="none"
              >
                <g stroke="currentColor" stroke-width="1.5" stroke-dasharray="4 5">
                  <line x1="160" y1="160" x2="48" y2="48" />
                  <line x1="160" y1="160" x2="272" y2="48" />
                  <line x1="160" y1="160" x2="48" y2="272" />
                  <line x1="160" y1="160" x2="272" y2="272" />
                </g>
              </svg>
              <!-- orbit rings -->
              <div class="absolute inset-0 rounded-full border border-dashed border-primary-400/30 dark:border-primary-600/30"></div>
              <div class="absolute inset-10 rounded-full border border-dashed border-primary-400/20 dark:border-primary-600/20"></div>
              <!-- center node (gateway) -->
              <div
                class="absolute left-1/2 top-1/2 flex h-24 w-24 -translate-x-1/2 -translate-y-1/2 animate-glow items-center justify-center rounded-3xl bg-gradient-to-br from-primary-400 to-primary-600 shadow-xl shadow-primary-500/40"
              >
                <Icon name="swap" size="lg" class="text-white" />
              </div>
              <!-- Claude (top-left) -->
              <div class="absolute left-[15%] top-[15%] -translate-x-1/2 -translate-y-1/2">
                <div class="flex animate-float flex-col items-center">
                  <div class="flex h-14 w-14 items-center justify-center rounded-2xl bg-white shadow-lg ring-1 ring-primary-100 dark:bg-dark-800 dark:ring-dark-700">
                    <BrandIcon name="claude" class="h-7 w-7 text-[#D97757]" />
                  </div>
                  <span class="mt-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">Claude</span>
                </div>
              </div>
              <!-- All Models (top-right) -->
              <div class="absolute right-[15%] top-[15%] translate-x-1/2 -translate-y-1/2">
                <div class="flex animate-float flex-col items-center [animation-delay:0.8s]">
                  <div class="flex h-14 w-14 items-center justify-center rounded-2xl bg-white shadow-lg ring-1 ring-primary-100 dark:bg-dark-800 dark:ring-dark-700">
                    <Icon name="sparkles" size="md" class="text-primary-500" />
                  </div>
                  <span class="mt-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">All Models</span>
                </div>
              </div>
              <!-- ChatGPT (bottom-left) -->
              <div class="absolute bottom-[15%] left-[15%] -translate-x-1/2 translate-y-1/2">
                <div class="flex animate-float flex-col items-center [animation-delay:1.6s]">
                  <div class="flex h-14 w-14 items-center justify-center rounded-2xl bg-white shadow-lg ring-1 ring-primary-100 dark:bg-dark-800 dark:ring-dark-700">
                    <BrandIcon name="openai" class="h-7 w-7 text-gray-900 dark:text-white" />
                  </div>
                  <span class="mt-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">ChatGPT</span>
                </div>
              </div>
              <!-- Gemini (bottom-right) -->
              <div class="absolute bottom-[15%] right-[15%] translate-x-1/2 translate-y-1/2">
                <div class="flex animate-float flex-col items-center [animation-delay:2.4s]">
                  <div class="flex h-14 w-14 items-center justify-center rounded-2xl bg-white shadow-lg ring-1 ring-primary-100 dark:bg-dark-800 dark:ring-dark-700">
                    <BrandIcon name="gemini" class="h-7 w-7 text-[#4285F4]" />
                  </div>
                  <span class="mt-1.5 text-xs font-medium text-gray-500 dark:text-dark-400">Gemini</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- Feature Tags - Centered -->
        <div class="mb-12 flex flex-wrap items-center justify-center gap-4 md:gap-6">
          <div
            class="inline-flex items-center gap-2.5 rounded-full border border-gray-200/50 bg-white/80 px-5 py-2.5 shadow-sm backdrop-blur-sm dark:border-dark-700/50 dark:bg-dark-800/80"
          >
            <Icon name="swap" size="sm" class="text-primary-500" />
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{
              t('home.tags.subscriptionToApi')
            }}</span>
          </div>
          <div
            class="inline-flex items-center gap-2.5 rounded-full border border-gray-200/50 bg-white/80 px-5 py-2.5 shadow-sm backdrop-blur-sm dark:border-dark-700/50 dark:bg-dark-800/80"
          >
            <Icon name="shield" size="sm" class="text-primary-500" />
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{
              t('home.tags.stickySession')
            }}</span>
          </div>
          <div
            class="inline-flex items-center gap-2.5 rounded-full border border-gray-200/50 bg-white/80 px-5 py-2.5 shadow-sm backdrop-blur-sm dark:border-dark-700/50 dark:bg-dark-800/80"
          >
            <Icon name="chart" size="sm" class="text-primary-500" />
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{
              t('home.tags.realtimeBilling')
            }}</span>
          </div>
        </div>

        <!-- Features Grid -->
        <div class="mb-12 grid gap-6 md:grid-cols-3">
          <!-- Feature 1: Unified Gateway -->
          <div
            class="group rounded-2xl border border-gray-200/50 bg-white/60 p-6 backdrop-blur-sm transition-all duration-300 hover:shadow-xl hover:shadow-primary-500/10 dark:border-dark-700/50 dark:bg-dark-800/60"
          >
            <div
              class="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-gradient-to-br from-blue-500 to-blue-600 shadow-lg shadow-blue-500/30 transition-transform group-hover:scale-110"
            >
              <Icon name="server" size="lg" class="text-white" />
            </div>
            <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.unifiedGateway') }}
            </h3>
            <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-400">
              {{ t('home.features.unifiedGatewayDesc') }}
            </p>
          </div>

          <!-- Feature 2: Account Pool -->
          <div
            class="group rounded-2xl border border-gray-200/50 bg-white/60 p-6 backdrop-blur-sm transition-all duration-300 hover:shadow-xl hover:shadow-primary-500/10 dark:border-dark-700/50 dark:bg-dark-800/60"
          >
            <div
              class="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-gradient-to-br from-primary-500 to-primary-600 shadow-lg shadow-primary-500/30 transition-transform group-hover:scale-110"
            >
              <svg
                class="h-6 w-6 text-white"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="1.5"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M18 18.72a9.094 9.094 0 003.741-.479 3 3 0 00-4.682-2.72m.94 3.198l.001.031c0 .225-.012.447-.037.666A11.944 11.944 0 0112 21c-2.17 0-4.207-.576-5.963-1.584A6.062 6.062 0 016 18.719m12 0a5.971 5.971 0 00-.941-3.197m0 0A5.995 5.995 0 0012 12.75a5.995 5.995 0 00-5.058 2.772m0 0a3 3 0 00-4.681 2.72 8.986 8.986 0 003.74.477m.94-3.197a5.971 5.971 0 00-.94 3.197M15 6.75a3 3 0 11-6 0 3 3 0 016 0zm6 3a2.25 2.25 0 11-4.5 0 2.25 2.25 0 014.5 0zm-13.5 0a2.25 2.25 0 11-4.5 0 2.25 2.25 0 014.5 0z"
                />
              </svg>
            </div>
            <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.multiAccount') }}
            </h3>
            <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-400">
              {{ t('home.features.multiAccountDesc') }}
            </p>
          </div>

          <!-- Feature 3: Billing & Quota -->
          <div
            class="group rounded-2xl border border-gray-200/50 bg-white/60 p-6 backdrop-blur-sm transition-all duration-300 hover:shadow-xl hover:shadow-primary-500/10 dark:border-dark-700/50 dark:bg-dark-800/60"
          >
            <div
              class="mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-gradient-to-br from-purple-500 to-purple-600 shadow-lg shadow-purple-500/30 transition-transform group-hover:scale-110"
            >
              <svg
                class="h-6 w-6 text-white"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                stroke-width="1.5"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z"
                />
              </svg>
            </div>
            <h3 class="mb-2 text-lg font-semibold text-gray-900 dark:text-white">
              {{ t('home.features.balanceQuota') }}
            </h3>
            <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-400">
              {{ t('home.features.balanceQuotaDesc') }}
            </p>
          </div>
        </div>

        <!-- Supported Providers -->
        <div class="mb-8 text-center">
          <h2 class="mb-3 text-2xl font-bold text-gray-900 dark:text-white">
            {{ t('home.providers.title') }}
          </h2>
          <p class="text-sm text-gray-600 dark:text-dark-400">
            {{ t('home.providers.description') }}
          </p>
        </div>

        <div class="mb-16 flex flex-wrap items-center justify-center gap-4">
          <!-- Claude - Supported -->
          <div
            class="flex items-center gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
          >
            <div
              class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-orange-400 to-orange-500"
            >
              <BrandIcon name="claude" class="h-4 w-4 text-white" />
            </div>
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.claude') }}</span>
            <span
              class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
              >{{ t('home.providers.supported') }}</span
            >
          </div>
          <!-- GPT - Supported -->
          <div
            class="flex items-center gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
          >
            <div
              class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-green-500 to-green-600"
            >
              <BrandIcon name="openai" class="h-4 w-4 text-white" />
            </div>
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">GPT</span>
            <span
              class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
              >{{ t('home.providers.supported') }}</span
            >
          </div>
          <!-- Gemini - Supported -->
          <div
            class="flex items-center gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
          >
            <div
              class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-blue-500 to-blue-600"
            >
              <BrandIcon name="gemini" class="h-4 w-4 text-white" />
            </div>
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.gemini') }}</span>
            <span
              class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
              >{{ t('home.providers.supported') }}</span
            >
          </div>
          <!-- Antigravity - Supported -->
          <div
            class="flex items-center gap-2 rounded-xl border border-primary-200 bg-white/60 px-5 py-3 ring-1 ring-primary-500/20 backdrop-blur-sm dark:border-primary-800 dark:bg-dark-800/60"
          >
            <div
              class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-rose-500 to-pink-600"
            >
              <BrandIcon name="antigravity" class="h-4 w-4 text-white" />
            </div>
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.antigravity') }}</span>
            <span
              class="rounded bg-primary-100 px-1.5 py-0.5 text-[10px] font-medium text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"
              >{{ t('home.providers.supported') }}</span
            >
          </div>
          <!-- More - Coming Soon -->
          <div
            class="flex items-center gap-2 rounded-xl border border-gray-200/50 bg-white/40 px-5 py-3 opacity-60 backdrop-blur-sm dark:border-dark-700/50 dark:bg-dark-800/40"
          >
            <div
              class="flex h-8 w-8 items-center justify-center rounded-lg bg-gradient-to-br from-gray-500 to-gray-600"
            >
              <span class="text-xs font-bold text-white">+</span>
            </div>
            <span class="text-sm font-medium text-gray-700 dark:text-dark-200">{{ t('home.providers.more') }}</span>
            <span
              class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500 dark:bg-dark-700 dark:text-dark-400"
              >{{ t('home.providers.soon') }}</span
            >
          </div>
        </div>
      </div>

      <!-- ===== 定价方案 ===== -->
      <section id="pricing" class="mx-auto mt-8 max-w-6xl scroll-mt-24">
        <div class="mb-10 text-center">
          <h2 class="mb-3 text-3xl font-bold text-gray-900 dark:text-white">按量付费，按需使用</h2>
          <p class="text-sm text-gray-600 dark:text-dark-400">
            1 RMB = 1 USD，使用官方原生模型，余额永不过期
          </p>
        </div>
        <div class="grid gap-6 md:grid-cols-3">
          <!-- PAYGO -->
          <div class="card card-hover relative flex flex-col p-6">
            <span class="absolute right-4 top-4 rounded-full bg-primary-100 px-2.5 py-0.5 text-[11px] font-medium text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">推荐</span>
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">PAYGO 按量付费</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">充值即用 · 永不过期</p>
            <ul class="mt-5 flex-1 space-y-2.5 text-sm text-gray-600 dark:text-dark-300">
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />充值金额获得等价人民币额度</li>
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />按实际使用量计费</li>
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />余额永不过期</li>
            </ul>
            <router-link :to="isAuthenticated ? dashboardPath : '/register'" class="btn btn-primary mt-6">立即充值</router-link>
          </div>
          <!-- Claude -->
          <div class="card card-hover flex flex-col p-6">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">Claude 按需付费</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">1:1（RMB:USD） · 无需订阅</p>
            <ul class="mt-5 flex-1 space-y-2.5 text-sm text-gray-600 dark:text-dark-300">
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />官方价格同步</li>
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />支持 Claude 全系列模型</li>
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />专为 Claude Code 优化</li>
            </ul>
            <router-link :to="isAuthenticated ? dashboardPath : '/register'" class="btn btn-secondary mt-6">立即开始</router-link>
          </div>
          <!-- ChatGPT -->
          <div class="card card-hover flex flex-col p-6">
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">ChatGPT 按需付费</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">1:1（RMB:USD） · 灵活计费</p>
            <ul class="mt-5 flex-1 space-y-2.5 text-sm text-gray-600 dark:text-dark-300">
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />官方价格同步</li>
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />支持 GPT 全系列模型</li>
              <li class="flex items-start gap-2"><Icon name="check" size="sm" class="mt-0.5 text-primary-500" />专为 Codex 优化</li>
            </ul>
            <router-link :to="isAuthenticated ? dashboardPath : '/register'" class="btn btn-secondary mt-6">立即开始</router-link>
          </div>
        </div>
      </section>

      <!-- ===== 使用价值 ===== -->
      <section class="mx-auto mt-20 max-w-6xl">
        <div class="mb-10 text-center">
          <h2 class="mb-3 text-3xl font-bold text-gray-900 dark:text-white">释放你的编程潜能</h2>
          <p class="text-sm text-gray-600 dark:text-dark-400">稳定、专业的 AI API 基础设施</p>
        </div>
        <div class="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
          <div class="card p-6 text-center">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"><Icon name="cloud" size="lg" /></div>
            <h3 class="mb-2 font-semibold text-gray-900 dark:text-white">全球直连</h3>
            <p class="text-sm text-gray-500 dark:text-dark-400">无论身处何地都能稳定访问，减少等待与中断。</p>
          </div>
          <div class="card p-6 text-center">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"><Icon name="server" size="lg" /></div>
            <h3 class="mb-2 font-semibold text-gray-900 dark:text-white">高可用架构</h3>
            <p class="text-sm text-gray-500 dark:text-dark-400">分布式设计与自动故障转移，关键时刻依然可用。</p>
          </div>
          <div class="card p-6 text-center">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"><Icon name="swap" size="lg" /></div>
            <h3 class="mb-2 font-semibold text-gray-900 dark:text-white">简单集成</h3>
            <p class="text-sm text-gray-500 dark:text-dark-400">只需修改 API 地址即可使用，无需重写业务逻辑。</p>
          </div>
          <div class="card p-6 text-center">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-xl bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-400"><Icon name="shield" size="lg" /></div>
            <h3 class="mb-2 font-semibold text-gray-900 dark:text-white">完整兼容</h3>
            <p class="text-sm text-gray-500 dark:text-dark-400">兼容官方 API 行为，保留主流模型常用能力。</p>
          </div>
        </div>
      </section>

      <!-- ===== FAQ ===== -->
      <section id="faq" class="mx-auto mt-20 max-w-3xl scroll-mt-24">
        <div class="mb-10 text-center">
          <h2 class="mb-3 text-3xl font-bold text-gray-900 dark:text-white">常见问题</h2>
        </div>
        <div class="space-y-3">
          <details class="card group p-5">
            <summary class="flex cursor-pointer list-none items-center justify-between font-medium text-gray-900 dark:text-white">支持哪些模型？<Icon name="arrowRight" size="sm" class="text-gray-400 transition-transform group-open:rotate-90" /></summary>
            <p class="mt-3 text-sm text-gray-600 dark:text-dark-400">支持 Claude、ChatGPT、Gemini 等主流模型全系列，兼容 Claude Code、Codex、Gemini CLI 等主流工具。</p>
          </details>
          <details class="card group p-5">
            <summary class="flex cursor-pointer list-none items-center justify-between font-medium text-gray-900 dark:text-white">额度会过期吗？<Icon name="arrowRight" size="sm" class="text-gray-400 transition-transform group-open:rotate-90" /></summary>
            <p class="mt-3 text-sm text-gray-600 dark:text-dark-400">不会。充值获得的额度永不过期，按实际使用量扣费。</p>
          </details>
          <details class="card group p-5">
            <summary class="flex cursor-pointer list-none items-center justify-between font-medium text-gray-900 dark:text-white">如何接入？<Icon name="arrowRight" size="sm" class="text-gray-400 transition-transform group-open:rotate-90" /></summary>
            <p class="mt-3 text-sm text-gray-600 dark:text-dark-400">注册账号、充值、生成 API Key，把客户端的 API 地址改成本站地址即可，无需改动业务代码。</p>
          </details>
          <details class="card group p-5">
            <summary class="flex cursor-pointer list-none items-center justify-between font-medium text-gray-900 dark:text-white">支持哪些支付方式？<Icon name="arrowRight" size="sm" class="text-gray-400 transition-transform group-open:rotate-90" /></summary>
            <p class="mt-3 text-sm text-gray-600 dark:text-dark-400">支持支付宝、微信支付与 Stripe 等多种支付方式，自助充值实时到账。</p>
          </details>
        </div>
      </section>

      <!-- ===== 底部 CTA ===== -->
      <section class="mx-auto mt-20 max-w-5xl">
        <div class="overflow-hidden rounded-3xl bg-gradient-to-br from-primary-600 to-primary-800 px-8 py-14 text-center shadow-xl">
          <h2 class="mb-3 text-3xl font-bold text-white">准备好提升你的 AI 应用了吗？</h2>
          <p class="mb-8 text-primary-100">加入开发者行列，用 {{ siteName }} 稳定专业的基础设施构建 AI 的未来。</p>
          <router-link :to="isAuthenticated ? dashboardPath : '/register'" class="inline-flex items-center gap-2 rounded-xl bg-white px-8 py-3 text-base font-semibold text-primary-700 shadow-lg transition-transform hover:scale-[1.02]">
            {{ isAuthenticated ? t('home.goToDashboard') : '创建免费账号' }}
            <Icon name="arrowRight" size="md" />
          </router-link>
        </div>
      </section>
    </main>

    <!-- Footer -->
    <footer class="relative z-10 border-t border-gray-200/50 px-6 py-8 dark:border-dark-800/50">
      <div
        class="mx-auto flex max-w-6xl flex-col items-center justify-center gap-4 text-center sm:flex-row sm:text-left"
      >
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
          >
            {{ t('home.docs') }}
          </a>
          <a
            v-if="githubUrl"
            :href="githubUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="text-sm text-gray-500 transition-colors hover:text-gray-700 dark:text-dark-400 dark:hover:text-white"
          >
            GitHub
          </a>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import BrandIcon from '@/components/icons/BrandIcon.vue'

const { t } = useI18n()

const authStore = useAuthStore()
const appStore = useAppStore()

// Site settings - directly from appStore (already initialized from injected config)
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'No Wind API')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')

// Check if homeContent is a URL (for iframe display)
const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

// Theme
const isDark = ref(document.documentElement.classList.contains('dark'))

// GitHub URL（已去除原版链接，留空则页脚不显示）
const githubUrl = ''

// Auth state
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => isAdmin.value ? '/admin/dashboard' : '/dashboard')
const userInitial = computed(() => {
  const user = authStore.user
  if (!user || !user.email) return ''
  return user.email.charAt(0).toUpperCase()
})

// Current year for footer
const currentYear = computed(() => new Date().getFullYear())

// Toggle theme
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

// Initialize theme（默认浅色，贴合参考站点；仅手动选过 dark 才用暗色）
function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (savedTheme === 'dark' || !savedTheme) {
    isDark.value = true
    document.documentElement.classList.add('dark')
  }
}

// ==================== Canvas 粒子动画 ====================

const homeCanvasRef = ref<HTMLCanvasElement | null>(null)
let homeAnimationId = 0

interface HomeParticle {
  x: number; y: number; vx: number; vy: number; radius: number; opacity: number
}

function initHomeParticles() {
  const canvas = homeCanvasRef.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return

  const dpr = window.devicePixelRatio || 1
  const resize = () => {
    canvas.width = window.innerWidth * dpr
    canvas.height = window.innerHeight * dpr
    canvas.style.width = window.innerWidth + 'px'
    canvas.style.height = window.innerHeight + 'px'
    ctx.scale(dpr, dpr)
  }
  resize()
  window.addEventListener('resize', resize)

  const count = Math.min(100, Math.floor(window.innerWidth / 12))
  const particles: HomeParticle[] = []
  const maxDist = 160
  const color = { r: 14, g: 165, b: 233 }

  for (let i = 0; i < count; i++) {
    particles.push({
      x: Math.random() * window.innerWidth,
      y: Math.random() * window.innerHeight,
      vx: (Math.random() - 0.5) * 0.4,
      vy: (Math.random() - 0.5) * 0.4,
      radius: Math.random() * 1.8 + 0.3,
      opacity: Math.random() * 0.5 + 0.15,
    })
  }

  function draw() {
    if (!ctx) return
    const w = window.innerWidth, h = window.innerHeight
    ctx.clearRect(0, 0, w, h)
    for (const p of particles) {
      p.x += p.vx; p.y += p.vy
      if (p.x < 0 || p.x > w) p.vx *= -1
      if (p.y < 0 || p.y > h) p.vy *= -1
    }
    for (let i = 0; i < particles.length; i++) {
      for (let j = i + 1; j < particles.length; j++) {
        const dx = particles[i].x - particles[j].x, dy = particles[i].y - particles[j].y
        const dist = Math.sqrt(dx * dx + dy * dy)
        if (dist < maxDist) {
          ctx.beginPath()
          ctx.strokeStyle = `rgba(${color.r},${color.g},${color.b},${(1 - dist / maxDist) * 0.12})`
          ctx.lineWidth = 0.5
          ctx.moveTo(particles[i].x, particles[i].y)
          ctx.lineTo(particles[j].x, particles[j].y)
          ctx.stroke()
        }
      }
    }
    for (const p of particles) {
      ctx.beginPath()
      ctx.arc(p.x, p.y, p.radius, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(${color.r},${color.g},${color.b},${p.opacity})`
      ctx.fill()
    }
    homeAnimationId = requestAnimationFrame(draw)
  }
  draw()
}

onMounted(() => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
  // 启动粒子动画
  initHomeParticles()
})

onUnmounted(() => {
  cancelAnimationFrame(homeAnimationId)
})
</script>

<style scoped>
/* Terminal Container */
.terminal-container {
  position: relative;
  display: inline-block;
}

/* Terminal Window */
.terminal-window {
  width: 420px;
  background: linear-gradient(145deg, #1e293b 0%, #0f172a 100%);
  border-radius: 14px;
  box-shadow:
    0 25px 50px -12px rgba(0, 0, 0, 0.4),
    0 0 0 1px rgba(255, 255, 255, 0.1),
    inset 0 1px 0 rgba(255, 255, 255, 0.1);
  overflow: hidden;
  transform: perspective(1000px) rotateX(2deg) rotateY(-2deg);
  transition: transform 0.3s ease;
}

.terminal-window:hover {
  transform: perspective(1000px) rotateX(0deg) rotateY(0deg) translateY(-4px);
}

/* Terminal Header */
.terminal-header {
  display: flex;
  align-items: center;
  padding: 12px 16px;
  background: rgba(30, 41, 59, 0.8);
  border-bottom: 1px solid rgba(255, 255, 255, 0.05);
}

.terminal-buttons {
  display: flex;
  gap: 8px;
}

.terminal-buttons span {
  width: 12px;
  height: 12px;
  border-radius: 50%;
}

.btn-close {
  background: #ef4444;
}
.btn-minimize {
  background: #eab308;
}
.btn-maximize {
  background: #22c55e;
}

.terminal-title {
  flex: 1;
  text-align: center;
  font-size: 12px;
  font-family: ui-monospace, monospace;
  color: #64748b;
  margin-right: 52px;
}

/* Terminal Body */
.terminal-body {
  padding: 20px 24px;
  font-family: ui-monospace, 'Fira Code', monospace;
  font-size: 14px;
  line-height: 2;
}

.code-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  opacity: 0;
  animation: line-appear 0.5s ease forwards;
}

.line-1 {
  animation-delay: 0.3s;
}
.line-2 {
  animation-delay: 1s;
}
.line-3 {
  animation-delay: 1.8s;
}
.line-4 {
  animation-delay: 2.5s;
}

@keyframes line-appear {
  from {
    opacity: 0;
    transform: translateY(5px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.code-prompt {
  color: #22c55e;
  font-weight: bold;
}
.code-cmd {
  color: #38bdf8;
}
.code-flag {
  color: #a78bfa;
}
.code-url {
  color: #38bdf8;
}
.code-comment {
  color: #64748b;
  font-style: italic;
}
.code-success {
  color: #22c55e;
  background: rgba(34, 197, 94, 0.15);
  padding: 2px 8px;
  border-radius: 4px;
  font-weight: 600;
}
.code-response {
  color: #fbbf24;
}

/* Blinking Cursor */
.cursor {
  display: inline-block;
  width: 8px;
  height: 16px;
  background: #22c55e;
  animation: blink 1s step-end infinite;
}

@keyframes blink {
  0%,
  50% {
    opacity: 1;
  }
  51%,
  100% {
    opacity: 0;
  }
}

/* Dark mode adjustments */
:deep(.dark) .terminal-window {
  box-shadow:
    0 25px 50px -12px rgba(0, 0, 0, 0.6),
    0 0 0 1px rgba(14, 165, 233, 0.30),
    0 0 40px rgba(14, 165, 233, 0.15),
    inset 0 1px 0 rgba(255, 255, 255, 0.1);
}

/* 慢脉冲动画 */
.animate-pulse-slow {
  animation: pulse-slow 6s ease-in-out infinite;
}

@keyframes pulse-slow {
  0%, 100% { opacity: 0.4; transform: scale(1); }
  50% { opacity: 0.7; transform: scale(1.05); }
}
</style>
