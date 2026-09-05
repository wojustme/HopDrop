import Foundation
import UIKit
import UniformTypeIdentifiers
import HopDrop   // gomobile bind 生成的 HopDrop.xcframework

/// HopDropController 把 gomobile 生成的 MobileClient 封装成 iOS 侧更顺手的形态。
///
/// 使用步骤：
///   1. 用 scripts/build-ios.sh 生成 HopDrop.xcframework，拖入 Xcode 工程。
///   2. 在 Info.plist 声明本地网络与 Bonjour 权限（见文件末尾注释）。
///   3. 在 SwiftUI 中构造 HopDropController（@StateObject），绑定 UI。
///
/// 支持双向传输：
///   - 接收：DocumentSink 把对端推来的文件写入 App 的 Documents/HopDrop 目录；
///   - 发送：选中设备后用系统文件选择器挑文件，FileSource 流式读出并推送。
///
/// 注意：Go 回调运行在非主线程，务必用 DispatchQueue.main.async 切回主线程再更新 @Published。
@MainActor
final class HopDropController: ObservableObject {
    @Published var peers: [PeerDevice] = []
    @Published var progress: ProgressInfo?
    /// 本机设备名，用于界面展示（iOS 取系统设备名）。
    @Published var selfName: String = UIDevice.current.name
    /// 本机平台，固定 "ios"。
    @Published var selfPlatform: String = "ios"
    /// 本机配对串（hopdrop://host:port?...），用于生成二维码；无网络时为 nil。
    @Published var pairingURI: String?
    /// 非空时展示全屏传输遮罩（发送/接收进行中及其短暂的终态）。
    @Published var transfer: ProgressInfo?
    /// 收到传输请求时置位，SwiftUI 据此弹窗；用户答复后调用 respond(...)。
    @Published var pendingOffer: (id: String, offer: OfferInfo)?
    /// 当前选中的目标设备 ID（发送时定位对端）。
    @Published var selectedId: String?

    private var client: MobileClient?
    /// 持有 sink，避免被 ARC 回收（Go 侧只持有代理引用）。
    private var sink: DocumentSink?
    /// 把传输进度投射到灵动岛 / 锁屏 Live Activity。
    private let liveActivity = HopDropLiveActivity()
    /// 终态遮罩的延时收起任务，便于被下一条进度取消。
    private var dismissTask: Task<Void, Never>?
    /// 原生 Bonjour 发现（NWBrowser/NWListener），把发现到的 peer 喂给 Go 侧。
    private var bonjour: HopDropBonjour?

    func start() {
        let callback = CallbackImpl(owner: self)
        let sink = DocumentSink()
        self.sink = sink
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
        self.selfName = c.selfName()
        self.selfPlatform = c.selfPlatform()
        refreshPairingURI()
        startBonjour(client: c)
    }

    /// startBonjour 启动原生 Bonjour 发现，并把结果喂进 MobileClient。
    private func startBonjour(client c: MobileClient) {
        // 用 Go 侧的稳定设备 ID，保证与对端看到的一致、且能过滤掉自己。
        let selfID = (try? JSONDecoder().decode(PeerDevice.self,
            from: Data(c.selfJSON().utf8)))?.id ?? UUID().uuidString
        let b = HopDropBonjour(
            selfID: selfID,
            selfName: c.selfName(),
            selfPlatform: c.selfPlatform(),
            port: c.selfSyncPort()
        )
        b.onResolved = { [weak self] id, name, platform, host, port in
            self?.client?.addDiscoveredPeer(id, name: name, platform: platform, addr: host, port: port)
        }
        b.onRemoved = { [weak self] id in
            self?.client?.removeDiscoveredPeer(id)
        }
        b.start()
        self.bonjour = b
    }

    /// refreshPairingURI 重新计算本机配对串（网络切换后可再调用刷新二维码）。
    func refreshPairingURI() {
        let uri = client?.pairingURI() ?? ""
        pairingURI = uri.isEmpty ? nil : uri
    }

    /// 直连一个 "host:port" 端点发送（扫码/手动配对场景）。
    func sendToEndpoint(_ endpoint: String, urls: [URL]) {
        guard let client = client else { return }
        DispatchQueue.global(qos: .userInitiated).async {
            let source = FileSource(urls: urls)
            do {
                try client.send(toEndpoint: endpoint, src: source)
            } catch {
                print("HopDrop sendToEndpoint failed: \(error)")
            }
            _ = source
        }
    }

    /// registerScanned 把扫码得到的对端登记为一台在线设备，进入设备列表并选中，
    /// 之后走正常「选设备 → 发送」流程；无需再次扫码。
    func registerScanned(_ info: HopDropPairing.Info) {
        client?.addDiscoveredPeer(info.id, name: info.name, platform: info.platform,
                                  addr: info.host, port: info.port)
        selectedId = info.id
    }

