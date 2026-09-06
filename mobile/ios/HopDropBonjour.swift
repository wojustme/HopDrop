import Foundation
import Network

/// HopDropBonjour 用系统 Bonjour 发布 Go HTTPS Server，并通过 NWBrowser 发现对端。
///
/// 服务类型固定 `_hopdrop._tcp`，与桌面(Go mDNS)、Android(NsdManager) 一致，可互相发现。
/// 设备身份放在 TXT 记录：id / name / platform / fingerprint。
///
/// 发现到（并解析出 IP:port）的 peer 通过 onResolved 回调交给上层喂进 MobileClient；
/// 服务消失通过 onRemoved 回调移除。
@MainActor
final class HopDropBonjour {
    static let serviceType = "_hopdrop._tcp"

    private var service: NetService?
    private var browser: NWBrowser?
    /// 正在解析中的连接，按服务名持有，避免被释放。
    private var resolving: [String: NWConnection] = [:]
    /// 服务名 → 已上报的设备 ID，便于消失时回调 onRemoved。
    private var nameToID: [String: String] = [:]

    private let selfID: String
    private let selfName: String
    private let selfPlatform: String
    private let selfFingerprint: String
    private let port: UInt16

    /// 参数依次为 id/name/platform/fingerprint/host/port。
    var onResolved: ((String, String, String, String, String, Int) -> Void)?
    /// 一台设备离线（服务消失）时回调，参数为设备 ID。
    var onRemoved: ((String) -> Void)?

    init(selfID: String, selfName: String, selfPlatform: String,
         selfFingerprint: String, port: Int) {
        self.selfID = selfID
        self.selfName = selfName
        self.selfPlatform = selfPlatform
        self.selfFingerprint = selfFingerprint
        self.port = UInt16(max(0, min(port, 65535)))
    }

    // MARK: - 生命周期

    func start() {
        publishService()
        startBrowser()
    }

    func stop() {
        service?.stop(); service = nil
        browser?.cancel(); browser = nil
        for (_, c) in resolving { c.cancel() }
        resolving.removeAll()
        nameToID.removeAll()
    }

    // MARK: - 广播 Go HTTPS Server 已经占用的端口

    private func publishService() {
        guard port > 0 else { return }
        let published = NetService(
            domain: "local.", type: Self.serviceType + ".",
            name: selfID, port: Int32(port)
        )
        published.includesPeerToPeer = true
        let fields = [
            "id": Data(selfID.utf8),
            "name": Data(selfName.utf8),
            "platform": Data(selfPlatform.utf8),
            "fingerprint": Data(selfFingerprint.utf8),
        ]
        published.setTXTRecord(NetService.data(fromTXTRecord: fields))
        published.publish()
        service = published
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
            var id = name, dev = name, platform = "", fingerprint = ""
            if case let .bonjour(txt) = result.metadata {
                id = txt["id"] ?? name
                dev = txt["name"] ?? name
                platform = txt["platform"] ?? ""
                fingerprint = txt["fingerprint"] ?? ""
            }
            if id == selfID { continue }
            live.insert(name)
            nameToID[name] = id

            // 已在解析中的跳过，避免重复连接。
            if resolving[name] != nil { continue }
            resolve(name: name, type: type, domain: domain,
                    id: id, deviceName: dev, platform: platform, fingerprint: fingerprint)
        }

        // 处理消失：之前见过、这次不在的服务，回调 onRemoved。
        let disappeared = nameToID.filter { !live.contains($0.key) }
        for (name, id) in disappeared {
            resolving[name]?.cancel()
            resolving[name] = nil
            nameToID[name] = nil
            onRemoved?(id)
        }
    }

    /// resolve 用一个临时 NWConnection 把 Bonjour 服务解析成具体 IP:port。
    private func resolve(name: String, type: String, domain: String,
                         id: String, deviceName: String, platform: String, fingerprint: String) {
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
                    if self.nameToID[name] == id, let (host, port) = hp {
                        self.onResolved?(id, deviceName, platform, fingerprint, host, port)
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
            return ("\(addr)", portInt)
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
