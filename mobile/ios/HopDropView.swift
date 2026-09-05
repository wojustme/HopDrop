import SwiftUI
import UniformTypeIdentifiers

/// HopDropTheme 定义 HopDrop iOS 端的米白配色，与桌面端 hopTheme / Android 保持一致：
///   - 背景用温润米白，卡片用更亮的暖白拉出层次；
///   - 深青 teal 作强调色，深墨蓝灰作正文，冷暖平衡、对比清晰。
enum HopDropTheme {
    static let background = Color(red: 0xF5 / 255, green: 0xF1 / 255, blue: 0xE7 / 255)
    static let surface = Color(red: 0xFC / 255, green: 0xFA / 255, blue: 0xF4 / 255)
    static let input = Color(red: 0xEE / 255, green: 0xE8 / 255, blue: 0xD9 / 255)
    static let inputSelected = Color(red: 0xD4 / 255, green: 0xEB / 255, blue: 0xF0 / 255)
    static let accent = Color(red: 0x0C / 255, green: 0x8F / 255, blue: 0xA6 / 255)
    static let ink = Color(red: 0x24 / 255, green: 0x2A / 255, blue: 0x33 / 255)
    static let muted = Color(red: 0x8C / 255, green: 0x86 / 255, blue: 0x76 / 255)
    static let separator = Color(red: 0xE2 / 255, green: 0xDB / 255, blue: 0xC9 / 255)
    static let online = Color(red: 0x12 / 255, green: 0xA4 / 255, blue: 0x6E / 255)
}

/// HopDropView 是米白主题的 iOS 参考界面：品牌头 + 状态条 + 在线设备卡片列表 + 发送按钮。
///
/// 支持双向传输：点选一台设备后点“发送文件”拉起系统文件选择器挑文件推送；
/// 收到传输请求时弹窗询问接受/拒绝。
struct HopDropView: View {
    @StateObject private var controller = HopDropController()
    /// 控制系统文件选择器的呈现。
    @State private var showImporter = false

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
                Button(action: onSendTapped) {
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
        // 系统文件选择器：可多选任意类型文件，选完交给 controller.send(...)。
        .fileImporter(
            isPresented: $showImporter,
            allowedContentTypes: [.item],
            allowsMultipleSelection: true
        ) { result in
            guard let deviceId = controller.selectedId,
                  case let .success(urls) = result, !urls.isEmpty else { return }
            controller.send(deviceId: deviceId, urls: urls)
        }
        // 入站传输确认弹窗，替代此前的“默认接受”。
        .alert("收到文件传输", isPresented: offerBinding, presenting: controller.pendingOffer) { item in
            Button("接收") { controller.respond(offerId: item.id, accept: true) }
            Button("拒绝", role: .cancel) { controller.respond(offerId: item.id, accept: false) }
        } message: { item in
            Text("\(item.offer.peer.name) 想给你发送 \(item.offer.files.count) 个文件"
                + "（共 \(humanBytes(item.offer.total_bytes))）。\n接收后将保存到「文件」App 的 HopDrop 目录。")
        }
    }

    /// 把 controller 的元组型 pendingOffer 适配成 alert 需要的 Bool 绑定。
    private var offerBinding: Binding<Bool> {
        Binding(
            get: { controller.pendingOffer != nil },
            set: { if !$0 { controller.pendingOffer = nil } }
        )
    }

    private func onSendTapped() {
        guard controller.selectedId != nil else { return }
        showImporter = true
    }

    private var statusText: String {
        guard controller.selectedId != nil else {
            if let p = controller.progress { return progressText(p) }
            return "请先选择一台设备"
        }
        if let p = controller.progress { return progressText(p) }
        return "已选择设备 · 可发送文件"
    }

    private func progressText(_ p: ProgressInfo) -> String {
        let verb = p.direction == "send" ? "发送" : "接收"
        switch p.phase {
        case "transfer": return "\(verb) \(p.files)/\(p.total_files) · \(p.current_name)"
        case "done": return "\(verb)完成 · \(p.files) 个文件"
        case "rejected": return "对方拒绝了本次传输"
        case "error": return "出错: \(p.err ?? "")"
        default: return "\(verb)中…"
        }
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
                        HopDropPeerRow(peer: peer, selected: peer.id == controller.selectedId)
                            .contentShape(Rectangle())
                            .onTapGesture { controller.selectedId = peer.id }
                    }
                }
                .padding(8)
            }
            .background(HopDropTheme.surface)
            .clipShape(RoundedRectangle(cornerRadius: 14))
        }
    }
}

/// HopDropPeerRow 是单台设备的卡片行：状态点 + 名称 + 平台标签；点击可选中为发送目标。
private struct HopDropPeerRow: View {
    let peer: PeerDevice
    let selected: Bool

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(HopDropTheme.online)
                .frame(width: 9, height: 9)
            Text(peer.name)
                .font(.system(size: 15, weight: .bold))
                .foregroundColor(HopDropTheme.ink)
            Spacer()
            Text(selected ? "已选 · \(peer.platform)" : peer.platform)
                .font(.system(size: 13))
                .foregroundColor(selected ? HopDropTheme.accent : HopDropTheme.muted)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .background(selected ? HopDropTheme.inputSelected : HopDropTheme.input)
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(selected ? HopDropTheme.accent : HopDropTheme.separator, lineWidth: 1)
        )
        .clipShape(RoundedRectangle(cornerRadius: 10))
    }
}

/// humanBytes 把字节数格式化为可读字符串。
private func humanBytes(_ n: Int64) -> String {
    if n < 1024 { return "\(n) B" }
    let units = ["KB", "MB", "GB", "TB"]
    var v = Double(n) / 1024
    var i = 0
    while v >= 1024 && i < units.count - 1 { v /= 1024; i += 1 }
    return String(format: "%.1f %@", v, units[i])
}
