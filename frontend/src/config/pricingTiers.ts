export interface PricingTier {
  id: string;
  priceRMB: number;
  creditUSD: number;
  bonusUSD: number;
  tagColor?: string;
  locales: {
    [key: string]: {
      title: string;
      subtitle: string;
      tag?: string;
      features: string[];
    };
  };
}

export const TOPUP_TIERS: PricingTier[] = [
  {
    id: "plan_1",
    priceRMB: 50,
    creditUSD: 50,
    bonusUSD: 0,
    locales: {
      zh: {
        title: "体验",
        subtitle: "适合初次体验",
        features: ["获得 $50 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Trial",
        subtitle: "For first-time users",
        features: ["Get $50 credit", "Never expires", "All models supported"],
      }
    }
  },
  {
    id: "plan_2",
    priceRMB: 100,
    creditUSD: 100,
    bonusUSD: 2.99,
    locales: {
      zh: {
        title: "标准",
        subtitle: "开发者常用",
        features: ["获得 $102.99 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Standard",
        subtitle: "For regular developers",
        features: ["Get $102.99 credit", "Never expires", "All models supported"],
      }
    }
  },
  {
    id: "plan_3",
    priceRMB: 500,
    creditUSD: 500,
    bonusUSD: 19.9,
    locales: {
      zh: {
        title: "进阶",
        subtitle: "高频使用",
        tag: "人气推荐",
        features: ["获得 $519.90 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Advanced",
        subtitle: "For frequent users",
        tag: "Popular",
        features: ["Get $519.90 credit", "Never expires", "All models supported"],
      }
    }
  },
  {
    id: "plan_4",
    priceRMB: 1000,
    creditUSD: 1000,
    bonusUSD: 49.9,
    tagColor: "bg-orange-600",
    locales: {
      zh: {
        title: "专业",
        subtitle: "专业开发者",
        tag: "最佳价值",
        features: ["获得 $1049.90 额度", "永不过期", "支持全部模型"],
      },
      en: {
        title: "Professional",
        subtitle: "For pro developers",
        tag: "Best Value",
        features: ["Get $1049.90 credit", "Never expires", "All models supported"],
      }
    }
  },
];
