import SwiftUI

private enum UserSort: String, CaseIterable, Identifiable {
    case createdAt = "created_at"
    case id
    case balance
    case lastUsedAt = "last_used_at"
    case email

    var id: String { rawValue }
    var title: String {
        switch self {
        case .createdAt: return "注册时间"
        case .id: return "用户 ID"
        case .balance: return "余额"
        case .lastUsedAt: return "最后使用"
        case .email: return "邮箱"
        }
    }
}

struct UsersView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    @State private var users: [UserProfile] = []
    @State private var groups: [AdminGroup] = []
    @State private var searchText = ""
    @State private var status: String
    @State private var role = ""
    @State private var groupName = ""
    @State private var sort = UserSort.createdAt
    @State private var descending = true
    @State private var isLoading = false
    @State private var showCreate = false
    @State private var error: ErrorMessage?

    init(initialStatus: String = "") {
        _status = State(initialValue: initialStatus)
    }

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && users.isEmpty {
                    ProgressView("正在读取用户…").frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && users.isEmpty {
                    EmptyState(title: "暂无用户", systemImage: "person.2", detail: "创建用户后可直接设置余额、并发、RPM 和可用分组。")
                }
                if !users.isEmpty { UserTable(users: users) }
            }
            .padding(16)
            .padding(.bottom, 100)
        }
        .navigationTitle("用户管理")
        .searchable(text: $searchText, prompt: "搜索邮箱或用户名")
        .appScreenStyle()
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                filterMenu
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showCreate = true } label: { Image(systemName: "person.badge.plus") }
                    .accessibilityLabel("创建用户")
            }
        }
        .sheet(isPresented: $showCreate) { UserEditorView(onSaved: { Task { await loadUsers(notify: true) } }) }
        .task { await loadGroups(); await loadUsers() }
        .task(id: "\(searchText)|\(status)|\(role)|\(groupName)|\(sort.rawValue)|\(descending)") {
            do { try await Task.sleep(for: .milliseconds(250)) }
            catch { return }
            guard !Task.isCancelled else { return }
            await loadUsers()
        }
        .refreshable { await loadUsers(notify: true) }
        .requestError($error)
    }

    private var filterMenu: some View {
        Menu {
            Section("用户状态") {
                selectionButton("全部用户", isSelected: status.isEmpty) { status = "" }
                selectionButton("正常", isSelected: status == "active") { status = "active" }
                selectionButton("停用", isSelected: status == "disabled") { status = "disabled" }
            }
            Section("角色") {
                selectionButton("全部角色", isSelected: role.isEmpty) { role = "" }
                selectionButton("普通用户", isSelected: role == "user") { role = "user" }
                selectionButton("管理员", isSelected: role == "admin") { role = "admin" }
            }
            if !groups.isEmpty {
                Section("允许分组") {
                    selectionButton("全部分组", isSelected: groupName.isEmpty) { groupName = "" }
                    ForEach(groups) { group in
                        selectionButton(group.name, isSelected: groupName == group.name) { groupName = group.name }
                    }
                }
            }
            Section("排序") {
                ForEach(UserSort.allCases) { option in
                    selectionButton(option.title, isSelected: sort == option) { sort = option }
                }
                Button { descending.toggle() } label: {
                    Label(descending ? "降序" : "升序", systemImage: descending ? "arrow.down" : "arrow.up")
                }
            }
        } label: {
            Image(systemName: "line.3.horizontal.decrease.circle")
        }
        .accessibilityLabel("用户筛选和排序")
    }

    private func selectionButton(_ title: String, isSelected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Label(title, systemImage: isSelected ? "checkmark" : "circle")
        }
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do {
            groups = try await api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")])
        } catch {
            // User loading remains useful when group permissions are temporarily unavailable.
        }
    }

    private func loadUsers(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            var query = [
                URLQueryItem(name: "page", value: "1"),
                URLQueryItem(name: "page_size", value: "100"),
                URLQueryItem(name: "sort_by", value: sort.rawValue),
                URLQueryItem(name: "sort_order", value: descending ? "desc" : "asc")
            ]
            if !searchText.trimmingCharacters(in: .whitespaces).isEmpty { query.append(URLQueryItem(name: "search", value: searchText)) }
            if !status.isEmpty { query.append(URLQueryItem(name: "status", value: status)) }
            if !role.isEmpty { query.append(URLQueryItem(name: "role", value: role)) }
            if !groupName.isEmpty { query.append(URLQueryItem(name: "group_name", value: groupName)) }
            let page: Page<UserProfile> = try await api.request(method: .get, path: "admin/users", query: query)
            guard !Task.isCancelled else { return }
            users = page.items
            if notify { feedback.success("用户列表已刷新", detail: "已加载 \(users.count) 位用户。") }
        } catch {
            // Search and filter changes cancel the obsolete request.
            guard !Task.isCancelled, !isExpectedCancellation(error) else { return }
            self.error = ErrorMessage(error, title: "无法读取用户")
        }
    }
}

