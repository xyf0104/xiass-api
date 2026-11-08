import SwiftUI

struct DashboardView: View {
    @EnvironmentObject private var session: AppSession
    @EnvironmentObject private var feedback: FeedbackCenter

    @State private var stats: DashboardStats?
    @State private var realtime: RealtimeMetrics?
    @State private var groupCount = 0
    @State private var isLoading = true
    @State private var error: ErrorMessage?

    private let columns = [GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8)]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                serviceHeader

                if isLoading && stats == nil {
                    ProgressView("正在读取运行数据…")
                        .frame(maxWidth: .infinity, minHeight: 140)
                } else {
                    LazyVGrid(columns: columns, spacing: 8) {
                        metricLink(title: "上游账号", value: DisplayFormat.integer(stats?.totalAccounts), icon: "person.crop.rectangle.stack", tint: AppTheme.primary) { AccountsView() }
                        metricLink(title: "正常账号", value: DisplayFormat.integer(stats?.normalAccounts), icon: "checkmark.seal.fill", tint: .green) { AccountsView(initialFilter: .normal) }
                        metricLink(title: "分组", value: DisplayFormat.integer(groupCount), icon: "square.stack.3d.up.fill", tint: .indigo) { GroupsView() }
                        metricLink(title: "活跃用户", value: DisplayFormat.integer(stats?.activeUsers), icon: "person.2.fill", tint: AppTheme.accent) { UsersView(initialStatus: "active") }
                        metricLink(title: "今日请求", value: DisplayFormat.integer(stats?.todayRequests), icon: "arrow.left.arrow.right.circle.fill", tint: .cyan) { UsageLogsView() }
                        metricLink(title: "平均延迟", value: realtime?.averageResponseTime.map { "\(DisplayFormat.decimal($0, digits: 0)) ms" } ?? "--", icon: "timer", tint: .orange) { UsageLogsView() }
                    }
                    .buttonStyle(.plain)

                    HStack(spacing: 8) {
                        NavigationLink { UsageLogsView() } label: {
                            Label("最近调用", systemImage: "clock.arrow.circlepath").frame(maxWidth: .infinity)
                        }
                        NavigationLink { PaymentOrdersView() } label: {
                            Label("充值记录", systemImage: "creditcard.and.123").frame(maxWidth: .infinity)
                        }
                    }
                    .font(.footnote.weight(.semibold))
                    .buttonStyle(.bordered)
                    .tint(AppTheme.primary)

                    if let stats, (stats.ratelimitAccounts ?? 0) > 0 || (stats.overloadAccounts ?? 0) > 0 || (stats.errorAccounts ?? 0) > 0 {
                        NavigationLink { AccountsView(initialFilter: .attention) } label: {
                            GlassCard(tint: .orange) {
                                HStack(alignment: .center, spacing: 12) {
                                    GlassIcon(name: "exclamationmark.triangle.fill", tint: .orange)
                                    VStack(alignment: .leading, spacing: 3) {
                                        Text("调度需要关注").font(.headline)
                                        Text("异常 \(stats.errorAccounts ?? 0) · 限流 \(stats.ratelimitAccounts ?? 0) · 过载 \(stats.overloadAccounts ?? 0)")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer(minLength: 8)
                                    Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)
            .padding(.bottom, 108)
        }
        .appScreenStyle()
        .navigationTitle("XIASS API")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { Task { await load(notify: true) } } label: { Image(systemName: "arrow.clockwise") }
                    .accessibilityLabel("刷新概览")
            }
        }
        .task { await load() }
        .refreshable { await load(notify: true) }
        .requestError($error)
    }

    private var serviceHeader: some View {
        GlassCard(tint: AppTheme.primary) {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .center, spacing: 12) {
                    GlassIcon(name: "shield.checkered", tint: AppTheme.primary)
                    VStack(alignment: .leading, spacing: 3) {
                        Text("移动管理控制台")
                            .font(.headline)
                        Text(session.connectionAddress)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 8)
                    Label("已连接", systemImage: "checkmark.circle.fill")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.green)
                }
                HStack(spacing: 12) {
                    dashboardPulse("活跃请求", value: DisplayFormat.integer(realtime?.activeRequests))
                    dashboardPulse("每分钟", value: DisplayFormat.decimal(realtime?.requestsPerMinute, digits: 0))
                    dashboardPulse("错误率", value: realtime?.errorRate.map { "\(DisplayFormat.decimal($0, digits: 1))%" } ?? "--")
                }
            }
        }
    }

    private func dashboardPulse(_ title: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
            Text(value).font(.subheadline.monospacedDigit().weight(.semibold)).lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func metricLink<Destination: View>(
        title: String,
        value: String,
        icon: String,
        tint: Color,
        @ViewBuilder destination: () -> Destination
    ) -> some View {
        NavigationLink(destination: destination) {
            HStack(spacing: 9) {
                Image(systemName: icon)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(tint)
                    .frame(width: 30, height: 30)
                    .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                VStack(alignment: .leading, spacing: 2) {
                    Text(title).font(.caption2.weight(.medium)).foregroundStyle(.secondary).lineLimit(1)
                    Text(value).font(.subheadline.weight(.bold).monospacedDigit()).lineLimit(1).minimumScaleFactor(0.8)
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 9)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 11, style: .continuous))
        }
    }

    private func load(notify: Bool = false) async {
        guard let api = session.api else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            async let statsRequest: DashboardStats = api.request(method: .get, path: "admin/dashboard/stats")
            async let realtimeRequest: RealtimeMetrics = api.request(method: .get, path: "admin/dashboard/realtime")
            async let groupRequest: Page<AdminGroup> = api.request(method: .get, path: "admin/groups", query: [URLQueryItem(name: "page", value: "1"), URLQueryItem(name: "page_size", value: "1")])
            stats = try await statsRequest
            realtime = try await realtimeRequest
            groupCount = try await groupRequest.total
            if notify { feedback.success("概览已刷新", detail: "已同步最新账号、用户、分组和调用状态。") }
        } catch {
            self.error = ErrorMessage(error, title: "无法读取概览")
        }
    }
}
