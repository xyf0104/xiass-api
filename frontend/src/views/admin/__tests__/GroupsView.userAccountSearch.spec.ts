import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";

import type { Account, AdminGroup, AdminUser, PaginatedResponse } from "@/types";
import type { UserGroupAccountRuntimeUser } from "@/api/admin/groups";
import GroupsView from "../GroupsView.vue";

const {
  routeQuery,
  listGroups,
  getAllGroups,
  getModelsListCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  getUserAccountRuntime,
  getUserAccountAllowlist,
  updateUserAccountAllowlist,
  clearUserAccountAllowlist,
  listAccounts,
  listUsers,
  showError,
  showSuccess,
  isCurrentStep,
  nextStep,
} = vi.hoisted(() => ({
  routeQuery: {} as Record<string, string>,
  listGroups: vi.fn(),
  getAllGroups: vi.fn(),
  getModelsListCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  getUserAccountRuntime: vi.fn(),
  getUserAccountAllowlist: vi.fn(),
  updateUserAccountAllowlist: vi.fn(),
  clearUserAccountAllowlist: vi.fn(),
  listAccounts: vi.fn(),
  listUsers: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isCurrentStep: vi.fn(),
  nextStep: vi.fn(),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: routeQuery }),
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
      getUserAccountRuntime,
      getUserAccountAllowlist,
      getById: vi.fn(),
      listCompositeRoutes: vi.fn().mockResolvedValue([]),
      createCompositeRoute: vi.fn(),
      updateCompositeRoute: vi.fn(),
      deleteCompositeRoute: vi.fn(),
      previewCompositeRoute: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      delete: vi.fn(),
      updateSortOrder: vi.fn(),
      updateUserAccountAllowlist,
      clearUserAccountAllowlist,
    },
    accounts: {
      list: listAccounts,
      getById: vi.fn(),
    },
    users: {
      list: listUsers,
    },
  },
}));

vi.mock("@/stores/app", () => ({
  useAppStore: () => ({ showError, showSuccess }),
}));

vi.mock("@/stores/onboarding", () => ({
  useOnboardingStore: () => ({ isCurrentStep, nextStep }),
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

vi.mock("vue-i18n", async () => {
  const actual = await vi.importActual<typeof import("vue-i18n")>("vue-i18n");
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  };
});

const group = {
  id: 10,
  name: "OpenAI Main",
  platform: "openai",
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: "active",
  subscription_type: "standard",
  supported_model_scopes: [],
  sort_order: 10,
  created_at: "2026-08-20T00:00:00Z",
  updated_at: "2026-08-20T00:00:00Z",
} as AdminGroup;

const account = {
  id: 101,
  name: "OAuth Account A",
  platform: "openai",
  status: "active",
  schedulable: true,
} as Account;

const adminUser = (id: number, username: string): AdminUser => ({
  id,
  username,
  email: `${username}@example.com`,
  role: "user",
  balance: 100,
  concurrency: 5,
  status: "active",
  allowed_groups: null,
  balance_notify_enabled: false,
  balance_notify_threshold: null,
  balance_notify_extra_emails: [],
  notes: "",
  created_at: "2026-08-20T00:00:00Z",
  updated_at: "2026-08-20T00:00:00Z",
});

const runtimeUser = (
  id: number,
  username: string,
  currentConcurrency: number,
  activeAccountIDs: number[],
): UserGroupAccountRuntimeUser => ({
  ...adminUser(id, username),
  current_concurrency: currentConcurrency,
  active_account_ids: activeAccountIDs,
});

const userPage = (items: AdminUser[]): PaginatedResponse<AdminUser> => ({
  items,
  total: items.length,
  page: 1,
  page_size: 50,
  pages: items.length > 0 ? 1 : 0,
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const AppLayoutStub = { template: "<div><slot /></div>" };
const TablePageLayoutStub = {
  template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
};
const BaseDialogStub = {
  props: ["show"],
  emits: ["close"],
  template: '<section v-if="show"><slot /><slot name="footer" /></section>',
};
const UserAccountAllowlistDialogStub = {
  props: ["show", "restricted", "candidates"],
  emits: ["close", "save", "restore"],
  template: `
    <div
      v-if="show"
      data-test="allowlist-dialog-stub"
      :data-restricted="String(restricted)"
      :data-candidate-count="String(candidates.length)"
    >
      <button data-test="allowlist-save-empty-stub" @click="$emit('save', [])">save empty</button>
    </div>
  `,
};

let wrapper: ReturnType<typeof mount> | null = null;

const mountView = async () => {
  wrapper = mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
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
        UserAccountAllowlistDialog: UserAccountAllowlistDialogStub,
        ReasoningEffortPolicyFields: true,
        PricingEntryCard: true,
        VueDraggable: { template: "<div><slot /></div>" },
      },
    },
  });
  await flushPromises();
  return wrapper;
};