private struct UserTable: View {
    let users: [UserProfile]

    var body: some View {
        LazyVStack(spacing: 8) {
            ForEach(users) { user in
                NavigationLink { UserDetailView(user: user) } label: {
                    AdminListRowSurface {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(spacing: 8) {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(user.email)
                                        .font(.subheadline.weight(.semibold))
                                        .lineLimit(1)
                                    Text(user.username?.isEmpty == false ? user.username! : "用户 #\(user.id)")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                }
                                Spacer(minLength: 4)
                                StatusPill(text: user.status ?? "active")
                                Image(systemName: "chevron.right")
                                    .font(.caption.weight(.bold))
                                    .foregroundStyle(.tertiary)
                            }
                            HStack(spacing: 10) {
                                CompactMetric(title: "余额", value: DisplayFormat.currency(user.balance), tint: AppTheme.accent)
                                CompactMetric(title: "并发", value: "\(user.currentConcurrency ?? 0)/\(user.concurrency ?? 0)")
                                CompactMetric(title: "RPM", value: (user.rpmLimit ?? 0) == 0 ? "不限" : String(user.rpmLimit ?? 0))
                            }
                            HStack(spacing: 8) {
                                Text(user.role == "admin" ? "管理员" : "普通用户")
                                    .font(.caption2.weight(.medium))
                                    .foregroundStyle(user.role == "admin" ? .indigo : .secondary)
                                Text("最后使用：\(DisplayFormat.shortDate(user.lastUsedAt))")
                                    .font(.caption2)
                                    .foregroundStyle(.tertiary)
                                    .lineLimit(1)
                            }
                        }
                    }
                }
                .buttonStyle(.plain)
            }
        }
    }
}

