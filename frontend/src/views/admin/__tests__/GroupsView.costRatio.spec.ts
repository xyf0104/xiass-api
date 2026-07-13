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
  createGroupAPI,
  updateGroupAPI,
  listAccounts,
  getAccountByID,
  showError,
  showSuccess,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelsListCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  createGroupAPI: vi.fn(),
  updateGroupAPI: vi.fn(),
  listAccounts: vi.fn(),
  getAccountByID: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}));

vi.mock("@/api/admin", () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      getAll: getAllGroups,
      getModelsListCandidates,
      getUsageSummary,
      getCapacitySummary,
      create: createGroupAPI,
      update: updateGroupAPI,
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
    },
    accounts: {
      list: listAccounts,
      getById: getAccountByID,
    },
  },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({ showError, showSuccess }),
}));

vi.mock("@/stores/onboarding", () => ({
  useOnboardingStore: () => ({ isCurrentStep, nextStep }),
}));

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  };
});

const testGroup = (overrides: Partial<AdminGroup> = {}): AdminGroup => ({
  id: 1,
  name: "Cost Ratio Group",
  description: null,
  platform: "anthropic",
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: "active",
  subscription_type: "standard",
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: "",
  require_oauth_only: false,
  require_privacy_set: false,
  cost_ratio: 0.2,
  created_at: "2026-07-01T00:00:00Z",
  updated_at: "2026-07-01T00:00:00Z",
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  sort_order: 10,
  ...overrides,
});

const AppLayoutStub = { template: "<div><slot /></div>" };
const TablePageLayoutStub = {
  template:
    '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
};
const DataTableStub = {
  props: ["data"],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-actions" :row="row" />
      </div>
    </div>
  `,
};
const BaseDialogStub = {
  props: ["show"],
  template: '<section v-if="show"><slot /><slot name="footer" /></section>',
};
const SelectStub = {
  props: ["modelValue", "options"],
  emits: ["update:modelValue", "change"],
  template: `
    <select
      :value="modelValue"
      @change="$emit('update:modelValue', $event.target.value); $emit('change')"
    >
      <option v-for="option in options" :key="String(option.value)" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
};

let wrapper: ReturnType<typeof mount> | null = null;

const mountView = async () => {
  wrapper = mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: { template: "<div><slot /></div>" },
      },
    },
  });
  await flushPromises();
  return wrapper;
};

const openEditDialog = async () => {
  const editButton = wrapper!
    .findAll("button")
    .find((button) => button.text().includes("common.edit"));
  expect(editButton).toBeTruthy();
  await editButton!.trigger("click");
  await flushPromises();
};

describe("admin GroupsView cost_ratio persistence payloads", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem(
      "group-hidden-columns",
      JSON.stringify(["billing_type", "usage", "capacity"]),
    );
    vi.clearAllMocks();

    listGroups.mockResolvedValue({
      items: [testGroup()],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    });
    getAllGroups.mockResolvedValue([]);
    getModelsListCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    createGroupAPI.mockResolvedValue(testGroup());
    updateGroupAPI.mockResolvedValue(testGroup());
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 });
    getAccountByID.mockResolvedValue(null);
    isCurrentStep.mockReturnValue(false);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    localStorage.clear();
  });

  it("sends cost_ratio when creating a group", async () => {
    await mountView();
    await wrapper!.get('[data-tour="groups-create-btn"]').trigger("click");
    await wrapper!.get('[data-tour="group-form-name"]').setValue("Created Ratio Group");
    await wrapper!.get('[data-test="create-cost-ratio"]').setValue("0.125");

    await wrapper!.get("#create-group-form").trigger("submit");
    await flushPromises();

    expect(createGroupAPI).toHaveBeenCalledWith(
      expect.objectContaining({ cost_ratio: 0.125 }),
    );
  });

  it("sends an updated cost_ratio", async () => {
    await mountView();
    await openEditDialog();
    await wrapper!.get('[data-test="edit-cost-ratio"]').setValue("0.075");

    await wrapper!.get("#edit-group-form").trigger("submit");
    await flushPromises();

    expect(updateGroupAPI).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ cost_ratio: 0.075 }),
    );
  });

  it("sends the negative sentinel when cost_ratio is cleared", async () => {
    await mountView();
    await openEditDialog();
    await wrapper!.get('[data-test="edit-cost-ratio"]').setValue("");

    await wrapper!.get("#edit-group-form").trigger("submit");
    await flushPromises();

    expect(updateGroupAPI).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ cost_ratio: -1 }),
    );
  });

  it("keeps zero as a legal cost_ratio value", async () => {
    await mountView();
    await openEditDialog();
    await wrapper!.get('[data-test="edit-cost-ratio"]').setValue("0");

    await wrapper!.get("#edit-group-form").trigger("submit");
    await flushPromises();

    expect(updateGroupAPI).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ cost_ratio: 0 }),
    );
  });
});
