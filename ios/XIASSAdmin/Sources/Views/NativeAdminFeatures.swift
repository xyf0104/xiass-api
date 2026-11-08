import SwiftUI

struct ProxyListView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var proxies: [AdminProxy] = []
    @State private var isLoading = false
    @State private var showCreate = false
    @State private var error: ErrorMessage?

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 12) {
                if isLoading && proxies.isEmpty { ProgressView("正在读取代理…").frame(maxWidth: .infinity, minHeight: 180) }
                if !isLoading && proxies.isEmpty {
                    EmptyState(title: "暂无代理节点", systemImage: "network", detail: "可在此添加并测试 HTTP、HTTPS 或 SOCKS5 代理。")
                }
                if !proxies.isEmpty {
                    ProxyTable(proxies: proxies, onChanged: { Task { await load() } })
                }
            }
            .padding(16)
            .padding(.bottom, 100)
        }
        .appScreenStyle()
        .navigationTitle("代理与 FRP")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { showCreate = true } label: { Image(systemName: "plus") }.accessibilityLabel("添加代理")
            }
        }
        .sheet(isPresented: $showCreate) { ProxyEditorView(onSaved: { Task { await load() } }) }
        .task { await load() }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let page: Page<AdminProxy> = try await api.request(method: .get, path: "admin/proxies", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "100")])
            proxies = page.items
            if notify { feedback.success("代理列表已刷新", detail: "已同步 \(proxies.count) 个节点。") }
        } catch { self.error = ErrorMessage(error, title: "无法读取代理节点") }
    }
}

private struct ProxyTable: View {
    let proxies: [AdminProxy]
    let onChanged: () -> Void

    var body: some View {
        LazyVStack(spacing: 8) {
            ForEach(proxies) { proxy in
                NavigationLink { ProxyDetailView(proxy: proxy, onChanged: onChanged) } label: {
                    AdminListRowSurface {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(spacing: 8) {
                                Text(proxy.name).font(.subheadline.weight(.semibold)).lineLimit(1)
                                Spacer(minLength: 4)
                                StatusPill(text: proxy.status ?? "inactive")
                                Image(systemName: "chevron.right").font(.caption.weight(.bold)).foregroundStyle(.tertiary)
                            }
                            Text("\(proxy.protocolName?.uppercased() ?? "--") · \(proxy.host ?? "--"):\(proxy.port.map(String.init) ?? "--")")
                                .font(.caption.monospaced())
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                            HStack(spacing: 10) {
                                CompactMetric(title: "延迟", value: proxy.latencyMS.map { "\($0) ms" } ?? "未检测", tint: latencyTint(proxy.latencyMS))
                                CompactMetric(title: "绑定账号", value: String(proxy.accountCount ?? 0))
                                CompactMetric(title: "地区", value: proxy.country ?? "--", tint: .secondary)
                            }
                        }
                    }
                }
                .buttonStyle(.plain)
            }
        }
    }

    private func latencyTint(_ latency: Int?) -> Color {
        guard let latency else { return .secondary }
        if latency < 180 { return .green }
        if latency < 500 { return .orange }
        return .red
    }
}

