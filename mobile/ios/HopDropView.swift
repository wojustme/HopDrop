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
    /// 控制配对面板（本机二维码 + 扫码）的呈现。
    @State private var showPairing = false
    /// 扫码/配对得到的对端端点；非 nil 时拉起文件选择并直连发送。
    @State private var pendingEndpoint: String?
    @State private var pendingFingerprint: String?
    /// 直连发送用的文件选择器开关（与设备列表发送分开，避免目标混淆）。
    @State private var showEndpointImporter = false

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

                // —— 本机设备名卡片 ——
                HStack(spacing: 10) {
                    Image(systemName: "iphone")
                        .font(.system(size: 20))
                        .foregroundColor(HopDropTheme.accent)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(controller.selfName)
                            .font(.system(size: 15, weight: .bold))
                            .foregroundColor(HopDropTheme.ink)
                            .lineLimit(1)
                        Text("本机 · \(controller.selfPlatform)")
                            .font(.system(size: 12))
                            .foregroundColor(HopDropTheme.muted)
                    }
                    Spacer()
                    Button {
                        showPairing = true
                    } label: {
                        Image(systemName: "qrcode")
                            .font(.system(size: 22))
                            .foregroundColor(HopDropTheme.accent)
                    }
                }
                .padding(14)
                .background(HopDropTheme.surface)
                .clipShape(RoundedRectangle(cornerRadius: 14))

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

                // —— 操作按钮 ——
                HStack(spacing: 12) {
                    Button(action: onSendTapped) {
                        Text("发送文件")
                            .font(.system(size: 16, weight: .medium))
                            .foregroundColor(HopDropTheme.surface)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 15)
                            .background(HopDropTheme.accent)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                    }
                    Button {
                        showPairing = true
                    } label: {
                        Image(systemName: "qrcode.viewfinder")
                            .font(.system(size: 18, weight: .medium))
                            .foregroundColor(HopDropTheme.accent)
                            .frame(width: 54)
                            .padding(.vertical, 15)
                            .background(HopDropTheme.input)
                            .clipShape(RoundedRectangle(cornerRadius: 12))
                    }
                }
            }
            .padding(20)

            // —— 全屏传输遮罩：传输/接收进行中及短暂终态时盖住整个界面 ——
            if let t = controller.transfer {
                TransferOverlay(info: t)
                    .transition(.opacity)
                    .zIndex(1)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: controller.transfer?.phase)
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
        // 扫码/配对后的直连发送：选中文件后走 sendToEndpoint。
        .fileImporter(
            isPresented: $showEndpointImporter,
            allowedContentTypes: [.item],
            allowsMultipleSelection: true
        ) { result in
            guard let ep = pendingEndpoint, let fingerprint = pendingFingerprint,
                  case let .success(urls) = result, !urls.isEmpty else { return }
            controller.sendToEndpoint(ep, fingerprint: fingerprint, urls: urls)
            pendingEndpoint = nil
            pendingFingerprint = nil
        }
        // 配对面板：展示本机二维码 + 扫码入口。
        .sheet(isPresented: $showPairing) {
            PairingSheet(controller: controller) { code in
                // 扫到对端配对串：把对端登记为在线设备并选中，进入正常发送流程。
                // 这样即便对端（如桌面）随后重启，只要 IP:port 不变仍可直接发送。
                if let info = HopDropPairing.info(from: code) {
                    controller.registerScanned(info)
                    pendingEndpoint = info.endpoint
                    pendingFingerprint = info.fingerprint
                    // 稍延迟以等配对 sheet 完成关闭动画，再拉起文件选择器。
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
                        showEndpointImporter = true
                    }
                }
            }
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
            set: { visible in
                if !visible, let pending = controller.pendingOffer {
                    controller.respond(offerId: pending.id, accept: false)
                }
            }
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

/// TransferOverlay 是全屏传输遮罩：半透明蒙层 + 居中卡片，展示进度环、文件名与统计。
/// 传输/接收进行中盖住整个界面，避免用户误操作；完成/被拒/出错短暂停留后自动收起。
private struct TransferOverlay: View {
    let info: ProgressInfo

    private var verb: String { info.direction == "send" ? "发送" : "接收" }