const searchFor = async (query: string) => {
  await wrapper!.get('[data-test="runtime-user-search"]').setValue(query);
};

describe("GroupsView group user account search", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    localStorage.clear();
    Object.assign(routeQuery, {
      user_account_allowlist_group: "10",
      user_account_allowlist_account: "101",
    });

    listGroups.mockResolvedValue({
      items: [group],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    });
    getAllGroups.mockResolvedValue([]);
    getModelsListCandidates.mockResolvedValue([]);
    getUsageSummary.mockResolvedValue([]);
    getCapacitySummary.mockResolvedValue([]);
    getLiveCapability.mockResolvedValue({ supported: false });
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 });
    getUserAccountRuntime.mockResolvedValue({
      users: [
        runtimeUser(1, "selected-live", 2, [101]),
        runtimeUser(3, "other-live", 4, [102]),
      ],
      accounts: [account],
    });
    getUserAccountAllowlist.mockResolvedValue({ restricted: false, account_ids: [], candidates: [] });
    updateUserAccountAllowlist.mockResolvedValue({ restricted: true, account_ids: [] });
    clearUserAccountAllowlist.mockResolvedValue(undefined);
    listUsers.mockResolvedValue(userPage([]));
    isCurrentStep.mockReturnValue(false);
  });

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
    vi.clearAllTimers();
    vi.useRealTimers();
    localStorage.clear();
    for (const key of Object.keys(routeQuery)) delete routeQuery[key];
  });

  it("merges idle search results with account-filtered live users and preserves live data", async () => {
    listUsers.mockResolvedValueOnce(userPage([
      adminUser(1, "stale-selected-live"),
      adminUser(2, "idle-user"),
      adminUser(3, "stale-other-live"),
    ]));
    await mountView();

    expect(wrapper!.find('[data-test="runtime-user-row-1"]').exists()).toBe(true);
    expect(wrapper!.find('[data-test="runtime-user-row-3"]').exists()).toBe(false);

    await searchFor("user");
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();

    expect(listUsers).toHaveBeenCalledWith(
      1,
      50,
      {
        status: "active",
        search: "user",
        api_key_group_id: 10,
      },
      { signal: expect.any(AbortSignal) },
    );
    expect(wrapper!.findAll('[data-test="runtime-user-row-1"]')).toHaveLength(1);
    expect(wrapper!.get('[data-test="runtime-user-row-1"]').text()).toContain("selected-live");
    expect(wrapper!.get('[data-test="runtime-user-row-1"]').text()).toContain("2");
    expect(wrapper!.get('[data-test="runtime-user-row-2"]').text()).toContain("idle-user");
    expect(wrapper!.get('[data-test="runtime-user-row-2"]').text()).toContain("0");
    expect(wrapper!.get('[data-test="runtime-user-row-3"]').text()).toContain("other-live");
    expect(wrapper!.get('[data-test="runtime-user-row-3"]').text()).toContain("4");

    await wrapper!.get('[data-test="runtime-user-row-2"] button').trigger("click");
    await flushPromises();
    expect(getUserAccountAllowlist).toHaveBeenCalledWith(10, 2);

    await wrapper!.get('[data-test="runtime-user-search-clear"]').trigger("click");
    expect(wrapper!.find('[data-test="runtime-user-row-1"]').exists()).toBe(true);
    expect(wrapper!.find('[data-test="runtime-user-row-2"]').exists()).toBe(false);
    expect(wrapper!.find('[data-test="runtime-user-row-3"]').exists()).toBe(false);
  });

  it("debounces input and searches only the latest query", async () => {
    await mountView();

    await searchFor("i");
    await vi.advanceTimersByTimeAsync(150);
    await searchFor("idle");
    await vi.advanceTimersByTimeAsync(299);
    expect(listUsers).not.toHaveBeenCalled();

    await vi.advanceTimersByTimeAsync(1);
    await flushPromises();
    expect(listUsers).toHaveBeenCalledTimes(1);
    expect(listUsers.mock.calls[0][2]).toEqual(expect.objectContaining({ search: "idle" }));
  });

  it("opens the same account selector when the user's concurrency number is clicked", async () => {
    await mountView();

    await wrapper!.get('[data-test="runtime-user-concurrency-1"]').trigger("click");
    await flushPromises();

    expect(getUserAccountAllowlist).toHaveBeenCalledWith(10, 1);
    expect(wrapper!.get('[data-test="allowlist-dialog-stub"]').exists()).toBe(true);
  });

  it("keeps an active account visible after it becomes unavailable", async () => {
    delete routeQuery.user_account_allowlist_account;
    getUserAccountRuntime.mockResolvedValueOnce({
      users: [runtimeUser(1, "selected-live", 1, [103])],
      accounts: [
        { ...account, current_concurrency: 0, available: true },
        {
          ...account,
          id: 103,
          name: "disabled-while-running",
          current_concurrency: 1,
          available: false,
        },
      ],
    });

    await mountView();

    expect(wrapper!.get('[data-test="runtime-user-row-1"]').text()).toContain("disabled-while-running");
  });

  it("ignores stale responses and aborts an in-flight search when the dialog closes", async () => {
    const first = deferred<PaginatedResponse<AdminUser>>();
    const second = deferred<PaginatedResponse<AdminUser>>();
    const third = deferred<PaginatedResponse<AdminUser>>();
    listUsers
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise)
      .mockReturnValueOnce(third.promise);
    await mountView();

    await searchFor("first");
    await vi.advanceTimersByTimeAsync(300);
    const firstSignal = listUsers.mock.calls[0][3].signal as AbortSignal;

    await searchFor("second");
    expect(firstSignal.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(300);
    second.resolve(userPage([adminUser(22, "second-result")]));
    await flushPromises();
    expect(wrapper!.find('[data-test="runtime-user-row-22"]').exists()).toBe(true);

    first.resolve(userPage([adminUser(11, "stale-first-result")]));
    await flushPromises();
    expect(wrapper!.find('[data-test="runtime-user-row-11"]').exists()).toBe(false);

    await searchFor("third");
    await vi.advanceTimersByTimeAsync(300);
    const thirdSignal = listUsers.mock.calls[2][3].signal as AbortSignal;
    await wrapper!.get('[data-test="runtime-user-dialog-close"]').trigger("click");
    expect(thirdSignal.aborted).toBe(true);
    expect(wrapper!.find('[data-test="runtime-user-search"]').exists()).toBe(false);

    third.resolve(userPage([adminUser(33, "late-third-result")]));
    await flushPromises();
    expect(wrapper!.find('[data-test="runtime-user-row-33"]').exists()).toBe(false);
  });

  it("reports search failures without closing the runtime dialog", async () => {
    listUsers.mockRejectedValueOnce({});
    await mountView();

    await searchFor("idle");
    await vi.advanceTimersByTimeAsync(300);
    await flushPromises();

    expect(showError).toHaveBeenCalledWith(
      "admin.groups.userAccountAllowlist.loadFailed",
    );
    expect(wrapper!.find('[data-test="runtime-user-search"]').exists()).toBe(true);
    expect(wrapper!.find('[data-test="runtime-user-row-1"]').exists()).toBe(true);
  });

  it("preserves restricted empty-selection semantics and saves deny-all", async () => {
    getUserAccountAllowlist.mockResolvedValueOnce({
      restricted: true,
      account_ids: [],
      candidates: [{
        id: 101,
        name: "OAuth Account A",
        platform: "openai",
        type: "oauth",
        priority: 1,
        concurrency: 3,
        allowed: false,
        available: true,
      }],
    });
    await mountView();

    await wrapper!.get('[data-test="runtime-user-row-1"] button').trigger("click");
    await flushPromises();

    const dialog = wrapper!.get('[data-test="allowlist-dialog-stub"]');
    expect(dialog.attributes("data-restricted")).toBe("true");
    expect(dialog.attributes("data-candidate-count")).toBe("1");

    await wrapper!.get('[data-test="allowlist-save-empty-stub"]').trigger("click");
    await flushPromises();

    expect(updateUserAccountAllowlist).toHaveBeenCalledWith(10, 1, []);
  });

  it("keeps the open runtime dialog synchronized with the latest group concurrency snapshot", async () => {
    getUserAccountRuntime
      .mockResolvedValueOnce({
        users: [runtimeUser(1, "selected-live", 1, [101])],
        accounts: [{ ...account, current_concurrency: 1, available: true }],
      })
      .mockResolvedValueOnce({
        users: [runtimeUser(1, "selected-live", 4, [101])],
        accounts: [{ ...account, current_concurrency: 4, available: true }],
      });

    await mountView();

    expect(wrapper!.get('[data-test="runtime-user-concurrency-1"]').text()).toContain("1");
    expect(getUserAccountRuntime).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(5_000);
    await flushPromises();

    expect(getUserAccountRuntime).toHaveBeenCalledTimes(2);
    expect(wrapper!.get('[data-test="runtime-user-concurrency-1"]').text()).toContain("4");
  });
});
