<template>
  <div class="space-y-6">
    <!-- Banner -->
    <div class="bg-orange-50/50 border border-orange-200 rounded-2xl p-6 flex items-center justify-between dark:bg-orange-900/10 dark:border-orange-500/20">
      <div>
        <div class="flex items-center space-x-2 mb-2">
          <span class="bg-orange-100 text-orange-700 text-xs font-semibold px-2.5 py-0.5 rounded-full border border-orange-200 dark:bg-orange-500/20 dark:text-orange-400 dark:border-orange-500/30">
            {{ t('payment.topup.promoBadge', '限时加赠') }}
          </span>
        </div>
        <h2 class="text-xl font-bold text-gray-900 dark:text-white mb-1">
          {{ t('payment.topup.promoTitle', '多充多送，充值越高赠送越多') }}
        </h2>
        <p class="text-gray-500 dark:text-gray-400 text-sm">
          {{ t('payment.topup.promoDesc', '选择更高档位可获得额外赠送余额，最高赠送 $49.90。') }}
        </p>
      </div>
    </div>

    <!-- Pricing Cards -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-6">
      <div
        v-for="plan in TOPUP_TIERS"
        :key="plan.id"
        class="bg-white dark:bg-dark-800 rounded-2xl p-6 shadow-sm border border-gray-200 dark:border-dark-700 hover:border-primary-500 transition-all flex flex-col relative overflow-hidden group cursor-pointer"
        @click="$emit('select', plan)"
      >
        <div v-if="getLocaleData(plan).tag" :class="[
          'absolute top-4 left-4 text-[10px] font-bold px-2 py-1 rounded-full text-white',
          plan.tagColor ? plan.tagColor : 'bg-orange-200 text-orange-800 dark:bg-orange-500/30 dark:text-orange-300'
        ]">
          {{ getLocaleData(plan).tag }}
        </div>
        
        <div class="mt-8 mb-4">
          <h3 class="text-lg font-bold text-gray-900 dark:text-white">{{ getLocaleData(plan).title }}</h3>
          <p class="text-gray-500 dark:text-gray-400 text-xs mt-1">{{ getLocaleData(plan).subtitle }}</p>
        </div>
        
        <div class="mb-6">
          <div class="flex items-baseline">
            <span class="text-gray-400 font-semibold mr-1">¥</span>
            <span class="text-4xl font-extrabold text-gray-900 dark:text-white tracking-tight">{{ plan.priceRMB }}</span>
            <span v-if="plan.bonusUSD > 0" class="ml-2 bg-orange-100 text-orange-700 text-xs font-bold px-2 py-1 rounded-full dark:bg-orange-500/20 dark:text-orange-400">
              +¥{{ plan.bonusUSD }}
            </span>
          </div>
          <div class="text-xs text-gray-500 dark:text-gray-400 mt-2">
            {{ t('payment.topup.getCredit', '获得') }} 
            <span :class="{'line-through opacity-50': plan.bonusUSD > 0}">¥{{ plan.creditUSD.toFixed(2) }}</span>
            <span v-if="plan.bonusUSD > 0" class="text-orange-600 dark:text-orange-400 font-semibold ml-1">¥{{ (plan.creditUSD + plan.bonusUSD).toFixed(2) }}</span>
            {{ t('payment.topup.creditUnit', '额度') }}
          </div>
        </div>

        <div class="space-y-3 mb-8 flex-grow">
          <div v-for="(feature, idx) in getLocaleData(plan).features" :key="idx" class="flex items-start">
            <Icon name="check" class="w-4 h-4 text-primary-500 mr-2 shrink-0 mt-0.5" />
            <span class="text-sm text-gray-600 dark:text-gray-300">{{ feature }}</span>
          </div>
        </div>

        <button 
          :class="['w-full rounded-xl py-3 font-semibold transition-all group-hover:shadow-lg', plan.buttonClass || 'bg-primary-600 hover:bg-primary-700 text-white shadow-md']"
          @click.stop="$emit('select', plan)"
        >
          {{ t('payment.topup.rechargeNow', '立即充值') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { TOPUP_TIERS, type PricingTier } from '@/config/pricingTiers'
import Icon from '@/components/icons/Icon.vue'

const { t, locale } = useI18n()

defineEmits<{
  (e: 'select', plan: PricingTier): void
}>()

const getLocaleData = (plan: PricingTier) => {
  const currentLocale = String(locale.value || 'zh')
  return plan.locales[currentLocale] || plan.locales['zh']
}
</script>
