import SwiftUI

/// HopDropTheme 定义 HopDrop iOS 端的米白配色，与桌面端 hopTheme / Android 保持一致：
///   - 背景用温润米白，卡片用更亮的暖白拉出层次；
///   - 深青 teal 作强调色，深墨蓝灰作正文，冷暖平衡、对比清晰。
enum HopDropTheme {
    static let background = Color(red: 0xF5 / 255, green: 0xF1 / 255, blue: 0xE7 / 255)
    static let surface = Color(red: 0xFC / 255, green: 0xFA / 255, blue: 0xF4 / 255)
    static let input = Color(red: 0xEE / 255, green: 0xE8 / 255, blue: 0xD9 / 255)
    static let accent = Color(red: 0x0C / 255, green: 0x8F / 255, blue: 0xA6 / 255)
    static let ink = Color(red: 0x24 / 255, green: 0x2A / 255, blue: 0x33 / 255)
    static let muted = Color(red: 0x8C / 255, green: 0x86 / 255, blue: 0x76 / 255)
    static let separator = Color(red: 0xE2 / 255, green: 0xDB / 255, blue: 0xC9 / 255)
    static let online = Color(red: 0x12 / 255, green: 0xA4 / 255, blue: 0x6E / 255)
}

/// HopDropView 是米白主题的 iOS 参考界面：品牌头 + 状态条 + 在线设备卡片列表。
///
/// 使用方式：在 App 入口用 `HopDropView()`（内部持有 HopDropController 作为 @StateObject），
/// 或把已有的 controller 传入。这里演示自持有的最简形态。
struct HopDropView: View {
    @StateObject private var controller = HopDropController()

    var body: some View {
        ZStack {
            HopDropTheme.background.ignoresSafeArea()

            VStack(alignment: .leading, spacing: 16) {
                // —— 品牌头 ——
                VStack(alignment: .leading, spacing: 2) {
                    Text("HopDrop")
                        .font(.system(size: 30, weight: .bold))
                        .foregroundColor(HopDropTheme.accent)
                    Text("局域网 · 极速互传")
                        .font(.system(size: 13))
                        .foregroundColor(HopDropTheme.muted)
                }

                // —— 状态条 ——
                HStack(spacing: 10) {
                    Circle()
                        .fill(HopDropTheme.accent)
                        .frame(width: 10, height: 10)
                    Text(statusText)
                        .font(.system(size: 15))
                        .foregroundColor(HopDropTheme.ink)
                    Spacer()
                }
                .padding(16)
                .background(HopDropTheme.surface)
                .clipShape(RoundedRectangle(cornerRadius: 14))

                // —— 在线设备 ——
                Text("在线设备（\(controller.peers.count)）")
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(HopDropTheme.ink)

                deviceList

                // —— 发送按钮 ——
                Button(action: { /* 选取文件后 controller.send(...) */ }) {
                    Text("发送文件")
                        .font(.system(size: 16, weight: .medium))
                        .foregroundColor(HopDropTheme.surface)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 15)
                        .background(HopDropTheme.accent)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                }
            }
            .padding(20)
        }
        .onAppear { controller.start() }
        .onDisappear { controller.stop() }
    }

    private var statusText: String {
        if let p = controller.progress {
            return "\(p.phase) \(p.files)/\(p.total_files)"
        }
        return "就绪 · 等待设备接入"
    }

    @ViewBuilder private var deviceList: some View {
        if controller.peers.isEmpty {
            VStack(spacing: 4) {
                Text("正在扫描局域网设备…")
                    .font(.system(size: 14))
                    .foregroundColor(HopDropTheme.muted)
                Text("确保设备处于同一 Wi-Fi 网络")
                    .font(.system(size: 12))
                    .foregroundColor(HopDropTheme.muted)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(HopDropTheme.surface)
            .clipShape(RoundedRectangle(cornerRadius: 14))
        } else {
            ScrollView {
                VStack(spacing: 8) {
                    ForEach(controller.peers) { peer in
                        HopDropPeerRow(peer: peer)
                    }
                }
                .padding(8)
            }
            .background(HopDropTheme.surface)
            .clipShape(RoundedRectangle(cornerRadius: 14))
        }
    }
}

/// HopDropPeerRow 是单台设备的卡片行：状态点 + 名称 + 平台标签。
private struct HopDropPeerRow: View {
    let peer: PeerDevice

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(HopDropTheme.online)
                .frame(width: 9, height: 9)
            Text(peer.name)
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(HopDropTheme.ink)
            Spacer()
            Text(peer.platform)
                .font(.system(size: 13))
                .foregroundColor(HopDropTheme.muted)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .background(HopDropTheme.input)
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(HopDropTheme.separator, lineWidth: 1)
        )
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }
}