    private var fraction: Double {
        if info.total_bytes > 0 {
            return min(1, max(0, Double(info.bytes) / Double(info.total_bytes)))
        }
        if info.total_files > 0 {
            return min(1, max(0, Double(info.files) / Double(info.total_files)))
        }
        return 0
    }

    private var isTerminal: Bool {
        info.phase == "done" || info.phase == "rejected" || info.phase == "error"
    }

    private var title: String {
        switch info.phase {
        case "done": return "\(verb)完成"
        case "rejected": return "对方已拒绝"
        case "error": return "传输出错"
        case "transfer": return "正在\(verb)"
        default: return "\(verb)准备中…"
        }
    }

    private var subtitle: String {
        switch info.phase {
        case "done": return "共 \(info.files) 个文件 · \(humanBytes(info.bytes))"
        case "rejected": return info.peer_name
        case "error": return info.err ?? ""
        case "transfer":
            return "\(info.current_name)（\(info.files)/\(info.total_files)）"
        default: return info.peer_name
        }
    }

    private var iconName: String {
        switch info.phase {
        case "done": return "checkmark.circle.fill"
        case "rejected": return "xmark.circle.fill"
        case "error": return "exclamationmark.triangle.fill"
        default: return info.direction == "send" ? "arrow.up.circle.fill" : "arrow.down.circle.fill"
        }
    }

    private var tint: Color {
        switch info.phase {
        case "done": return HopDropTheme.online
        case "rejected", "error": return Color(red: 0xD6 / 255, green: 0x3B / 255, blue: 0x5A / 255)
        default: return HopDropTheme.accent
        }
    }

    var body: some View {
        ZStack {
            // 半透明蒙层，拦截点击。
            Color.black.opacity(0.45)
                .ignoresSafeArea()
                .contentShape(Rectangle())
                .onTapGesture {} // 吞掉点击，禁止穿透

            VStack(spacing: 18) {
                // 进度环 / 终态图标。
                ZStack {
                    if isTerminal {
                        Image(systemName: iconName)
                            .font(.system(size: 54))
                            .foregroundColor(tint)
                    } else {
                        Circle()
                            .stroke(HopDropTheme.separator, lineWidth: 8)
                            .frame(width: 96, height: 96)
                        Circle()
                            .trim(from: 0, to: max(0.02, fraction))
                            .stroke(tint, style: StrokeStyle(lineWidth: 8, lineCap: .round))
                            .frame(width: 96, height: 96)
                            .rotationEffect(.degrees(-90))
                            .animation(.easeInOut(duration: 0.25), value: fraction)
                        Image(systemName: iconName)
                            .font(.system(size: 30))
                            .foregroundColor(tint)
                    }
                }
                .frame(width: 100, height: 100)

                VStack(spacing: 6) {
                    Text(title)
                        .font(.system(size: 19, weight: .bold))
                        .foregroundColor(HopDropTheme.ink)
                    if !info.peer_name.isEmpty && info.phase != "rejected" {
                        Text(info.peer_name)
                            .font(.system(size: 13))
                            .foregroundColor(HopDropTheme.muted)
                    }
                    if !subtitle.isEmpty {
                        Text(subtitle)
                            .font(.system(size: 13))
                            .foregroundColor(HopDropTheme.muted)
                            .multilineTextAlignment(.center)
                            .lineLimit(2)
                    }
                }

                if !isTerminal {
                    // 线性进度条 + 百分比。
                    VStack(spacing: 6) {
                        ProgressView(value: fraction)
                            .tint(tint)
                        HStack {
                            Text("\(Int(fraction * 100))%")
                                .font(.system(size: 12, weight: .medium).monospacedDigit())
                                .foregroundColor(tint)
                            Spacer()
                            Text("\(humanBytes(info.bytes)) / \(humanBytes(info.total_bytes))")
                                .font(.system(size: 12).monospacedDigit())
                                .foregroundColor(HopDropTheme.muted)
                        }
                    }
                }
            }
            .padding(28)
            .frame(maxWidth: 300)
            .background(HopDropTheme.surface)
            .clipShape(RoundedRectangle(cornerRadius: 20))
            .shadow(color: .black.opacity(0.15), radius: 20, y: 8)
        }
    }
}