struct UserDetailView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss

    @State private var current: UserProfile
    @State private var groups: [AdminGroup] = []
    @State private var showEditor = false
    @State private var showBalance = false
    @State private var showDeleteConfirmation = false
    @State private var apiKeys: [APIKeyRecord] = []
    @State private var usageSummary: UserUsageSummary?
    @State private var balanceHistory: BalanceHistoryResponse?
    @State private var error: ErrorMessage?

    init(user: UserProfile) { _current = State(initialValue: user) }

    private var allowedGroupTitle: String {
        guard let allowed = current.allowedGroups, !allowed.isEmpty else { return "全部分组" }
        let index = Dictionary(uniqueKeysWithValues: groups.map { ($0.id, $0) })
        return allowed.map { id in
            guard let group = index[id] else { return "#\(id)" }
            return "\(group.name)（\(DisplayFormat.decimal(group.rateMultiplier ?? 1))x）"
        }.joined(separator: "、")
    }

    var body: some View {
        List {
            Section("用户资料") {
                LabeledContent("邮箱", value: current.email)
                LabeledContent("用户名", value: current.username?.isEmpty == false ? current.username! : "未设置")
                LabeledContent("角色", value: current.role == "admin" ? "管理员" : "普通用户")
                LabeledContent("状态") { StatusPill(text: current.status ?? "active") }
                LabeledContent("注册时间", value: DisplayFormat.shortDate(current.createdAt))
                LabeledContent("最后使用", value: DisplayFormat.shortDate(current.lastUsedAt))
                if let notes = current.notes, !notes.isEmpty { LabeledContent("备注", value: notes) }
            }
            Section("额度与限制") {
                LabeledContent("余额", value: DisplayFormat.currency(current.balance))
                LabeledContent("并发", value: "\(current.currentConcurrency ?? 0) / \(current.concurrency ?? 0)")
                LabeledContent("RPM 上限", value: (current.rpmLimit ?? 0) == 0 ? "不限制" : String(current.rpmLimit ?? 0))
                LabeledContent("可用分组", value: allowedGroupTitle)
            }
            Section("用量与 API 密钥") {
                LabeledContent("累计请求", value: DisplayFormat.integer(usageSummary?.totalRequests))
                LabeledContent("累计 Token", value: DisplayFormat.integer(usageSummary?.totalTokens))
                LabeledContent("累计成本", value: DisplayFormat.currency(usageSummary?.totalCost))
                NavigationLink { UserAPIKeysView(user: current, initialKeys: apiKeys) } label: {
                    LabeledContent("API 密钥", value: "\(apiKeys.count) 个")
                }
            }
            Section("余额与充值") {
                LabeledContent("累计充值", value: DisplayFormat.currency(balanceHistory?.totalRecharged))
                NavigationLink { BalanceHistoryView(user: current, initialHistory: balanceHistory?.items ?? []) } label: {
                    LabeledContent("余额变动", value: "\(balanceHistory?.items.count ?? 0) 条")
                }
                NavigationLink { PaymentOrdersView(userID: current.id, title: "\(current.email) 的充值") } label: {
                    Label("查看充值订单", systemImage: "creditcard.and.123")
                }
            }
            Section("操作") {
                Button { showEditor = true } label: { Label("编辑用户", systemImage: "pencil") }
                Button { showBalance = true } label: { Label("调整余额", systemImage: "creditcard") }
                Button { toggleStatus() } label: {
                    Label((current.status ?? "active") == "active" ? "停用用户" : "启用用户", systemImage: (current.status ?? "active") == "active" ? "person.crop.circle.badge.xmark" : "person.crop.circle.badge.checkmark")
                }
                Button(role: .destructive) { showDeleteConfirmation = true } label: { Label("删除用户", systemImage: "trash") }
            }
        }
        .navigationTitle(current.username?.isEmpty == false ? current.username! : current.email)
        .navigationBarTitleDisplayMode(.inline)
        .appScreenStyle()
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "pencil") }.accessibilityLabel("编辑用户")
                Button { Task { await reload(notify: true) } } label: { Image(systemName: "arrow.clockwise") }.accessibilityLabel("刷新用户信息")
            }
        }
        .task { await reload() }
        .sheet(isPresented: $showEditor) { UserEditorView(user: current, onSaved: { Task { await reload() } }) }
        .sheet(isPresented: $showBalance) { UserBalanceView(user: current, onSaved: { Task { await reload() } }) }
        .confirmationDialog("确定删除此用户吗？", isPresented: $showDeleteConfirmation, titleVisibility: .visible) {
            Button("删除用户", role: .destructive) { deleteUser() }
            Button("取消", role: .cancel) {}
        } message: { Text("删除后用户无法再登录，相关 API Key 也会受到影响。") }
        .requestError($error)
    }

    private func reload(notify: Bool = false) async {
        guard let api = session.api else { return }
        do {
            async let profile: UserProfile = api.request(method: .get, path: "admin/users/\(current.id)")
            async let allGroups: [AdminGroup] = api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")])
            current = try await profile
            groups = try await allGroups
            await loadRelated()
            if notify { feedback.success("用户信息已刷新", detail: "已同步余额、分组和最新使用情况。") }
        } catch { self.error = ErrorMessage(error, title: "无法读取用户详情") }
    }

    private func loadRelated() async {
        guard let api = session.api else { return }
        do {
            async let keysRequest: Page<APIKeyRecord> = api.request(method: .get, path: "admin/users/\(current.id)/api-keys", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")])
            async let usageRequest: UserUsageSummary = api.request(method: .get, path: "admin/users/\(current.id)/usage")
            async let balanceRequest: BalanceHistoryResponse = api.request(method: .get, path: "admin/users/\(current.id)/balance-history", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "20")])
            apiKeys = try await keysRequest.items
            usageSummary = try await usageRequest
            balanceHistory = try await balanceRequest
        } catch {
            // A user profile must remain editable even if optional activity data is unavailable.
        }
    }

    private func toggleStatus() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let next = (current.status ?? "active") == "active" ? "disabled" : "active"
                let body = UserUpdateRequest(email: nil, password: nil, username: nil, notes: nil, role: nil, concurrency: nil, rpmLimit: nil, status: next, allowedGroups: nil)
                current = try await api.request(method: .put, path: "admin/users/\(current.id)", body: body)
                feedback.success(next == "active" ? "用户已启用" : "用户已停用", detail: "调度状态已立即同步。")
            } catch { self.error = ErrorMessage(error, title: "用户状态更新失败") }
        }
    }

    private func deleteUser() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .delete, path: "admin/users/\(current.id)")
                feedback.success("用户已删除", detail: "该用户及关联访问权限已移除。")
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "删除用户失败") }
        }
    }
}