    func stop() {
        bonjour?.stop()
        bonjour = nil
        client?.stop()
        liveActivity.finish()
    }

    func respond(offerId: String, accept: Bool) {
        client?.respond(offerId, accept: accept)
        pendingOffer = nil
    }

    /// updateTransferOverlay 根据一条进度维护全屏遮罩的显隐：
    ///   - handshake / offer / transfer：立即显示并保持；
    ///   - done / rejected / error：展示终态，短暂停留后自动收起。
    func updateTransferOverlay(_ p: ProgressInfo) {
        dismissTask?.cancel()
        transfer = p
        switch p.phase {
        case "done", "rejected", "error":
            let delay: UInt64 = p.phase == "done" ? 1_400_000_000 : 1_800_000_000
            dismissTask = Task { [weak self] in
                try? await Task.sleep(nanoseconds: delay)
                if Task.isCancelled { return }
                self?.transfer = nil
            }
        default:
            break
        }
    }

    /// 把一批文件发送给某台设备。urls 通常来自 SwiftUI 的 .fileImporter。
    ///
    /// 发送是阻塞的（会一直到传输完成），因此放到后台队列执行；FileSource 由闭包
    /// 持有直到 send 返回，其 deinit 负责释放 security-scoped 资源。
    func send(deviceId: String, urls: [URL]) {
        guard let client = client else { return }
        DispatchQueue.global(qos: .userInitiated).async {
            let source = FileSource(urls: urls)
            do {
                try client.send(toDevice: deviceId, src: source)
            } catch {
                print("HopDrop send failed: \(error)")
            }
            _ = source  // 保活到 send 结束
        }
    }

    // MARK: - gomobile 回调桥接

    private final class CallbackImpl: NSObject, MobileCallbackProtocol {
        weak var owner: HopDropController?
        init(owner: HopDropController) { self.owner = owner }

        func onPeers(_ peersJSON: String?) {
            guard let data = peersJSON?.data(using: .utf8),
                  let list = try? JSONDecoder().decode([PeerDevice].self, from: data) else { return }
            DispatchQueue.main.async {
                self.owner?.peers = list
                // 选中的设备若已离线，清空选择。
                if let sel = self.owner?.selectedId, !list.contains(where: { $0.id == sel }) {
                    self.owner?.selectedId = nil
                }
            }
        }
        func onOffer(_ offerID: String?, offerJSON: String?) {
            guard let id = offerID, let data = offerJSON?.data(using: .utf8),
                  let offer = try? JSONDecoder().decode(OfferInfo.self, from: data) else { return }
            DispatchQueue.main.async { self.owner?.pendingOffer = (id, offer) }
        }
        func onProgress(_ progressJSON: String?) {
            guard let data = progressJSON?.data(using: .utf8),
                  let p = try? JSONDecoder().decode(ProgressInfo.self, from: data) else { return }
            DispatchQueue.main.async {
                self.owner?.progress = p
                // 同步推进灵动岛 / 锁屏 Live Activity。
                self.owner?.liveActivity.apply(p)
                // 驱动全屏传输遮罩。
                self.owner?.updateTransferOverlay(p)
            }
        }
    }
}

// MARK: - Sink：接收落地到 Documents/HopDrop

/// DocumentSink 是 gomobile MobileSink 的实现：把收到的文件写入 App Documents/HopDrop，
/// 按 rel_path 重建子目录，并做路径穿越防护。用自增 handle 关联已打开的 FileHandle。
final class DocumentSink: NSObject, MobileSinkProtocol {
    private let baseDir: URL
    private var handles: [String: FileHandle] = [:]
    private var seq = 0

    override init() {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first
            ?? URL(fileURLWithPath: NSTemporaryDirectory())
        baseDir = docs.appendingPathComponent("HopDrop", isDirectory: true)
        super.init()
    }

    func openWrite(_ metaJSON: String?, error: NSErrorPointer) -> String {
        guard let data = metaJSON?.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            error?.pointee = NSError(domain: "HopDrop", code: 2,
                          userInfo: [NSLocalizedDescriptionKey: "invalid file meta"])
            return ""
        }
        let name = (obj["name"] as? String) ?? "file"
        let rel = (obj["rel_path"] as? String) ?? name
        let dest = safeDestination(rel: rel, fallback: name)

