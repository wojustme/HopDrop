import SwiftUI

/// HopDropApp 是 iOS 参考 App 的入口：仅承载 HopDropView。
/// 本地网络授权、Bonjour 服务声明见 Info.plist。
@main
struct HopDropApp: App {
    var body: some Scene {
        WindowGroup {
            HopDropView()
        }
    }
}
