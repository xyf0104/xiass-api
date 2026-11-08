import SwiftUI

enum AccountListFilter: String, CaseIterable, Identifiable {
    case all, normal, attention

    var id: String { rawValue }
    var title: String {
        switch self {
        case .all: return "全部"
        case .normal: return "正常"
        case .attention: return "需处理"
        }
    }
}

struct AccountsView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var accounts: [AdminAccount] = []
    @State private var searchText = ""
    @State private var platform = "all"
    @State private var filter: AccountListFilter
    @State private var isLoading = false
    @State private var showEditor = false
    @State private var error: ErrorMessage?

    init(initialFilter: AccountListFilter = .all) {
        _filter = State(initialValue: initialFilter)
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && accounts.isEmpty {
                    ProgressView("正在读取账号…")
                        .frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && accounts.isEmpty {
                    EmptyState(title: "暂无账号", systemImage: "person.crop.rectangle.stack", detail: "添加上游账号，或调整筛选条件。")
                }
                ForEach(platformSections, id: \.platform) { section in
                    HStack(spacing: 8) {
                        PlatformBadge(platform: section.platform)
                        Text("\(section.accounts.count) 个账号")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal, 4)
                    .padding(.top, 4)

                    AccountTable(accounts: section.accounts)
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .navigationTitle("上游账号")
        .searchable(text: $searchText, prompt: "搜索账号名称或备注")
        .appScreenStyle()
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Menu {
                    Section("平台分类") {
                        Button { platform = "all" } label: {
                            Label("全部平台", systemImage: platform == "all" ? "checkmark" : "circle")
                        }
                        ForEach(PlatformOption.allCases) { option in
                            Button { platform = option.rawValue } label: {
                                Label(option.title, systemImage: platform == option.rawValue ? "checkmark" : PlatformStyle.icon(for: option.rawValue))
                            }
                        }
                    }
                    Section("运行状态") {
                        ForEach(AccountListFilter.allCases) { item in
                            Button { filter = item } label: {
                                Label(item.title, systemImage: filter == item ? "checkmark" : "circle")
                            }
                        }
                    }
                } label: {
                    Image(systemName: "line.3.horizontal.decrease.circle")
                }
                .accessibilityLabel("平台筛选")
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "plus") }
                    .accessibilityLabel("添加账号")
            }
        }
        .sheet(isPresented: $showEditor) {
            AccountCreationHubView(onSaved: { Task { await load() } })
        }
        .task(id: "\(searchText)|\(platform)|\(filter.rawValue)") {
            do { try await Task.sleep(for: .milliseconds(250)) }
            catch { return }
            guard !Task.isCancelled else { return }
            await load()
        }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            var query = [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")]
            if platform != "all" { query.append(URLQueryItem(name: "platform", value: platform)) }
            if !searchText.trimmingCharacters(in: .whitespaces).isEmpty { query.append(URLQueryItem(name: "search", value: searchText)) }
            let page: Page<AdminAccount> = try await api.request(method: .get, path: "admin/accounts", query: query)
            guard !Task.isCancelled else { return }
            accounts = page.items.filter { account in
                switch filter {
                case .all:
                    return true
                case .normal:
                    return account.status == "active" && account.schedulable != false && account.errorMessage?.isEmpty != false
                case .attention:
                    return account.status == "error" || account.errorMessage?.isEmpty == false || account.rateLimitedAt != nil || account.overloadUntil != nil || account.schedulable == false
                }
            }
            if notify { feedback.success("账号列表已刷新", detail: "已同步 \(accounts.count) 个账号。") }
        } catch {
            // Typing another character cancels the obsolete request. It must
            // never turn into a visible "cancelled" error for the new query.
            guard !Task.isCancelled, !isExpectedCancellation(error) else { return }
            self.error = ErrorMessage(error, title: "无法读取账号")
        }
    }

    private var platformSections: [(platform: String, accounts: [AdminAccount])] {
        let grouped = Dictionary(grouping: accounts) { $0.platform.lowercased() }
        return grouped.keys.sorted(by: platformOrder).map { platform in
            let sorted = (grouped[platform] ?? []).sorted {
                let left = $0.rateMultiplier ?? 1
                let right = $1.rateMultiplier ?? 1
                return left == right ? $0.name.localizedStandardCompare($1.name) == .orderedAscending : left < right
            }
            return (platform, sorted)
        }
    }

    private func platformOrder(_ lhs: String, _ rhs: String) -> Bool {
        let order = ["openai", "anthropic", "gemini", "antigravity", "grok", "composite"]
        return (order.firstIndex(of: lhs) ?? order.count) < (order.firstIndex(of: rhs) ?? order.count)
    }

}