struct ProxyDetailView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    let proxy: AdminProxy
    let onChanged: () -> Void
    @State private var showEditor = false
    @State private var showDelete = false
    @State private var isTesting = false
    @State private var error: ErrorMessage?

    var body: some View {
        List {
            Section("节点信息") {
                LabeledContent("名称", value: proxy.name)
                LabeledContent("协议", value: proxy.protocolName?.uppercased() ?? "--")
                LabeledContent("地址", value: "\(proxy.host ?? "--"):\(proxy.port.map(String.init) ?? "--")")
                LabeledContent("状态") { StatusPill(text: proxy.status ?? "inactive") }
                LabeledContent("绑定账号", value: "\(proxy.accountCount ?? 0) 个")
            }
            Section("连通性") {
                LabeledContent("最近延迟", value: proxy.latencyMS.map { "\($0) ms" } ?? "未检测")
                LabeledContent("检测状态", value: proxy.latencyStatus ?? "--")
                if let message = proxy.latencyMessage, !message.isEmpty { LabeledContent("检测说明", value: message) }
                LabeledContent("质量", value: proxy.qualityScore.map { "\($0) 分" } ?? proxy.qualityStatus ?? "未检测")
            }
            Section("操作") {
                Button(isTesting ? "正在检测…" : "实时连通性检测") { test() }.disabled(isTesting)
                Button { showEditor = true } label: { Label("编辑节点", systemImage: "pencil") }
                Button(role: .destructive) { showDelete = true } label: { Label("删除节点", systemImage: "trash") }
            }
        }
        .appScreenStyle()
        .navigationTitle(proxy.name)
        .navigationBarTitleDisplayMode(.inline)
        .sheet(isPresented: $showEditor) { ProxyEditorView(proxy: proxy, onSaved: onChanged) }
        .confirmationDialog("确定删除此代理节点吗？", isPresented: $showDelete, titleVisibility: .visible) {
            Button("删除节点", role: .destructive) { delete() }
            Button("取消", role: .cancel) {}
        }
        .requestError($error)
    }

    private func test() {
        Task {
            isTesting = true
            defer { isTesting = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: JSONValue = try await api.request(method: .post, path: "admin/proxies/\(proxy.id)/test", body: EmptyResponse())
                feedback.success("代理连通性检测完成", detail: "检测结果已写入 XIASS API 节点状态。")
                onChanged()
            } catch { self.error = ErrorMessage(error, title: "代理连通性检测失败") }
        }
    }

    private func delete() {
        Task {
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let _: EmptyResponse = try await api.request(method: .delete, path: "admin/proxies/\(proxy.id)")
                feedback.success("代理节点已删除", detail: "绑定关系已同步更新。")
                onChanged()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "删除代理节点失败") }
        }
    }
}

struct ProxyEditorView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @Environment(\.dismiss) private var dismiss
    let proxy: AdminProxy?
    let onSaved: () -> Void
    @State private var name: String
    @State private var protocolName: String
    @State private var host: String
    @State private var port: Int
    @State private var username: String
    @State private var password = ""
    @State private var status: String
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    init(proxy: AdminProxy? = nil, onSaved: @escaping () -> Void) {
        self.proxy = proxy
        self.onSaved = onSaved
        _name = State(initialValue: proxy?.name ?? "")
        _protocolName = State(initialValue: proxy?.protocolName ?? "socks5")
        _host = State(initialValue: proxy?.host ?? "")
        _port = State(initialValue: proxy?.port ?? 1080)
        _username = State(initialValue: proxy?.username ?? "")
        _status = State(initialValue: proxy?.status ?? "active")
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("节点信息") {
                    TextField("节点名称", text: $name)
                    Picker("协议", selection: $protocolName) {
                        Text("HTTP").tag("http")
                        Text("HTTPS").tag("https")
                        Text("SOCKS5").tag("socks5")
                        Text("SOCKS5H").tag("socks5h")
                    }
                    TextField("主机或 IP", text: $host).textInputAutocapitalization(.never).autocorrectionDisabled()
                    IntegerInput(label: "端口", value: $port, range: 1...65_535, step: 1)
                    TextField("用户名（可选）", text: $username).textInputAutocapitalization(.never).autocorrectionDisabled()
                    SecureField(proxy == nil ? "密码（可选）" : "新密码（留空保持原值）", text: $password)
                    if proxy != nil {
                        Picker("状态", selection: $status) {
                            Text("启用").tag("active")
                            Text("停用").tag("inactive")
                        }
                    }
                }
            }
            .scrollDismissesKeyboard(.interactively)
            .appScreenStyle()
            .navigationTitle(proxy == nil ? "添加代理" : "编辑代理")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button(isSaving ? "正在保存…" : "保存") { save() }.disabled(isSaving || name.isEmpty || host.isEmpty) }
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
                let body = ProxyWriteRequest(name: name.trimmingCharacters(in: .whitespacesAndNewlines), protocolName: protocolName, host: host.trimmingCharacters(in: .whitespacesAndNewlines), port: port, username: username.trimmingCharacters(in: .whitespacesAndNewlines), password: password, status: proxy == nil ? nil : status)
                if let proxy {
                    let _: AdminProxy = try await api.request(method: .put, path: "admin/proxies/\(proxy.id)", body: body)
                } else {
                    let _: AdminProxy = try await api.request(method: .post, path: "admin/proxies", body: body)
                }
                feedback.success(proxy == nil ? "代理节点已添加" : "代理节点已保存", detail: "地址与运行状态已同步。")
                onSaved()
                dismiss()
            } catch { self.error = ErrorMessage(error, title: "保存代理节点失败") }
        }
    }
}

