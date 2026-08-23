import Foundation
import HopDrop   // gomobile bind 生成的 HopDrop.xcframework

/// HopDropController 把 gomobile 生成的 MobileClient 封装成 iOS 侧更顺手的形态。
///
/// 使用步骤：
///   1. 用 scripts/build-ios.sh 生成 HopDrop.xcframework，拖入 Xcode 工程。
///   2. 在 Info.plist 声明本地网络与 Bonjour 权限（见文件末尾注释）。
///   3. 在 SwiftUI 中构造 HopDropController（@StateObject），绑定 UI。
///
/// 注意：Go 回调运行在非主线程，务必用 DispatchQueue.main.async 切回主线程再更新 @Published。
@MainActor
final class HopDropController: ObservableObject {
    @Published var peers: [PeerDevice] = []
    @Published var progress: ProgressInfo?
    /// 收到传输请求时置位，SwiftUI 据此弹窗；用户答复后调用 respond(...)。
    @Published var pendingOffer: (id: String, offer: OfferInfo)?

    private var client: MobileClient?

    func start() {
        let callback = CallbackImpl(owner: self)
        let sink = PhotoSink()   // 也可换成写入沙盒 Documents 的实现
        let idDir = NSSearchPathForDirectoriesInDomains(.applicationSupportDirectory, .userDomainMask, true).first ?? NSTemporaryDirectory()

        var err: NSError?
        let name = UIDevice.current.name
        let c = MobileNewClient(name, "ios", idDir, sink, callback, &err)
        guard let c = c, err == nil else {
            print("HopDrop start failed: \(String(describing: err))")
            return
        }
        self.client = c
        try? c.start(0)
    }

    func stop() { client?.stop() }

    func respond(offerId: String, accept: Bool) {
        client?.respond(offerId, accept: accept)
        pendingOffer = nil
    }

    /// 把一批照片/文件发送给某台设备（source 为宿主实现的取源）。
    func send(deviceId: String, source: MobileSource) {
        try? client?.send(toDevice: deviceId, src: source)
    }

    // MARK: - gomobile 回调桥接

    private final class CallbackImpl: NSObject, MobileCallback {
        weak var owner: HopDropController?
        init(owner: HopDropController) { self.owner = owner }

        func onPeers(_ peersJSON: String?) {
            guard let data = peersJSON?.data(using: .utf8),
                  let list = try? JSONDecoder().decode([PeerDevice].self, from: data) else { return }
            DispatchQueue.main.async { self.owner?.peers = list }
        }
        func onOffer(_ offerID: String?, offerJSON: String?) {
            guard let id = offerID, let data = offerJSON?.data(using: .utf8),
                  let offer = try? JSONDecoder().decode(OfferInfo.self, from: data) else { return }
            DispatchQueue.main.async { self.owner?.pendingOffer = (id, offer) }
        }
        func onProgress(_ progressJSON: String?) {
            guard let data = progressJSON?.data(using: .utf8),
                  let p = try? JSONDecoder().decode(ProgressInfo.self, from: data) else { return }
            DispatchQueue.main.async { self.owner?.progress = p }
        }
    }
}

// MARK: - DTO（与 mobile/dto.go 的 JSON 字段一一对应）

struct PeerDevice: Codable, Identifiable {
    let id: String
    let name: String
    let platform: String
    let sync_port: Int
}

struct FileMeta: Codable {
    let id: String
    let name: String
    let rel_path: String
    let size: Int64
    let mod_unix: Int64
    let mime_type: String?
}

struct OfferInfo: Codable {
    let peer: PeerDevice
    let files: [FileMeta]
    let total_bytes: Int64
}

struct ProgressInfo: Codable {
    let direction: String
    let peer_id: String
    let peer_name: String
    let phase: String
    let current_name: String
    let files: Int
    let total_files: Int
    let bytes: Int64
    let total_bytes: Int64
    let err: String?
}

/*
Info.plist 需要声明（iOS 14+ 访问本地网络需用户授权）：

    <key>NSLocalNetworkUsageDescription</key>
    <string>HopDrop 需要访问本地网络以发现附近设备并传输文件</string>
    <key>NSBonjourServices</key>
    <array>
        <string>_hopdrop._udp</string>
    </array>

组播地址 239.192.71.71:47771 属于本地网络，触发本地网络授权弹窗。
*/