private struct AccountTable: View {
    let accounts: [AdminAccount]

    var body: some View {
        LazyVStack(spacing: 8) {
            ForEach(accounts) { account in
                NavigationLink { AccountDetailView(account: account) } label: {
                    AdminListRowSurface {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(alignment: .firstTextBaseline, spacing: 8) {
                                Text(account.name)
                                    .font(.subheadline.weight(.semibold))
                                    .lineLimit(1)
                                Spacer(minLength: 4)
                                StatusPill(text: account.healthLabel)
                                Image(systemName: "chevron.right")
                                    .font(.caption.weight(.bold))
                                    .foregroundStyle(.tertiary)
                            }
                            Text(account.displayEmail ?? "账号 #\(account.id)")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                            HStack(spacing: 10) {
                                CompactMetric(title: "类型", value: account.type, tint: .secondary)
                                CompactMetric(title: "并发", value: "\(account.currentConcurrency ?? 0)/\(account.concurrency ?? 0)")
                                CompactMetric(title: "倍率", value: DisplayFormat.decimal(account.rateMultiplier ?? 1))
                                CompactMetric(title: "优先级", value: String(account.priority ?? 0))
                            }
                            Text("最近使用：\(DisplayFormat.shortDate(account.lastUsedAt))")
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                                .lineLimit(1)
                        }
                    }
                }
                .buttonStyle(.plain)
            }
        }
    }
}

