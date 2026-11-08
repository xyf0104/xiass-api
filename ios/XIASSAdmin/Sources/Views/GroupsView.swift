import SwiftUI

struct GroupsView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var groups: [AdminGroup] = []
    @State private var platform = "all"
    @State private var isLoading = false
    @State private var showCreate = false
    @State private var error: ErrorMessage?

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && groups.isEmpty {
                    ProgressView("正在读取分组…")
                        .frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && groups.isEmpty {
                    EmptyState(title: "暂无分组", systemImage: "square.stack.3d.up", detail: "创建分组后，即可把上游账号和用户接入对应平台。")
                }
                ForEach(platformSections, id: \.platform) { section in
                    HStack(spacing: 8) {
                        PlatformBadge(platform: section.platform)
                        Text("\(section.groups.count) 个分组")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Spacer()
                    }
                    .padding(.horizontal, 4)
                    .padding(.top, 4)
                    GroupTable(groups: section.groups)
                }
            }
            .padding(16)
            .padding(.bottom, 72)
        }
        .navigationTitle("分组管理")
        .appScreenStyle()
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Menu {
                    Button { platform = "all" } label: { Label("全部平台", systemImage: platform == "all" ? "checkmark" : "circle") }
                    ForEach(GroupPlatformOption.allCases) { option in
                        Button { platform = option.rawValue } label: {
                            Label(option.title, systemImage: platform == option.rawValue ? "checkmark" : PlatformStyle.icon(for: option.rawValue))
                        }
                    }
                } label: { Image(systemName: "line.3.horizontal.decrease.circle") }
                    .accessibilityLabel("分组平台分类")
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { showCreate = true } label: { Image(systemName: "plus") }
                    .accessibilityLabel("创建分组")
            }
        }
        .sheet(isPresented: $showCreate) { GroupEditorView(onSaved: { Task { await load() } }) }
        .task(id: platform) { await load() }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            var query = [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100"), URLQueryItem(name: "sort_by", value: "rate_multiplier"), URLQueryItem(name: "sort_order", value: "asc")]
            if platform != "all" { query.append(URLQueryItem(name: "platform", value: platform)) }
            let page: Page<AdminGroup> = try await api.request(method: .get, path: "admin/groups", query: query)
            guard !Task.isCancelled else { return }
            groups = page.items
            if notify { feedback.success("分组列表已刷新", detail: "已同步 \(groups.count) 个分组。") }
        } catch {
            // Platform filter changes cancel the obsolete request.
            guard !Task.isCancelled, !isExpectedCancellation(error) else { return }
            self.error = ErrorMessage(error, title: "无法读取分组")
        }
    }

    private var platformSections: [(platform: String, groups: [AdminGroup])] {
        let grouped = Dictionary(grouping: groups) { $0.platform.lowercased() }
        let order = ["openai", "anthropic", "gemini", "antigravity", "grok", "composite"]
        return grouped.keys.sorted { (order.firstIndex(of: $0) ?? order.count) < (order.firstIndex(of: $1) ?? order.count) }.map { platform in
            let sorted = (grouped[platform] ?? []).sorted {
                let left = $0.rateMultiplier ?? 1
                let right = $1.rateMultiplier ?? 1
                return left == right ? $0.name.localizedStandardCompare($1.name) == .orderedAscending : left < right
            }
            return (platform, sorted)
        }
    }

}

private struct GroupTable: View {
    let groups: [AdminGroup]

    var body: some View {
        LazyVStack(spacing: 8) {
            ForEach(groups) { group in
                NavigationLink { GroupDetailView(group: group) } label: {
                    AdminListRowSurface {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(spacing: 8) {
                                PlatformBadge(platform: group.platform)
                                Text(group.name)
                                    .font(.subheadline.weight(.semibold))
                                    .lineLimit(1)
                                Spacer(minLength: 4)
                                StatusPill(text: group.status)
                                Image(systemName: "chevron.right")
                                    .font(.caption.weight(.bold))
                                    .foregroundStyle(.tertiary)
                            }
                            HStack(spacing: 10) {
                                CompactMetric(title: "账号", value: "\(group.activeAccountCount ?? 0)/\(group.accountCount ?? 0)")
                                CompactMetric(title: "倍率", value: DisplayFormat.decimal(group.rateMultiplier ?? 1))
                                CompactMetric(title: "RPM", value: (group.rpmLimit ?? 0) == 0 ? "不限" : String(group.rpmLimit ?? 0))
                            }
                            if let description = group.description, !description.isEmpty {
                                Text(description)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(2)
                            }
                        }
                    }
                }
                .buttonStyle(.plain)
            }
        }
    }
}

