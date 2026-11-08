import SwiftUI
import UIKit

func isExpectedCancellation(_ error: Error) -> Bool {
    if error is CancellationError { return true }
    if let urlError = error as? URLError, urlError.code == .cancelled { return true }

    // URLSession may bridge a cancelled request through an NSError instead of
    // preserving the original URLError type. Search updates intentionally
    // replace the previous request, so neither representation is actionable.
    let nsError = error as NSError
    if nsError.domain == NSURLErrorDomain && nsError.code == NSURLErrorCancelled { return true }
    if let underlying = nsError.userInfo[NSUnderlyingErrorKey] as? Error,
       isExpectedCancellation(underlying) {
        return true
    }

    let description = error.localizedDescription
        .trimmingCharacters(in: .whitespacesAndNewlines)
        .lowercased()
    return description == "cancelled" || description == "canceled"
}

enum AppAppearance: String, CaseIterable, Identifiable {
    case system
    case light
    case dark

    var id: String { rawValue }
    var title: String {
        switch self {
        case .system: return "跟随系统"
        case .light: return "浅色"
        case .dark: return "深色"
        }
    }

    var colorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light: return .light
        case .dark: return .dark
        }
    }
}

enum AppTheme {
    static let primary = Color(red: 0.00, green: 0.42, blue: 0.76)
    static let accent = Color(red: 0.00, green: 0.49, blue: 0.35)
    static let panelRadius: CGFloat = 18
    static let compactRadius: CGFloat = 13
}

struct AppBackdrop: View {
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        LinearGradient(
            colors: colorScheme == .dark
                ? [Color(red: 0.025, green: 0.07, blue: 0.12), Color(red: 0.035, green: 0.13, blue: 0.19), Color(red: 0.025, green: 0.17, blue: 0.20)]
                : [Color(red: 0.86, green: 0.93, blue: 0.99), Color(red: 0.92, green: 0.97, blue: 1.0), Color(red: 0.84, green: 0.95, blue: 0.91)],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
        .overlay(alignment: .top) {
            LinearGradient(
                colors: [AppTheme.primary.opacity(colorScheme == .dark ? 0.24 : 0.16), .clear],
                startPoint: .top,
                endPoint: .bottom
            )
            .frame(height: 260)
        }
        .ignoresSafeArea()
    }
}

struct AppScreen<Content: View>: View {
    @AppStorage("xiass.appearance") private var appearanceRaw = AppAppearance.system.rawValue
    @ViewBuilder let content: Content

    private var appearance: AppAppearance { AppAppearance(rawValue: appearanceRaw) ?? .system }

    var body: some View {
        ZStack {
            AppBackdrop()
            content
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .tint(AppTheme.primary)
        .preferredColorScheme(appearance.colorScheme)
    }
}

struct GlassCard<Content: View>: View {
    @Environment(\.colorScheme) private var colorScheme
    let tint: Color
    let content: Content

    init(tint: Color = .white, @ViewBuilder content: () -> Content) {
        self.tint = tint
        self.content = content()
    }

    var body: some View {
        content
            .padding(16)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous))
            .background(tint.opacity(colorScheme == .dark ? 0.21 : 0.16), in: RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: AppTheme.panelRadius, style: .continuous)
                    .stroke(colorScheme == .dark ? .white.opacity(0.20) : .white.opacity(0.94), lineWidth: 1)
            }
            .shadow(color: .black.opacity(colorScheme == .dark ? 0.14 : 0.08), radius: 12, y: 6)
    }
}

struct GlassIcon: View {
    let name: String
    let tint: Color

    var body: some View {
        Image(systemName: name)
            .font(.system(size: 16, weight: .semibold))
            .foregroundStyle(tint)
            .frame(width: 38, height: 38)
            .background(tint.opacity(0.24), in: RoundedRectangle(cornerRadius: 13, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 13, style: .continuous)
                    .stroke(.white.opacity(0.16), lineWidth: 0.8)
            }
    }
}

struct GlassActionRow: View {
    let title: String
    let detail: String?
    let icon: String
    let tint: Color

    init(_ title: String, detail: String? = nil, icon: String, tint: Color = AppTheme.primary) {
        self.title = title
        self.detail = detail
        self.icon = icon
        self.tint = tint
    }

