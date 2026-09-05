import Foundation
import Network

/// HopDropBonjour 用 Apple 的 Network framework 做局域网设备发现，替代 Go 侧的组播。
///
/// 关键点：iOS 对 App 自开的 UDP 组播需要 com.apple.developer.networking.multicast 授权
/// （需 Apple 审批）；而走系统 Bonjour（NWListener 广播 + NWBrowser 浏览）只需“本地网络”
/// 权限即可。因此发现放在原生这一层，传输仍复用 Go 引擎。
///
/// 服务类型固定 `_hopdrop._tcp`，与桌面(Go mDNS)、Android(NsdManager) 一致，可互相发现。
/// 设备身份放在 TXT 记录：id / name / platform。
///
/// 发现到（并解析出 IP:port）的 peer 通过 onResolved 回调交给上层喂进 MobileClient；
/// 服务消失通过 onRemoved 回调移除。
@MainActor
final class HopDropBonjour {
    static let serviceType = "_hopdrop._tcp"

    private var listener: NWListener?
    private var browser: NWBrowser?
    /// 正在解析中的连接，按服务名持有，避免被释放。
    private var resolving: [String: NWConnection] = [:]
    /// 服务名 → 已上报的设备 ID，便于消失时回调 onRemoved。
    private var nameToID: [String: String] = [:]

    private let selfID: String
    private let selfName: String
    private let selfPlatform: String
    private let port: UInt16

    /// 解析出一台设备（含 IP:port）时回调；参数依次为 id/name/platform/host/port。
    var onResolved: ((String, String, String, String, Int) -> Void)?
    /// 一台设备离线（服务消失）时回调，参数为设备 ID。
    var onRemoved: ((String) -> Void)?

    init(selfID: String, selfName: String, selfPlatform: String, port: Int) {
        self.selfID = selfID
        self.selfName = selfName
        self.selfPlatform = selfPlatform
        self.port = UInt16(max(0, min(port, 65535)))
    }

    // MARK: - 生命周期

    func start() {
        startListener()
        startBrowser()
    }

    func stop() {
        listener?.cancel(); listener = nil
        browser?.cancel(); browser = nil
        for (_, c) in resolving { c.cancel() }
        resolving.removeAll()
        nameToID.removeAll()
    }

    // MARK: - 广播自身（NWListener）

    private func startListener() {
        guard port > 0 else { return }
        do {
            let params = NWParameters.tcp
            let listener = try NWListener(using: params, on: NWEndpoint.Port(rawValue: port)!)
            // 广播 Bonjour 服务，TXT 携带设备身份。
            let txt = NWTXTRecord([
                "id": selfID,
                "name": selfName,
                "platform": selfPlatform,
            ])
            listener.service = NWListener.Service(
                name: selfID,             // 实例名用设备 ID，保证唯一
                type: Self.serviceType,
                txtRecord: txt.data
            )
            // 我们并不用这个 listener 处理数据（真正的接收 socket 在 Go 引擎里），
            // 但必须接受连接以保持服务健康；直接取消进来的连接即可。
            listener.newConnectionHandler = { conn in conn.cancel() }
            listener.stateUpdateHandler = { state in
                if case let .failed(err) = state {
                    print("HopDrop Bonjour listener failed: \(err)")
                }
            }
            listener.start(queue: .main)
            self.listener = listener
        } catch {
            print("HopDrop Bonjour listener start error: \(error)")
        }
    }

    // MARK: - 发现设备（NWBrowser）

    private func startBrowser() {
        let params = NWParameters()
        params.includePeerToPeer = true
        let browser = NWBrowser(
            for: .bonjourWithTXTRecord(type: Self.serviceType, domain: nil),
            using: params
        )
        browser.browseResultsChangedHandler = { [weak self] results, _ in
            Task { @MainActor in self?.handleResults(results) }
        }
        browser.stateUpdateHandler = { state in
            if case let .failed(err) = state {
                print("HopDrop Bonjour browser failed: \(err)")
            }
        }
        browser.start(queue: .main)
        self.browser = browser
    }

    private func handleResults(_ results: Set<NWBrowser.Result>) {
        // 当前在线的服务名集合。
        var live = Set<String>()

        for result in results {
            guard case let .service(name, type, domain, _) = result.endpoint else { continue }
            // 从 TXT 里取设备身份；忽略自己。
            var id = name, dev = name, platform = ""
            if case let .bonjour(txt) = result.metadata {
                id = txt["id"] ?? name
                dev = txt["name"] ?? name
                platform = txt["platform"] ?? ""
            }
            if id == selfID { continue }
            live.insert(name)
            nameToID[name] = id

            // 已在解析中的跳过，避免重复连接。
            if resolving[name] != nil { continue }
            resolve(name: name, type: type, domain: domain,
                    id: id, deviceName: dev, platform: platform)
        }

        // 处理消失：之前见过、这次不在的服务，回调 onRemoved。
        for (name, id) in nameToID where !live.contains(name) {
            resolving[name]?.cancel()
            resolving[name] = nil
            nameToID[name] = nil
            onRemoved?(id)
        }
    }

    /// resolve 用一个临时 NWConnection 把 Bonjour 服务解析成具体 IP:port。
    private func resolve(name: String, type: String, domain: String,
                         id: String, deviceName: String, platform: String) {
        let endpoint = NWEndpoint.service(name: name, type: type, domain: domain, interface: nil)
        let conn = NWConnection(to: endpoint, using: .tcp)
        resolving[name] = conn

        conn.stateUpdateHandler = { [weak self, weak conn] state in
            guard let conn = conn else { return }
            switch state {
            case .ready:
                let hp = Self.remoteHostPort(conn)
                Task { @MainActor in
                    guard let self = self else { return }
                    if let (host, port) = hp {
                        self.onResolved?(id, deviceName, platform, host, port)
                    }
                    conn.cancel()
                    self.resolving[name] = nil
                }
            case .failed, .cancelled:
                Task { @MainActor in self?.resolving[name] = nil }
            default:
                break
            }
        }
        conn.start(queue: .main)
    }

    /// remoteHostPort 从已就绪连接里取出对端的 IP 字符串与端口。
    nonisolated private static func remoteHostPort(_ conn: NWConnection) -> (String, Int)? {
        guard case let .hostPort(host, port) = conn.currentPath?.remoteEndpoint else {
            return nil
        }
        let portInt = Int(port.rawValue)
        switch host {
        case let .ipv4(addr):
            return (ipv4String(addr), portInt)
        case let .ipv6(addr):
            // 优先取内嵌的 IPv4（若有），否则用 IPv6 字面量。
            if let v4 = addr.asIPv4 {
                return (ipv4String(v4), portInt)
            }
            let raw = "\(addr)"
            return (raw.components(separatedBy: "%").first ?? raw, portInt)
        case let .name(n, _):
            return (n, portInt)
        @unknown default:
            return nil
        }
    }

    nonisolated private static func ipv4String(_ addr: IPv4Address) -> String {
        let b = addr.rawValue
        return "\(b[0]).\(b[1]).\(b[2]).\(b[3])"
    }
}
