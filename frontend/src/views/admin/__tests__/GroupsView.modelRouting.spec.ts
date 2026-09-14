import { defineComponent, h } from "vue";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";

import type { AdminGroup } from "@/types";
import GroupsView from "../GroupsView.vue";

const {
  listGroups,
  getAllGroups,
  getModelsListCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  listCompositeRoutes,
  updateGroup,
  getAccountByID,
  listPools,
  showError,
  showSuccess,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelsListCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  listCompositeRoutes: vi.fn(),
  updateGroup: vi.fn(),
  getAccountByID: vi.fn(),
  listPools: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock("@/api/admin", () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: getAllGroups,
      getModelsListCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      listCompositeRoutes,
      createCompositeRoute: vi.fn(),
      updateCompositeRoute: vi.fn(),
      deleteCompositeRoute: vi.fn(),
      previewCompositeRoute: vi.fn(),
      create: vi.fn(),
      update: updateGroup,
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: {
      list: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 }),
      getById: getAccountByID,
    },
  },
}));

vi.mock("@/api/admin/accountPools", () => ({
  accountPoolsAPI: { list: listPools },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({ showError, showSuccess }),
}));

vi.mock("@/stores/onboarding", () => ({
  useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() }),
}));

vi.mock("@/composables/useExecutionNodeAdminAccess", () => ({
  useExecutionNodeAdminAccess: () => ({
    executionNodeStatus: { value: null },
    executionNodeAccessLoading: { value: false },
    sharedWriteAllowed: { value: true },
    sharedReadOnly: { value: false },
    loadExecutionNodeAdminAccess: vi.fn().mockResolvedValue(undefined),
  }),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) };
});

const group = {
  id: 140,
  name: "ChatGPT Pro 20x",
  platform: "openai",
  status: "active",
  subscription_type: "standard",
  rate_multiplier: 0.25,
  rpm_limit: 0,
  is_exclusive: false,
  model_routing_enabled: true,
  model_routing: { "gpt-5.6-luna": [101] },
  model_routing_pools: { "gpt-5.6-luna": [7] },
  supported_model_scopes: [],
  sort_order: 0,
  created_at: "2026-09-14T00:00:00Z",
  updated_at: "2026-09-14T00:00:00Z",
} as AdminGroup;

const BaseDialogStub = defineComponent({
  props: ["show"],
  template: '<section v-if="show"><slot /><slot name="footer" /></section>',
});

const ReasoningEffortPolicyStub = defineComponent({
  setup(_, { expose }) {
    expose({ validate: () => true, resetValidation: () => undefined });
    return () => h("div");
  },
});

describe("GroupsView OpenAI model routing preferences", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(
      "group-hidden-columns",
      JSON.stringify(["billing_type", "usage", "capacity"]),
    );
    vi.clearAllMocks();
    listGroups.mockResolvedValue({ items: [group], total: 1, page: 1, page_size: 20, pages: 1 });
    getAllGroups.mockResolvedValue([]);
    getModelsListCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getLiveCapability.mockResolvedValue({ supported: false });
    listCompositeRoutes.mockResolvedValue([]);
    listPools.mockResolvedValue({
      items: [
        { id: 7, name: "Plus 号池", proxy_id: null, account_ids: [101], account_count: 1 },
        { id: 8, name: "Pro 号池", proxy_id: null, account_ids: [102], account_count: 1 },
      ],
    });
    getAccountByID.mockResolvedValue({ id: 101, name: "plus1" });
    updateGroup.mockResolvedValue(group);
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("loads account and pool selections and persists multiple preferred pools", async () => {
    const wrapper = mount(GroupsView, {
      global: {
        stubs: {
          AppLayout: { template: "<main><slot /></main>" },
          TablePageLayout: {
            template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>',
          },
          DataTable: true,
          Pagination: true,
          BaseDialog: BaseDialogStub,
          ConfirmDialog: true,
          EmptyState: true,
          Select: true,
          PlatformIcon: true,
          Icon: true,
          GroupCapacityBadge: true,
          GroupRateMultipliersModal: true,
          GroupRPMOverridesModal: true,
          UserAccountAllowlistDialog: true,
          ReasoningEffortPolicyFields: ReasoningEffortPolicyStub,
          PricingEntryCard: true,
          VueDraggable: { template: "<div><slot /></div>" },
        },
      },
    });
    await flushPromises();

    const state = (wrapper.vm as unknown as {
      $: {
        setupState: {
          handleEdit: (value: AdminGroup) => Promise<void>;
          handleUpdateGroup: () => Promise<void>;
          editModelRoutingRules: Array<unknown>;
        };
      };
    }).$.setupState;
    await state.handleEdit(group);
    await flushPromises();

    expect(listPools).toHaveBeenCalledTimes(1);
    expect(getAccountByID).toHaveBeenCalledWith(101);
    const poolInputs = wrapper.findAll<HTMLInputElement>(
      '[data-test="edit-model-routing-pools"] input[type="checkbox"]',
    );
    expect(poolInputs).toHaveLength(2);
    expect(poolInputs[0].element.checked).toBe(true);
    await poolInputs[1].setValue(true);

    await state.handleUpdateGroup();
    await flushPromises();

    expect(showError).not.toHaveBeenCalled();
    expect(updateGroup).toHaveBeenCalledWith(
      140,
      expect.objectContaining({
        model_routing_enabled: true,
        model_routing: { "gpt-5.6-luna": [101] },
        model_routing_pools: { "gpt-5.6-luna": [7, 8] },
      }),
    );

		await state.handleEdit(group);
		await flushPromises();
		state.editModelRoutingRules.splice(0);
		await state.handleUpdateGroup();
		await flushPromises();

		expect(updateGroup).toHaveBeenLastCalledWith(
			140,
			expect.objectContaining({
				model_routing: {},
				model_routing_pools: {},
			}),
		);
    wrapper.unmount();
  });
});