    var body: some View {
        HStack(spacing: 12) {
            GlassIcon(name: icon, tint: tint)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.body.weight(.semibold))
                if let detail, !detail.isEmpty {
                    Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            Image(systemName: "chevron.right")
                .font(.caption.weight(.bold))
                .foregroundStyle(.tertiary)
        }
        .contentShape(Rectangle())
    }
}

struct AdminListRowSurface<Content: View>: View {
    @Environment(\.colorScheme) private var colorScheme
    let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .padding(.horizontal, 12)
            .padding(.vertical, 11)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .stroke(colorScheme == .dark ? .white.opacity(0.12) : .black.opacity(0.06), lineWidth: 0.8)
            }
    }
}

struct CompactMetric: View {
    let title: String
    let value: String
    var tint: Color = .primary

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(value)
                .font(.footnote.weight(.semibold).monospacedDigit())
                .foregroundStyle(tint)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

extension View {
    func appScreenStyle() -> some View {
        background {
            AppBackdrop()
                .contentShape(Rectangle())
                .onTapGesture { dismissActiveKeyboard() }
        }
            .scrollContentBackground(.hidden)
            .scrollDismissesKeyboard(.interactively)
            .toolbarBackground(.clear, for: .navigationBar)
    }

    func dismissKeyboardOnTap() -> some View {
        scrollDismissesKeyboard(.interactively)
            .background {
                Color.clear
                    .contentShape(Rectangle())
                    .onTapGesture { dismissActiveKeyboard() }
            }
    }
}

private func dismissActiveKeyboard() {
    UIApplication.shared.sendAction(
        #selector(UIResponder.resignFirstResponder),
        to: nil,
        from: nil,
        for: nil
    )
}

enum DisplayFormat {
    static func integer(_ value: Int?) -> String {
        guard let value else { return "--" }
        return value.formatted(.number.grouping(.automatic))
    }

    static func decimal(_ value: Double?, digits: Int = 2) -> String {
        guard let value else { return "--" }
        return value.formatted(.number.precision(.fractionLength(digits)))
    }

    static func currency(_ value: Double?) -> String {
        guard let value else { return "--" }
        return "¥\(value.formatted(.number.precision(.fractionLength(2))))"
    }

    static func shortDate(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "--" }
        return String(value.prefix(16)).replacingOccurrences(of: "T", with: " ")
    }
}

struct StatusPill: View {
    let text: String

    private var color: Color {
        switch text.lowercased() {
        case "active", "normal", "healthy", "success", "paid", "completed", "接收成功": return .green
        case "error", "failed", "disabled", "cancelled", "expired", "连接失败", "已超时": return .red
        case "paused", "inactive", "rate limited", "overloaded", "pending", "recharging", "refunding", "暂无卡密": return .orange
        case "实时监听", "正在取号": return AppTheme.primary
        default: return .secondary
        }
    }

    private var displayText: String {
        switch text.lowercased() {
        case "active", "normal", "healthy", "success": return "正常"
        case "error", "failed": return "异常"
        case "disabled": return "停用"
        case "paused", "inactive": return "暂停"
        case "rate limited": return "限流"
        case "overloaded": return "过载"
        case "pending": return "待支付"
        case "paid": return "已支付"
        case "recharging": return "充值中"
        case "completed": return "已完成"
        case "cancelled": return "已取消"
        case "expired": return "已过期"
        case "refunding": return "退款中"
        default: return text
        }
    }

    var body: some View {
        Text(displayText)
            .font(.caption.weight(.semibold))
            .foregroundStyle(color)
            .lineLimit(1)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(color.opacity(0.14), in: Capsule())
    }
}

struct MetricTile: View {
    let title: String
    let value: String
    let systemImage: String
    let tint: Color

    var body: some View {
        GlassCard(tint: tint) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 8) {
                    GlassIcon(name: systemImage, tint: tint)
                    Text(title)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.primary.opacity(0.78))
                        .lineLimit(1)
                    Spacer(minLength: 0)
                }
                Text(value)
                    .font(.system(.title3, design: .rounded, weight: .bold))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
            }
            .frame(maxWidth: .infinity, minHeight: 88, alignment: .leading)
        }
    }
}

struct AdminTableSurface<Content: View>: View {
    @Environment(\.colorScheme) private var colorScheme
    let minWidth: CGFloat
    let content: Content