struct ModelPricingLookupView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var model = ""
    @State private var pricing: [String: JSONValue] = [:]
    @State private var isLoading = false
    @State private var error: ErrorMessage?

    private let keys = ["input_price", "output_price", "cache_write_price", "cache_read_price", "image_input_price", "image_output_price"]

    var body: some View {
        Form {
            Section {
                TextField("模型名称，例如 gpt-5.6", text: $model).textInputAutocapitalization(.never).autocorrectionDisabled()
                Button(isLoading ? "正在查询…" : "查询默认价格") { Task { await lookup() } }
                    .disabled(isLoading || model.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            } header: {
                Text("查询模型价格")
            } footer: {
                Text("此处读取 XIASS API 渠道定价服务，用于核对模型默认计费字段。")
            }
            if !pricing.isEmpty {
                Section("价格结果") {
                    LabeledContent("已找到", value: pricing["found"]?.boolValue == true ? "是" : "否")
                    ForEach(keys, id: \.self) { key in
                        if let value = pricing[key]?.doubleValue { LabeledContent(priceTitle(key), value: DisplayFormat.currency(value)) }
                    }
                }
            }
        }
        .appScreenStyle()
        .navigationTitle("模型价格")
        .navigationBarTitleDisplayMode(.inline)
        .requestError($error)
    }

    private func lookup() async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            pricing = try await api.request(method: .get, path: "admin/channels/model-pricing", query: [URLQueryItem(name: "model", value: model.trimmingCharacters(in: .whitespacesAndNewlines))])
            feedback.success(pricing["found"]?.boolValue == true ? "已找到模型价格" : "未找到默认价格", detail: "已完成 \(model) 的定价查询。")
        } catch { self.error = ErrorMessage(error, title: "查询模型价格失败") }
    }

    private func priceTitle(_ key: String) -> String {
        switch key {
        case "input_price": return "输入价格"
        case "output_price": return "输出价格"
        case "cache_write_price": return "缓存写入"
        case "cache_read_price": return "缓存读取"
        case "image_input_price": return "图像输入"
        case "image_output_price": return "图像输出"
        default: return key
        }
    }
}

struct SystemSettingsView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter
    @State private var rawJSON = "{}"
    @State private var isLoading = false
    @State private var isSaving = false
    @State private var error: ErrorMessage?

    var body: some View {
        Form {
            Section("系统配置") {
                Text("原生高级设置编辑器")
                    .font(.headline)
                Text("这里直接使用 XIASS API 的系统设置接口，不依赖网页后台。保存前会校验 JSON。")
                    .font(.footnote).foregroundStyle(.secondary)
                JSONTextEditor(text: $rawJSON, minHeight: 360)
            }
            Section {
                Button(isLoading ? "正在读取…" : "重新读取系统配置") { Task { await load(notify: true) } }.disabled(isLoading || isSaving)
                Button(isSaving ? "正在保存…" : "保存系统配置") { save() }.disabled(isLoading || isSaving)
            }
        }
        .appScreenStyle()
        .navigationTitle("系统配置")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .requestError($error)
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let values: [String: JSONValue] = try await api.request(method: .get, path: "admin/settings")
            let encoder = JSONEncoder()
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            rawJSON = String(decoding: try encoder.encode(values), as: UTF8.self)
            if notify { feedback.success("系统配置已读取", detail: "已从 XIASS API 同步全部可见设置。") }
        } catch { self.error = ErrorMessage(error, title: "无法读取系统配置") }
    }

    private func save() {
        Task {
            isSaving = true
            defer { isSaving = false }
            do {
                guard let api = session.api else { throw APIError(message: "登录已失效，请重新登录。") }
                let values = try JSONValue.object(from: rawJSON)
                let _: [String: JSONValue] = try await api.request(method: .put, path: "admin/settings", body: values)
                feedback.success("系统配置已保存", detail: "服务端已接收并应用本次设置。")
                await load()
            } catch { self.error = ErrorMessage(error, title: "保存系统配置失败") }
        }
    }
}