struct UserEditorView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss

    let user: UserProfile?
    let onSaved: () -> Void

    @State private var email: String
    @State private var password = ""
    @State private var username: String
    @State private var notes: String
    @State private var role: String
    @State private var status: String
    @State private var balance: Double
    @State private var concurrency: Int
    @State private var rpm: Int
    @State private var groups: [AdminGroup] = []
    @State private var selectedGroupIDs: Set<Int>
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(user: UserProfile? = nil, onSaved: @escaping () -> Void) {
        self.user = user
        self.onSaved = onSaved
        _email = State(initialValue: user?.email ?? "")
        _username = State(initialValue: user?.username ?? "")
        _notes = State(initialValue: user?.notes ?? "")
        _role = State(initialValue: user?.role ?? "user")
        _status = State(initialValue: user?.status ?? "active")
        _balance = State(initialValue: user?.balance ?? 0)
        _concurrency = State(initialValue: max(1, user?.concurrency ?? 5))
        _rpm = State(initialValue: user?.rpmLimit ?? 0)
        _selectedGroupIDs = State(initialValue: Set(user?.allowedGroups ?? []))
    }

    private var isEditing: Bool { user != nil }
    private var sortedGroups: [AdminGroup] {
        groups.sorted {
            let left = ($0.platform, $0.rateMultiplier ?? 1, $0.name)
            let right = ($1.platform, $1.rateMultiplier ?? 1, $1.name)
            return left.0 == right.0 ? (left.1 == right.1 ? left.2 < right.2 : left.1 < right.1) : left.0 < right.0
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("基本信息") {
                    TextField("邮箱", text: $email).textInputAutocapitalization(.never).keyboardType(.emailAddress).autocorrectionDisabled()
                    TextField("用户名（可选）", text: $username)
                    TextField("管理员备注（可选）", text: $notes, axis: .vertical).lineLimit(2...4)
                    SecureField(isEditing ? "新密码（留空不修改）" : "初始密码", text: $password)
                }
                Section("权限与状态") {
                    Picker("角色", selection: $role) {
                        Text("普通用户").tag("user")
                        Text("管理员").tag("admin")
                    }
                    if isEditing {
                        Picker("状态", selection: $status) {
                            Text("启用").tag("active")
                            Text("停用").tag("disabled")
                        }
                    }
                }
                Section("额度与限制") {
                    if !isEditing { DecimalInput(label: "初始余额", value: $balance, range: 0...100_000, step: 1, suffix: "¥") }
                    IntegerInput(label: "并发数", value: $concurrency, range: 1...100, step: 1)
                    IntegerInput(label: "RPM 上限", value: $rpm, range: 0...100_000, step: 10, zeroLabel: "不限制", suffix: "RPM")
                }
                Section("允许使用的分组") {
                    if groups.isEmpty {
                        ProgressView("正在读取分组…")
                    } else {
                        ForEach(sortedGroups) { group in
                            Toggle(isOn: Binding(get: { selectedGroupIDs.contains(group.id) }, set: { enabled in
                                if enabled { selectedGroupIDs.insert(group.id) } else { selectedGroupIDs.remove(group.id) }
                            })) {
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(group.name)
                                    HStack(spacing: 7) {
                                        PlatformBadge(platform: group.platform)
                                        Text("倍率 \(DisplayFormat.decimal(group.rateMultiplier ?? 1))")
                                            .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }
                }
            }
            .scrollDismissesKeyboard(.interactively)
            .appScreenStyle()
            .navigationTitle(isEditing ? "编辑用户" : "创建用户")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "正在保存…" : "保存") { save() }
                        .disabled(isSaving || email.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || (!isEditing && password.count < 6))
                }
            }
            .task { await loadGroups() }
        }
        .requestError($error)
    }

    private func loadGroups() async {
        guard let api = session.api else { return }
        do { groups = try await api.request(method: .get, path: "admin/groups/all", query: [URLQueryItem(name: "include_inactive", value: "true")]) }
        catch { self.error = ErrorMessage(error, title: "无法读取分组") }
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let emailValue = email.trimmingCharacters(in: .whitespacesAndNewlines)
                if let user {
                    let request = UserUpdateRequest(email: emailValue, password: password.isEmpty ? nil : password, username: username.trimmingCharacters(in: .whitespacesAndNewlines), notes: notes.trimmingCharacters(in: .whitespacesAndNewlines), role: role, concurrency: concurrency, rpmLimit: rpm, status: status, allowedGroups: Array(selectedGroupIDs).sorted())
                    let _: UserProfile = try await api.request(method: .put, path: "admin/users/\(user.id)", body: request)
                } else {
                    let request = UserCreateRequest(email: emailValue, password: password, username: username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : username, notes: notes.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : notes, role: role, balance: balance, concurrency: concurrency, rpmLimit: rpm, allowedGroups: Array(selectedGroupIDs).sorted())
                    let _: UserProfile = try await api.request(method: .post, path: "admin/users", body: request)
                }
                password = ""
                feedback.success(isEditing ? "用户已保存" : "用户已创建", detail: "权限、额度和分组已同步。")
                onSaved()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: isEditing ? "用户保存失败" : "用户创建失败") }
        }
    }
}