    init(minWidth: CGFloat, @ViewBuilder content: () -> Content) {
        self.minWidth = minWidth
        self.content = content()
    }

    var body: some View {
        ScrollView(.horizontal, showsIndicators: true) {
            LazyVStack(alignment: .leading, spacing: 0) {
                content
            }
            .frame(minWidth: minWidth, alignment: .leading)
        }
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous)
                .stroke(colorScheme == .dark ? .white.opacity(0.16) : .white.opacity(0.88), lineWidth: 1)
        }
        .clipShape(RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous))
    }
}

struct AdminTableHeader<Content: View>: View {
    let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 14)
            .frame(height: 38)
            .background(AppTheme.primary.opacity(0.08))
    }
}

struct AdminTableRow<Content: View>: View {
    @Environment(\.colorScheme) private var colorScheme
    let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .padding(.horizontal, 14)
            .frame(minHeight: 54)
            .contentShape(Rectangle())
            .overlay(alignment: .bottom) {
                Divider().opacity(colorScheme == .dark ? 0.34 : 0.58)
            }
    }
}

struct AdminTableText: View {
    let text: String
    let width: CGFloat
    var alignment: Alignment = .leading
    var color: Color? = nil
    var weight: Font.Weight = .regular

    var body: some View {
        Text(text)
            .font(.subheadline.weight(weight))
            .foregroundStyle(color ?? .primary)
            .lineLimit(1)
            .truncationMode(.tail)
            .frame(width: width, alignment: alignment)
    }
}

struct AdminTableHeaderText: View {
    let text: String
    let width: CGFloat
    var alignment: Alignment = .leading

    var body: some View {
        Text(text)
            .lineLimit(1)
            .frame(width: width, alignment: alignment)
    }
}

struct PlatformBadge: View {
    let platform: String

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: PlatformStyle.icon(for: platform))
            Text(PlatformStyle.title(for: platform))
        }
        .font(.caption.weight(.bold))
        .foregroundStyle(PlatformStyle.color(for: platform))
        .lineLimit(1)
    }
}

enum PlatformStyle {
    static func title(for value: String) -> String {
        switch value.lowercased() {
        case "openai": return "OpenAI"
        case "anthropic": return "Claude"
        case "gemini": return "Gemini"
        case "antigravity": return "Antigravity"
        case "grok": return "Grok"
        case "composite": return "组合"
        default: return value.isEmpty ? "其他" : value.uppercased()
        }
    }

    static func icon(for value: String) -> String {
        switch value.lowercased() {
        case "openai": return "circle.hexagongrid.fill"
        case "anthropic": return "text.book.closed.fill"
        case "gemini": return "sparkles"
        case "antigravity": return "bolt.horizontal.circle.fill"
        case "grok": return "xmark.circle.fill"
        case "composite": return "point.3.connected.trianglepath.dotted"
        default: return "circle.fill"
        }
    }

    static func color(for value: String) -> Color {
        switch value.lowercased() {
        case "anthropic": return .orange
        case "gemini": return .teal
        case "antigravity": return .indigo
        case "grok": return .pink
        case "composite": return .purple
        default: return AppTheme.primary
        }
    }
}

struct DecimalInput: View {
    let label: String
    @Binding var value: Double
    let range: ClosedRange<Double>
    let step: Double
    var suffix: String? = nil
    @State private var draft = ""
    @FocusState private var isFocused: Bool

