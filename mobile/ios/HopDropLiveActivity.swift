import Foundation
import ActivityKit

/// HopDropLiveActivity 负责把传输进度投射到灵动岛 / 锁屏 Live Activity。
///
/// 生命周期与一次“选中设备 → 发送/接收”的进度流对齐：
///   - transfer 阶段：不存在活动则 start，存在则 update；
///   - done / rejected / error：update 到终态后延时 dismiss；
///   - 其余阶段（handshake / offer）：若已在进行中则同步内容。
///
/// 仅在 iOS 16.1+ 且用户开启了 Live Activities 时生效，其余情况下所有方法均为 no-op。
@MainActor
final class HopDropLiveActivity {
    private var activity: Activity<HopDropActivityAttributes>?

    /// apply 根据一条进度把 Live Activity 推进到对应状态。
    func apply(_ p: ProgressInfo) {
        guard #available(iOS 16.1, *) else { return }
        guard ActivityAuthorizationInfo().areActivitiesEnabled else { return }

        let state = HopDropActivityAttributes.ContentState(
            direction: p.direction,
            phase: p.phase,
            peerName: p.peer_name,
            currentName: p.current_name,
            files: p.files,
            totalFiles: p.total_files,
            bytes: p.bytes,
            totalBytes: p.total_bytes
        )

        switch p.phase {
        case "handshake", "offer", "transfer":
            if activity == nil {
                start(with: state)
            } else {
                update(state)
            }
        case "done", "rejected", "error":
            // 先把终态刷上去，再在几秒后收起。
            update(state)
            end(after: p.phase == "transfer" ? 0 : 2.0)
        default:
            update(state)
        }
    }

    /// 主动结束（如 controller.stop() 或视图消失时调用）。
    func finish() { end(after: 0) }

    // MARK: - 内部

    @available(iOS 16.1, *)
    private func start(with state: HopDropActivityAttributes.ContentState) {
        let attributes = HopDropActivityAttributes(sessionTitle: "HopDrop")
        do {
            if #available(iOS 16.2, *) {
                activity = try Activity.request(
                    attributes: attributes,
                    content: .init(state: state, staleDate: nil),
                    pushType: nil)
            } else {
                activity = try Activity.request(
                    attributes: attributes,
                    contentState: state,
                    pushType: nil)
            }
        } catch {
            print("HopDrop LiveActivity start failed: \(error)")
        }
    }

    @available(iOS 16.1, *)
    private func update(_ state: HopDropActivityAttributes.ContentState) {
        guard let activity else { return }
        Task {
            if #available(iOS 16.2, *) {
                await activity.update(.init(state: state, staleDate: nil))
            } else {
                await activity.update(using: state)
            }
        }
    }

    @available(iOS 16.1, *)
    private func end(after delay: TimeInterval) {
        guard let activity else { return }
        self.activity = nil
        Task {
            if delay > 0 { try? await Task.sleep(nanoseconds: UInt64(delay * 1_000_000_000)) }
            if #available(iOS 16.2, *) {
                await activity.end(nil, dismissalPolicy: .immediate)
            } else {
                await activity.end(dismissalPolicy: .immediate)
            }
        }
    }
}