struct AccountDetailView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    @State private var current: AdminAccount
    @State private var isWorking = false
    @State private var message: ErrorMessage?
    @State private var showEditor = false
    @State private var showTest = false
    @State private var showReauthorization = false
    @State private var showDeleteConfirmation = false

    init(account: AdminAccount) {
        _current = State(initialValue: account)
    }

    var body: some View {
        List {
            Section("账号信息") {
                LabeledContent("平台", value: current.platform.uppercased())
                LabeledContent("凭证类型", value: current.type)
                LabeledContent("优先级", value: String(current.priority ?? 0))
                LabeledContent("并发", value: "\(current.currentConcurrency ?? 0) / \(current.concurrency ?? 0)")
                LabeledContent("费率倍率", value: DisplayFormat.decimal(current.rateMultiplier))
                LabeledContent("分组", value: current.groupIDs?.map(String.init).joined(separator: "、") ?? "未分组")
                if let email = current.displayEmail { LabeledContent("邮箱", value: email) }
                if let notes = current.notes, !notes.isEmpty { LabeledContent("备注", value: notes) }
            }

            Section("调度") {
                Toggle("允许调度", isOn: Binding(
                    get: { current.schedulable ?? true },
                    set: { setSchedulable($0) }
                ))
                .disabled(isWorking)
                LabeledContent("当前状态") { StatusPill(text: current.healthLabel) }
                if let error = current.errorMessage, !error.isEmpty {
                    Text(error).foregroundStyle(.red)
                }
            }

            Section("常用操作") {
                Button { showTest = true } label: {
                    Label("测试账号", systemImage: "bolt.horizontal.circle")
                }
                if reauthorizationPlatform != nil {
                    Button { showReauthorization = true } label: {
                        Label("重新授权", systemImage: "arrow.triangle.2.circlepath.circle")
                    }
                }
                Button { showEditor = true } label: {
                    Label("编辑账号与凭证", systemImage: "pencil")
                }
                Button { performAction("refresh", success: "已发起凭证刷新。") } label: {
                    Label("刷新凭证", systemImage: "arrow.triangle.2.circlepath")
                }
                Button { performAction("recover-state", success: "账号运行状态已恢复。") } label: {
                    Label("恢复运行状态", systemImage: "stethoscope")
                }
                if current.errorMessage?.isEmpty == false {
                    Button { performAction("clear-error", success: "账号错误状态已清除。") } label: {
                        Label("清除错误", systemImage: "xmark.circle")
                    }
                }
            }

            if let credentials = current.credentialsStatus, !credentials.isEmpty {
                Section("凭证状态") {
                    ForEach(credentials.keys.sorted(), id: \.self) { key in
                        LabeledContent(key, value: credentials[key] == true ? "已配置" : "缺失")
                    }
                }
            }

            Section {
                Button(role: .destructive) { showDeleteConfirmation = true } label: {
                    Label("删除账号", systemImage: "trash")
                }
            }
        }
        .navigationTitle(current.name)
        .navigationBarTitleDisplayMode(.inline)
        .appScreenStyle()
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "pencil") }
                    .accessibilityLabel("编辑账号")
                Button { Task { await reload(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新账号信息")
            }
        }
        .task { await reload() }
        .sheet(isPresented: $showEditor) {
            AccountEditorView(account: current, onSaved: { Task { await reload() } })
        }
        .sheet(isPresented: $showReauthorization) {
            if let platform = reauthorizationPlatform {
                NavigationStack {
                    OAuthAccountFlow(platform: platform, existingAccount: current, onSaved: { Task { await reload() } })
                }
            }
        }
        .sheet(isPresented: $showTest) { AccountTestSheet(account: current) }
        .confirmationDialog("确定删除此账号吗？", isPresented: $showDeleteConfirmation, titleVisibility: .visible) {
            Button("删除账号", role: .destructive) { deleteAccount() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("删除后无法恢复，已绑定的分组关系也会移除。")
        }
        .requestError($message)
    }

    private var reauthorizationPlatform: PlatformOption? {
        guard current.type == "oauth" || current.type == "setup-token" else { return nil }
        return PlatformOption(rawValue: current.platform.lowercased())
    }

    private func reload(notify: Bool = false) async {
        guard let api = session.api else { return }
        do {
            current = try await api.request(method: .get, path: "admin/accounts/\(current.id)")
            if notify { feedback.success("账号信息已刷新", detail: "已同步最新凭证和调度状态。") }
        }
        catch { message = ErrorMessage(error, title: "无法读取账号详情") }
    }

    private func setSchedulable(_ value: Bool) {
        Task {
            isWorking = true
            defer { isWorking = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                current = try await api.request(
                    method: .post,
                    path: "admin/accounts/\(current.id)/schedulable",
                    body: SchedulableRequest(schedulable: value)
                )
                feedback.success(value ? "账号已加入调度" : "账号已暂停调度", detail: "新的调度状态已立即生效。")
            } catch { message = ErrorMessage(error, title: "调度状态更新失败") }
        }
    }

    private func performAction(_ path: String, success: String) {
        Task {
            isWorking = true
            defer { isWorking = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .post, path: "admin/accounts/\(current.id)/\(path)", body: EmptyResponse())
                feedback.success("操作完成", detail: success)
                await reload()
            } catch { message = ErrorMessage(error, title: "操作失败") }
        }
    }

    private func deleteAccount() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .delete, path: "admin/accounts/\(current.id)")
                feedback.success("账号已删除", detail: "账号与分组绑定已移除。")
                dismiss()
            } catch { message = ErrorMessage(error, title: "删除账号失败") }
        }
    }
}