        do {
            try FileManager.default.createDirectory(
                at: dest.deletingLastPathComponent(), withIntermediateDirectories: true)
            FileManager.default.createFile(atPath: dest.path, contents: nil)

            let fh = try FileHandle(forWritingTo: dest)
            let handle = "w\(seq)"; seq += 1
            handles[handle] = fh
            return handle
        } catch let e as NSError {
            error?.pointee = e
            return ""
        }
    }

    func write(_ handle: String?, data: Data?) throws {
        guard let h = handle, let fh = handles[h], let d = data else { return }
        try fh.write(contentsOf: d)
    }

    func close(_ handle: String?) throws {
        guard let h = handle, let fh = handles.removeValue(forKey: h) else { return }
        try? fh.close()
    }

    /// safeDestination 把相对路径限制在 baseDir 内，遇到 ".." / 绝对路径等越界情况退回文件名落地。
    private func safeDestination(rel: String, fallback: String) -> URL {
        let comps = rel.split(separator: "/").map(String.init)
        if rel.hasPrefix("/") || comps.contains("..") {
            return baseDir.appendingPathComponent(fallback)
        }
        let dest = baseDir.appendingPathComponent(rel)
        let baseStd = baseDir.standardizedFileURL.path + "/"
        if !dest.standardizedFileURL.path.hasPrefix(baseStd) {
            return baseDir.appendingPathComponent(fallback)
        }
        return dest
    }
}

// MARK: - Source：从 fileImporter 选到的 URL 取文件发送

/// FileSource 是 gomobile MobileSource 的实现：把用户选中的文件枚举成清单并流式读出。
///
/// 每次 readChunk 返回下一段字节，返回空 Data 表示 EOF（与 mobile.go 的 ReadChunk 约定一致）。
/// 选到的 URL 多为 security-scoped，init 时开启访问权限，deinit 统一释放。
final class FileSource: NSObject, MobileSourceProtocol {
    private struct Entry { let url: URL; let meta: [String: Any]; let scoped: Bool }

    private var entries: [Entry] = []
    private var handles: [String: FileHandle] = [:]

    init(urls: [URL]) {
        super.init()
        for (i, url) in urls.enumerated() {
            let scoped = url.startAccessingSecurityScopedResource()
            let id = String(i)
            let name = url.lastPathComponent

            var size: Int64 = 0
            if let vals = try? url.resourceValues(forKeys: [.fileSizeKey]), let s = vals.fileSize {
                size = Int64(s)
            }
            var mime = ""
            if let type = UTType(filenameExtension: url.pathExtension),
               let mt = type.preferredMIMEType {
                mime = mt
            }

            var meta: [String: Any] = [
                "id": id, "name": name, "rel_path": name,
                "size": size, "mod_unix": 0,
            ]
            if !mime.isEmpty { meta["mime_type"] = mime }
            entries.append(Entry(url: url, meta: meta, scoped: scoped))
        }
    }

    deinit {
        for e in entries where e.scoped { e.url.stopAccessingSecurityScopedResource() }
    }

    func listJSON(_ error: NSErrorPointer) -> String {
        do {
            let arr = entries.map { $0.meta }
            let data = try JSONSerialization.data(withJSONObject: arr, options: [])
            return String(data: data, encoding: .utf8) ?? "[]"
        } catch let e as NSError {
            error?.pointee = e
            return "[]"
        }
    }

    func openRead(_ fileID: String?, error: NSErrorPointer) -> String {
        guard let idStr = fileID, let idx = Int(idStr), idx >= 0, idx < entries.count else {
            error?.pointee = NSError(domain: "HopDrop", code: 1,
                          userInfo: [NSLocalizedDescriptionKey: "invalid file id"])
            return ""
        }
        do {
            let fh = try FileHandle(forReadingFrom: entries[idx].url)
            let handle = "r\(idStr)"
            handles[handle] = fh
            return handle
        } catch let e as NSError {
            error?.pointee = e
            return ""
        }
    }

    func readChunk(_ handle: String?) throws -> Data {
        guard let h = handle, let fh = handles[h] else { return Data() }
        return (try fh.read(upToCount: 128 * 1024)) ?? Data()
    }

    func closeRead(_ handle: String?) throws {
        guard let h = handle, let fh = handles.removeValue(forKey: h) else { return }
        try? fh.close()
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
Info.plist 需要声明：

    // iOS 14+ 访问本地网络（含组播）需用户授权
    <key>NSLocalNetworkUsageDescription</key>
    <string>HopDrop 需要访问本地网络以发现附近设备并传输文件</string>
    <key>NSBonjourServices</key>
    <array>
        <string>_hopdrop._udp</string>
    </array>

    // 让收到的文件在「文件」App 中可见（可选，便于用户取用 Documents/HopDrop）
    <key>UIFileSharingEnabled</key>
    <true/>
    <key>LSSupportsOpeningDocumentsInPlace</key>
    <true/>

组播地址 239.192.71.71:47771 属于本地网络，触发本地网络授权弹窗。
*/
