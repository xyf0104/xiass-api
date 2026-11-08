import SwiftUI

struct UsageLogsView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    @State private var logs: [UsageLog] = []
    @State private var isLoading = false
    @State private var search = ""
    @State private var error: ErrorMessage?

    var body: some View {
        ScrollView {
            LazyVStack(spacing: 8) {
                if !logs.isEmpty {
                    HStack {
                        Text("最近 100 条调用记录")
                            .font(.subheadline.weight(.semibold))
                        Spacer()
                        Text("已加载 \(logs.count) 条")
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                    .padding(.horizontal, 4)
                    .padding(.bottom, 2)
                }
                if isLoading && logs.isEmpty {
                    ProgressView("正在读取最近调用…").frame(maxWidth: .infinity, minHeight: 180)
                }
                if !isLoading && logs.isEmpty {
                    EmptyState(title: "暂无调用记录", systemImage: "clock.arrow.circlepath", detail: "新的 API 调用会实时显示在这里。")
                }
                if !logs.isEmpty { UsageLogTable(logs: logs) }
            }
            .padding(16)
            .padding(.bottom, 100)
        }
        .appScreenStyle()
        .navigationTitle("最近调用")
        .navigationBarTitleDisplayMode(.inline)
        .searchable(text: $search, prompt: "搜索用户、模型或请求 ID")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
            }
        }
        .task(id: search) {
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
            if !search.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                query.append(URLQueryItem(name: "search", value: search))
            }
            let page: Page<UsageLog> = try await api.request(method: .get, path: "admin/usage", query: query)
            guard !Task.isCancelled else { return }
            logs = page.items
            if notify { feedback.success("调用记录已刷新", detail: "已加载 \(logs.count) 条最近调用。") }
        } catch {
            // Replacing a search query cancels the obsolete request.
            guard !Task.isCancelled, !isExpectedCancellation(error) else { return }
            self.error = ErrorMessage(error, title: "无法读取调用记录")
        }
    }
}

private struct UsageLogTable: View {
    let logs: [UsageLog]

    var body: some View {
        LazyVStack(spacing: 7) {
            ForEach(logs, id: \.stableID) { log in
                NavigationLink { UsageLogDetailView(log: log) } label: {
                    AdminListRowSurface {
                        VStack(alignment: .leading, spacing: 7) {
                            HStack(spacing: 8) {
                                Text(log.model ?? "--")
                                    .font(.subheadline.weight(.semibold))
                                    .lineLimit(1)
                                Spacer(minLength: 4)
                                Text(log.statusCode.map(String.init) ?? "--")
                                    .font(.caption.weight(.bold).monospacedDigit())
                                    .foregroundStyle(statusColor(log.statusCode))
                                Image(systemName: "chevron.right")
                                    .font(.caption.weight(.bold))
                                    .foregroundStyle(.tertiary)
                            }
                            HStack(spacing: 8) {
                                Text(log.displayUser)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                Spacer(minLength: 4)
                                Text(DisplayFormat.shortDate(log.createdAt))
                                    .font(.caption2.monospacedDigit())
                                    .foregroundStyle(.tertiary)
                            }
                            HStack(spacing: 10) {
                                CompactMetric(title: "首字", value: log.firstTokenMS.map { "\($0) ms" } ?? "--")
                                CompactMetric(title: "总延迟", value: log.durationMS.map { "\($0) ms" } ?? "--")
                                CompactMetric(title: "推理", value: reasoningTitle(log.reasoningEffort), tint: .secondary)
                                CompactMetric(title: "成本", value: DisplayFormat.currency(log.actualCost), tint: AppTheme.accent)
                            }
                            Text("上游：\(log.upstreamModel?.isEmpty == false ? log.upstreamModel! : "--") · \(log.displayAccount)")
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

    private func statusColor(_ statusCode: Int?) -> Color {
        guard let statusCode else { return .secondary }
        return (200..<300).contains(statusCode) ? .green : .red
    }

}

struct UsageLogDetailView: View {
    let log: UsageLog

    var body: some View {
        List {
            Section("调用") {
                LabeledContent("用户", value: log.displayUser)
                LabeledContent("请求模型", value: log.model ?? "--")
                if let upstream = log.upstreamModel, !upstream.isEmpty { LabeledContent("上游模型", value: upstream) }
                LabeledContent("分组", value: log.displayGroup)
                LabeledContent("上游账号", value: log.displayAccount)
                LabeledContent("推理程度", value: reasoningTitle(log.reasoningEffort))
                LabeledContent("服务层级", value: log.serviceTier ?? "默认")
            }
            Section("链路与延迟") {
                LabeledContent("入口", value: log.inboundEndpoint ?? "--")
                LabeledContent("上游链路", value: log.upstreamEndpoint ?? "--")
                LabeledContent("模型映射", value: log.modelMappingChain ?? "无")
                LabeledContent("首字延迟", value: log.firstTokenMS.map { "\($0) ms" } ?? "--")
                LabeledContent("总延迟", value: log.durationMS.map { "\($0) ms" } ?? "--")
                LabeledContent("流式响应", value: log.stream == true ? "是" : "否")
                LabeledContent("请求类型", value: log.requestType ?? "--")
            }
            Section("计费与状态") {
                LabeledContent("状态码", value: log.statusCode.map(String.init) ?? "--")
                LabeledContent("实际成本", value: DisplayFormat.currency(log.actualCost))
                LabeledContent("Token", value: DisplayFormat.integer(log.totalTokens))
                LabeledContent("请求 ID", value: log.requestID ?? "--")
                LabeledContent("发生时间", value: DisplayFormat.shortDate(log.createdAt))
            }
        }
        .appScreenStyle()
        .navigationTitle("调用详情")
        .navigationBarTitleDisplayMode(.inline)
    }
}

func reasoningTitle(_ value: String?) -> String {
    switch value?.lowercased() {
    case "low": return "低"
    case "medium": return "中"
    case "high": return "高"
    case "xhigh", "max": return "超高"
    default: return value?.isEmpty == false ? value! : "默认"
    }
}