struct GroupDetailView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    @State private var current: AdminGroup
    @State private var showEditor = false
    @State private var showDeleteConfirmation = false
    @State private var error: ErrorMessage?

    init(group: AdminGroup) {
        _current = State(initialValue: group)
    }

    var body: some View {
        List {
            Section("分组信息") {
                LabeledContent("平台", value: current.platform.uppercased())
                LabeledContent("账号", value: "\(current.activeAccountCount ?? 0) / \(current.accountCount ?? 0)")
                LabeledContent("费率倍率", value: DisplayFormat.decimal(current.rateMultiplier))
                LabeledContent("RPM 上限", value: (current.rpmLimit ?? 0) == 0 ? "不限制" : String(current.rpmLimit ?? 0))
                LabeledContent("独占分组", value: current.isExclusive == true ? "是" : "否")
                LabeledContent("状态") { StatusPill(text: current.status) }
                if let description = current.description, !description.isEmpty { LabeledContent("说明", value: description) }
            }

            Section("操作") {
                Button { showEditor = true } label: { Label("编辑分组", systemImage: "pencil") }
                Button(current.status == "active" ? "停用分组" : "启用分组") { toggleStatus() }
                Button(role: .destructive) { showDeleteConfirmation = true } label: { Label("删除分组", systemImage: "trash") }
            }
        }
        .navigationTitle(current.name)
        .navigationBarTitleDisplayMode(.inline)
        .appScreenStyle()
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button { showEditor = true } label: { Image(systemName: "pencil") }
                    .accessibilityLabel("编辑分组")
                Button { Task { await reload(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新分组信息")
            }
        }
        .task { await reload() }
        .sheet(isPresented: $showEditor) {
            GroupEditorView(group: current, onSaved: { Task { await reload() } })
        }
        .confirmationDialog("确定删除此分组吗？", isPresented: $showDeleteConfirmation, titleVisibility: .visible) {
            Button("删除分组", role: .destructive) { deleteGroup() }
            Button("取消", role: .cancel) {}
        } message: {
            Text("已绑定的账号或 API Key 可能需要重新分配。")
        }
        .requestError($error)
    }

    private func reload(notify: Bool = false) async {
        guard let api = session.api else { return }
        do {
            current = try await api.request(method: .get, path: "admin/groups/\(current.id)")
            if notify { feedback.success("分组信息已刷新", detail: "已同步分组配置与账号状态。") }
        }
        catch { self.error = ErrorMessage(error, title: "无法读取分组详情") }
    }

    private func toggleStatus() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let request = GroupUpdateRequest(name: nil, description: nil, platform: nil, rateMultiplier: nil, rpmLimit: nil, isExclusive: nil, status: current.status == "active" ? "inactive" : "active", costRatio: nil, maxReasoningEffort: nil)
                current = try await api.request(method: .put, path: "admin/groups/\(current.id)", body: request)
                feedback.success(current.status == "active" ? "分组已启用" : "分组已停用", detail: "新的调度状态已立即生效。")
            } catch { self.error = ErrorMessage(error, title: "分组状态更新失败") }
        }
    }

    private func deleteGroup() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .delete, path: "admin/groups/\(current.id)")
                feedback.success("分组已删除", detail: "分组与关联调度配置已移除。")
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "删除分组失败") }
        }
    }
}

