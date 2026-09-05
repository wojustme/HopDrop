import Foundation
import ActivityKit

/// HopDropActivityAttributes 描述一次传输的 Live Activity（灵动岛 / 锁屏）数据模型。
///
/// 该文件同时被主 App 与 Widget 扩展编译（在 project.yml 里两个 target 都引用它），
/// 因此不要引入只属于某一端的类型。
///
/// - 静态部分（attributes 本体）：整场活动不变的信息，这里只放一个会话标题。
/// - 动态部分（ContentState）：随传输进度不断 update 的内容，字段与 mobile/dto.go 的
///   ProgressJSON 对齐，便于从 ProgressInfo 直接映射。
struct HopDropActivityAttributes: ActivityAttributes {
    struct ContentState: Codable, Hashable {
        /// "send" / "recv"。
        var direction: String
        /// "handshake" / "offer" / "transfer" / "done" / "rejected" / "error"。
        var phase: String
        /// 对端设备名。
        var peerName: String
        /// 当前正在传输的文件名。
        var currentName: String
        /// 已完成文件数 / 总文件数。
        var files: Int
        var totalFiles: Int
        /// 已传字节 / 总字节。
        var bytes: Int64
        var totalBytes: Int64

        /// 传输进度（0...1）。总字节为 0 时回退到文件数比例。
        var fraction: Double {
            if totalBytes > 0 { return min(1, max(0, Double(bytes) / Double(totalBytes))) }
            if totalFiles > 0 { return min(1, max(0, Double(files) / Double(totalFiles))) }
            return 0
        }

        /// "发送" / "接收"。
        var verb: String { direction == "send" ? "发送" : "接收" }
    }

    /// 整场活动的标题（如 "HopDrop"），静态不变。
    var sessionTitle: String
}
