import SwiftUI
import WidgetKit
import ActivityKit

/// HopDropWidgetBundle 是 Widget 扩展的入口，仅注册传输进度的 Live Activity。
@main
struct HopDropWidgetBundle: WidgetBundle {
    var body: some Widget {
        HopDropLiveActivityWidget()
    }
}

/// HopDropWidgetTheme 与 App 端 HopDropTheme 的米白配色保持一致（Widget 无法直接引用
/// App target 里的类型，故在此重申同一组色值）。
private enum HopDropWidgetTheme {
    static let accent = Color(red: 0x0C / 255, green: 0x8F / 255, blue: 0xA6 / 255)
    static let ink = Color(red: 0x24 / 255, green: 0x2A / 255, blue: 0x33 / 255)
    static let muted = Color(red: 0x8C / 255, green: 0x86 / 255, blue: 0x76 / 255)
    static let online = Color(red: 0x12 / 255, green: 0xA4 / 255, blue: 0x6E / 255)
}

/// HopDropLiveActivityWidget 定义灵动岛（紧凑/最小/展开）与锁屏横幅三种呈现。
struct HopDropLiveActivityWidget: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: HopDropActivityAttributes.self) { context in
            // —— 锁屏 / 横幅（不支持灵动岛的设备也走这里）——
            LockScreenView(state: context.state)
                .padding()
                .activityBackgroundTint(Color(red: 0xF5 / 255, green: 0xF1 / 255, blue: 0xE7 / 255))
        } dynamicIsland: { context in
            DynamicIsland {
                // —— 展开态（长按/下拉）——
                DynamicIslandExpandedRegion(.leading) {
                    Label {
                        Text(context.state.peerName)
                            .font(.caption)
                            .foregroundStyle(HopDropWidgetTheme.ink)
                            .lineLimit(1)
                    } icon: {
                        Image(systemName: iconName(context.state))
                            .foregroundStyle(HopDropWidgetTheme.accent)
                    }
                }
                DynamicIslandExpandedRegion(.trailing) {
                    Text(percentText(context.state))
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(HopDropWidgetTheme.accent)
                }
                DynamicIslandExpandedRegion(.center) {
                    Text(titleText(context.state))
                        .font(.caption2)
                        .foregroundStyle(HopDropWidgetTheme.muted)
                        .lineLimit(1)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    ProgressView(value: context.state.fraction)
                        .tint(HopDropWidgetTheme.accent)
                }
            } compactLeading: {
                Image(systemName: iconName(context.state))
                    .foregroundStyle(HopDropWidgetTheme.accent)
            } compactTrailing: {
                // 传输中显示环形进度，否则显示状态图标。
                if context.state.phase == "transfer" {
                    ProgressView(value: context.state.fraction)
                        .progressViewStyle(.circular)
                        .tint(HopDropWidgetTheme.accent)
                } else {
                    Text(shortStatus(context.state))
                        .font(.caption2)
                        .foregroundStyle(HopDropWidgetTheme.accent)
                }
            } minimal: {
                if context.state.phase == "transfer" {
                    ProgressView(value: context.state.fraction)
                        .progressViewStyle(.circular)
                        .tint(HopDropWidgetTheme.accent)
                } else {
                    Image(systemName: iconName(context.state))
                        .foregroundStyle(HopDropWidgetTheme.accent)
                }
            }
            .widgetURL(URL(string: "hopdrop://transfer"))
            .keylineTint(HopDropWidgetTheme.accent)
        }
    }
}

/// LockScreenView 是锁屏横幅：标题 + 对端 + 进度条 + 百分比。
private struct LockScreenView: View {
    let state: HopDropActivityAttributes.ContentState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: iconName(state))
                    .foregroundStyle(HopDropWidgetTheme.accent)
                Text("HopDrop · \(titleText(state))")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(HopDropWidgetTheme.ink)
                Spacer()
                Text(percentText(state))
                    .font(.subheadline.monospacedDigit())
                    .foregroundStyle(HopDropWidgetTheme.accent)
            }
            if state.phase == "transfer" {
                ProgressView(value: state.fraction)
                    .tint(HopDropWidgetTheme.accent)
                Text(subtitleText(state))
                    .font(.caption)
                    .foregroundStyle(HopDropWidgetTheme.muted)
                    .lineLimit(1)
            } else {
                Text(subtitleText(state))
                    .font(.caption)
                    .foregroundStyle(HopDropWidgetTheme.muted)
                    .lineLimit(1)
            }
        }
    }
}

// MARK: - 文案与图标（供各呈现共享）

private func iconName(_ s: HopDropActivityAttributes.ContentState) -> String {
    switch s.phase {
    case "done": return "checkmark.circle.fill"
    case "rejected": return "xmark.circle.fill"
    case "error": return "exclamationmark.triangle.fill"
    default: return s.direction == "send" ? "arrow.up.circle.fill" : "arrow.down.circle.fill"
    }
}

private func titleText(_ s: HopDropActivityAttributes.ContentState) -> String {
    switch s.phase {
    case "done": return "\(s.verb)完成"
    case "rejected": return "对方已拒绝"
    case "error": return "传输出错"
    case "transfer": return "正在\(s.verb)"
    default: return "\(s.verb)准备中"
    }
}

private func subtitleText(_ s: HopDropActivityAttributes.ContentState) -> String {
    switch s.phase {
    case "done": return "\(s.peerName) · 共 \(s.files) 个文件"
    case "rejected", "error": return s.peerName
    case "transfer": return "\(s.currentName)（\(s.files)/\(s.totalFiles)）"
    default: return s.peerName
    }
}

private func shortStatus(_ s: HopDropActivityAttributes.ContentState) -> String {
    switch s.phase {
    case "done": return "完成"
    case "rejected": return "拒绝"
    case "error": return "出错"
    default: return "…"
    }
}

private func percentText(_ s: HopDropActivityAttributes.ContentState) -> String {
    if s.phase == "done" { return "100%" }
    return "\(Int(s.fraction * 100))%"
}