struct GroupEditorView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss

    let group: AdminGroup?
    let onSaved: () -> Void

    @State private var name: String
    @State private var description: String
    @State private var platform: String
    @State private var multiplier: Double
    @State private var rpmLimit: Int
    @State private var exclusive: Bool
    @State private var status: String
    @State private var costRatio: Double
    @State private var maxReasoningEffort: String
    @State private var advancedJSON = "{\n  \n}"
    @State private var editAdvanced = false
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(group: AdminGroup? = nil, onSaved: @escaping () -> Void) {
        self.group = group
        self.onSaved = onSaved
        _name = State(initialValue: group?.name ?? "")
        _description = State(initialValue: group?.description ?? "")
        _platform = State(initialValue: group?.platform ?? "openai")
        _multiplier = State(initialValue: group?.rateMultiplier ?? 1)
        _rpmLimit = State(initialValue: group?.rpmLimit ?? 0)
        _exclusive = State(initialValue: group?.isExclusive ?? false)
        _status = State(initialValue: group?.status ?? "active")
        _costRatio = State(initialValue: group?.costRatio ?? 0)
        _maxReasoningEffort = State(initialValue: group?.maxReasoningEffort ?? "")
    }

    private var isEditing: Bool { group != nil }

    var body: some View {
        NavigationStack {
            Form {
                Section("基本信息") {
                    TextField("分组名称", text: $name)
                    TextField("分组说明（可选）", text: $description, axis: .vertical)
                        .lineLimit(2...4)
                    Picker("平台", selection: $platform) {
                        ForEach(GroupPlatformOption.allCases) { option in
                            Text(option.title).tag(option.rawValue)
                        }
                    }
                    if isEditing {
                        Picker("状态", selection: $status) {
                            Text("启用").tag("active")
                            Text("停用").tag("inactive")
                        }
                    }
                }

                Section("调度与限制") {
                    DecimalInput(label: "费率倍率", value: $multiplier, range: 0.05...100, step: 0.05, suffix: "x")
                    IntegerInput(label: "RPM 上限", value: $rpmLimit, range: 0...100_000, step: 10, zeroLabel: "不限制", suffix: "RPM")
                    Toggle("独占分组", isOn: $exclusive)
                    if platform == "openai" || platform == "anthropic" {
                        DecimalInput(label: "成本比例", value: $costRatio, range: 0...10, step: 0.05, suffix: "x")
                    }
                    if platform == "openai" {
                        Picker("推理强度上限", selection: $maxReasoningEffort) {
                            Text("不限制").tag("")
                            Text("低").tag("low")
                            Text("中").tag("medium")
                            Text("高").tag("high")
                            Text("超高").tag("xhigh")
                        }
                    }
                }

                Section {
                    Toggle("编辑网页端高级字段 JSON", isOn: $editAdvanced)
                    if editAdvanced {
                        JSONTextEditor(text: $advancedJSON, minHeight: 180)
                    }
                } header: {
                    Text("高级配置")
                } footer: {
                    Text("模型路由、图像计费、峰值倍率等可直接编辑 JSON；不会再跳转网页后台。")
                }
            }
            .scrollDismissesKeyboard(.interactively)
            .dismissKeyboardOnTap()
            .appScreenStyle()
            .navigationTitle(isEditing ? "编辑分组" : "创建分组")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "正在保存…" : "保存") { save() }
                        .disabled(isSaving || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .requestError($error)
    }

    private func payload() throws -> [String: JSONValue] {
        var value = group?.rawPayload ?? [:]
        value.merge([
            "name": .string(name.trimmingCharacters(in: .whitespacesAndNewlines)),
            "description": .string(description.trimmingCharacters(in: .whitespacesAndNewlines)),
            "platform": .string(platform),
            "rate_multiplier": .number(multiplier),
            "rpm_limit": .number(Double(rpmLimit)),
            "is_exclusive": .bool(exclusive),
            "max_reasoning_effort": .string(maxReasoningEffort)
        ]) { _, replacement in replacement }
        if costRatio > 0 { value["cost_ratio"] = .number(costRatio) }
        else { value.removeValue(forKey: "cost_ratio") }
        if isEditing { value["status"] = .string(status) }
        for key in ["daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd"] {
            if value[key] == .null { value.removeValue(forKey: key) }
        }
        if editAdvanced {
            value.merge(try JSONValue.object(from: advancedJSON)) { _, advanced in advanced }
        }
        return value
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let body = try payload()
                if let group {
                    let _: AdminGroup = try await api.request(method: .put, path: "admin/groups/\(group.id)", body: body)
                } else {
                    let _: AdminGroup = try await api.request(method: .post, path: "admin/groups", body: body)
                }
                feedback.success(isEditing ? "分组已保存" : "分组已创建", detail: "平台、倍率、限制与高级字段已同步。")
                onSaved()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: isEditing ? "分组保存失败" : "分组创建失败") }
        }
    }
}

private enum GroupPlatformOption: String, CaseIterable, Identifiable {
    case openai, anthropic, gemini, antigravity, grok, composite
    var id: String { rawValue }
    var title: String {
        switch self {
        case .openai: return "OpenAI"
        case .anthropic: return "Anthropic"
        case .gemini: return "Gemini"
        case .antigravity: return "Antigravity"
        case .grok: return "Grok"
        case .composite: return "组合分组"
        }
    }
}