struct UserBalanceView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    let user: UserProfile
    let onSaved: () -> Void
    @State private var amount = 0.0
    @State private var operation = "add"
    @State private var notes = ""
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    var body: some View {
        NavigationStack {
            Form {
                Section(user.email) { LabeledContent("当前余额", value: DisplayFormat.currency(user.balance)) }
                Section("余额调整") {
                    Picker("操作", selection: $operation) {
                        Text("增加余额").tag("add")
                        Text("扣减余额").tag("subtract")
                        Text("设为固定余额").tag("set")
                    }
                    DecimalInput(label: "金额", value: $amount, range: 0...100_000, step: 0.01, suffix: "¥")
                    TextField("操作备注", text: $notes, axis: .vertical)
                }
            }
            .navigationTitle("调整余额")
            .navigationBarTitleDisplayMode(.inline)
            .scrollDismissesKeyboard(.interactively)
            .appScreenStyle()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button(isSaving ? "正在保存…" : "保存") { save() }.disabled(isSaving || amount <= 0) }
            }
        }
        .requestError($error)
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let body = BalanceChangeRequest(balance: amount, operation: operation, notes: notes)
                let _: UserProfile = try await api.request(method: .post, path: "admin/users/\(user.id)/balance", body: body)
                feedback.success("余额已调整", detail: "已按 \(operation == "add" ? "增加" : operation == "subtract" ? "扣减" : "设定") \(DisplayFormat.currency(amount)) 处理。")
                onSaved()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "余额调整失败") }
        }
    }
}