    var body: some View {
        HStack(spacing: 10) {
            Text(label)
            Spacer(minLength: 8)
            Button { adjust(by: -step) } label: {
                Image(systemName: "minus")
            }
            .buttonStyle(.borderless)
            .disabled(value <= range.lowerBound)
            TextField(label, text: $draft)
                .keyboardType(.decimalPad)
                .multilineTextAlignment(.trailing)
                .font(.body.monospacedDigit())
                .focused($isFocused)
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous)
                        .stroke(isFocused ? AppTheme.primary.opacity(0.8) : .secondary.opacity(0.22), lineWidth: isFocused ? 1.3 : 0.8)
                }
                .frame(minWidth: 92, maxWidth: 122)
                .accessibilityLabel(label)
                .onChange(of: draft) { next in
                    updateValue(from: next)
                }
                .onChange(of: isFocused) { focused in
                    if !focused { commit() }
                }
                .onChange(of: value) { _ in
                    if !isFocused { draft = formattedValue }
                }
                .onAppear { draft = formattedValue }
            if let suffix { Text(suffix).foregroundStyle(.secondary) }
            Button { adjust(by: step) } label: {
                Image(systemName: "plus")
            }
            .buttonStyle(.borderless)
            .disabled(value >= range.upperBound)
        }
    }

    private var formattedValue: String {
        value.formatted(.number.precision(.fractionLength(0...4)))
    }

    private func updateValue(from text: String) {
        guard let parsed = Self.parse(text) else { return }
        value = min(max(parsed, range.lowerBound), range.upperBound)
    }

    private func commit() {
        guard let parsed = Self.parse(draft) else {
            draft = formattedValue
            return
        }
        value = min(max(parsed, range.lowerBound), range.upperBound)
        draft = formattedValue
    }

    private func adjust(by amount: Double) {
        isFocused = false
        value = min(max(value + amount, range.lowerBound), range.upperBound)
        draft = formattedValue
    }

    private static func parse(_ text: String) -> Double? {
        let normalized = text
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: "，", with: ",")
        guard !normalized.isEmpty else { return nil }
        let formatter = NumberFormatter()
        formatter.numberStyle = .decimal
        return formatter.number(from: normalized)?.doubleValue ?? Double(normalized)
    }
}

struct IntegerInput: View {
    let label: String
    @Binding var value: Int
    let range: ClosedRange<Int>
    let step: Int
    var zeroLabel: String? = nil
    var suffix: String? = nil
    @State private var draft = ""
    @FocusState private var isFocused: Bool

    var body: some View {
        HStack(spacing: 10) {
            Text(label)
            Spacer(minLength: 8)
            Button { adjust(by: -step) } label: { Image(systemName: "minus") }
                .buttonStyle(.borderless)
                .disabled(value <= range.lowerBound)
            TextField(label, text: $draft)
                .keyboardType(.numberPad)
                .multilineTextAlignment(.trailing)
                .font(.body.monospacedDigit())
                .focused($isFocused)
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous))
                .overlay {
                    RoundedRectangle(cornerRadius: AppTheme.compactRadius, style: .continuous)
                        .stroke(isFocused ? AppTheme.primary.opacity(0.8) : .secondary.opacity(0.22), lineWidth: isFocused ? 1.3 : 0.8)
                }
                .frame(minWidth: 82, maxWidth: 112)
                .accessibilityLabel(label)
                .onChange(of: draft) { next in
                    updateValue(from: next)
                }
                .onChange(of: isFocused) { focused in
                    if !focused { commit() }
                }
                .onChange(of: value) { _ in
                    if !isFocused { draft = formattedValue }
                }
                .onAppear { draft = formattedValue }
            if value == 0, let zeroLabel {
                Text(zeroLabel).foregroundStyle(.secondary)
            }
            if let suffix { Text(suffix).foregroundStyle(.secondary) }
            Button { adjust(by: step) } label: { Image(systemName: "plus") }
                .buttonStyle(.borderless)
                .disabled(value >= range.upperBound)
        }
    }

    private var formattedValue: String {
        value.formatted(.number.grouping(.never))
    }

    private func updateValue(from text: String) {
        guard let parsed = Self.parse(text) else { return }
        value = min(max(parsed, range.lowerBound), range.upperBound)
    }

    private func commit() {
        guard let parsed = Self.parse(draft) else {
            draft = formattedValue
            return
        }
        value = min(max(parsed, range.lowerBound), range.upperBound)
        draft = formattedValue
    }

    private func adjust(by amount: Int) {
        isFocused = false
        value = min(max(value + amount, range.lowerBound), range.upperBound)
        draft = formattedValue
    }

    private static func parse(_ text: String) -> Int? {
        let normalized = text
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .replacingOccurrences(of: ",", with: "")
            .replacingOccurrences(of: "，", with: "")
        return Int(normalized)
    }
}

struct EmptyState: View {
    let title: String
    let systemImage: String
    let detail: String

    var body: some View {
        GlassCard(tint: AppTheme.primary) {
            VStack(spacing: 12) {
                GlassIcon(name: systemImage, tint: AppTheme.primary)
                Text(title).font(.headline)
                Text(detail).font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            .padding(8)
            .frame(maxWidth: .infinity, minHeight: 220)
        }
        .padding()
    }
}
