import SwiftUI
import CoreImage.CIFilterBuiltins
import AVFoundation

/// HopDropPairing 汇集手动配对相关的解析与二维码生成工具。
///
/// 配对串格式（与 mobile.PairingURI 一致）：
///   hopdrop://<host:port>?id=...&name=...&platform=...&fingerprint=<SHA-256>
enum HopDropPairing {
    /// 从配对串解析出的对端信息。
    struct Info {
        let host: String
        let port: Int
        let id: String
        let name: String
        let platform: String
        let fingerprint: String
        var endpoint: String { host.contains(":") ? "[\(host)]:\(port)" : "\(host):\(port)" }
    }

    /// 从完整配对串里解析出可直连的 "host:port"。
    static func endpoint(from raw: String) -> String? {
        info(from: raw)?.endpoint
    }

    /// 完整解析配对串。TLS 指纹缺失或格式错误时拒绝。
    static func info(from raw: String) -> Info? {
        guard let comps = URLComponents(string: raw.trimmingCharacters(in: .whitespacesAndNewlines)),
              comps.scheme == "hopdrop", let host = comps.host,
              let port = comps.port, (1...65535).contains(port) else { return nil }
        let items = comps.queryItems ?? []
        let id = items.first(where: { $0.name == "id" })?.value ?? ""
        let name = items.first(where: { $0.name == "name" })?.value ?? ""
        let platform = items.first(where: { $0.name == "platform" })?.value ?? ""
        let fingerprint = items.first(where: { $0.name == "fingerprint" })?.value ?? ""
        guard !id.isEmpty, fingerprint.range(of: "^[0-9a-fA-F]{64}$", options: .regularExpression) != nil else {
            return nil
        }
        return Info(host: host, port: port, id: id,
                    name: name.isEmpty ? host : name, platform: platform,
                    fingerprint: fingerprint.lowercased())
    }

    /// 从配对串里解析出对端设备名（用于确认提示），无则返回 nil。
    static func name(from raw: String) -> String? {
        guard let comps = URLComponents(string: raw) else { return nil }
        return comps.queryItems?.first(where: { $0.name == "name" })?.value
    }

    /// 生成一张二维码 UIImage（CoreImage，无需第三方库）。
    static func qrImage(from string: String) -> UIImage? {
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else { return nil }
        // 放大到清晰尺寸。
        let scaled = output.transformed(by: CGAffineTransform(scaleX: 10, y: 10))
        guard let cg = context.createCGImage(scaled, from: scaled.extent) else { return nil }
        return UIImage(cgImage: cg)
    }
}

/// PairingSheet 是配对面板：上半展示本机二维码（供对端扫），下半一个「扫码发送」入口。
struct PairingSheet: View {
    @ObservedObject var controller: HopDropController
    @Environment(\.dismiss) private var dismiss
    /// 扫码结果透传给外部触发文件选择。
    let onScanned: (String) -> Void

    @State private var showScanner = false

    var body: some View {
        ZStack(alignment: .top) {
            HopDropTheme.background.ignoresSafeArea()

            ScrollView {
                VStack(spacing: 20) {
                    // 顶部标题 + 关闭按钮，同一行对齐，关闭放右上角圆形 ✕。
                    HStack {
                        Text("手动配对")
                            .font(.system(size: 18, weight: .bold))
                            .foregroundColor(HopDropTheme.ink)
                        Spacer()
                        Button {
                            dismiss()
                        } label: {
                            Image(systemName: "xmark")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundColor(HopDropTheme.muted)
                                .frame(width: 30, height: 30)
                                .background(HopDropTheme.input)
                                .clipShape(Circle())
                        }
                    }
                    .padding(.top, 4)

                    // —— 本机二维码 ——
                    VStack(spacing: 10) {
                        Text("让对方扫这个码")
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(HopDropTheme.ink)
                        if let uri = controller.pairingURI, let img = HopDropPairing.qrImage(from: uri) {
                            Image(uiImage: img)
                                .interpolation(.none)
                                .resizable()
                                .scaledToFit()
                                .frame(width: 220, height: 220)
                                .padding(12)
                                .background(Color.white)
                                .clipShape(RoundedRectangle(cornerRadius: 16))
                            Text(controller.selfName)
                                .font(.system(size: 14, weight: .medium))
                                .foregroundColor(HopDropTheme.ink)
                            if let ep = HopDropPairing.endpoint(from: uri) {
                                Text(ep)
                                    .font(.system(size: 12).monospacedDigit())
                                    .foregroundColor(HopDropTheme.muted)
                                    .textSelection(.enabled)
                            }
                        } else {
                            Text("暂无可用局域网地址\n请确认已连接 Wi-Fi")
                                .font(.system(size: 13))
                                .multilineTextAlignment(.center)
                                .foregroundColor(HopDropTheme.muted)
                                .frame(height: 220)
                        }
                    }
                    .frame(maxWidth: .infinity)
                    .padding(18)
                    .background(HopDropTheme.surface)
                    .clipShape(RoundedRectangle(cornerRadius: 18))

                    Text("或者")
                        .font(.system(size: 13))
                        .foregroundColor(HopDropTheme.muted)

                    // —— 扫码发送给对方 ——
                    Button {
                        showScanner = true
                    } label: {
                        HStack {
                            Image(systemName: "qrcode.viewfinder")
                            Text("扫码发送给对方")
                                .font(.system(size: 16, weight: .medium))
                        }
                        .foregroundColor(HopDropTheme.surface)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 15)
                        .background(HopDropTheme.accent)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                    }

                    Text("扫码会同时校验对方地址与设备指纹，无需依赖自动发现。")
                        .font(.system(size: 12))
                        .multilineTextAlignment(.center)
                        .foregroundColor(HopDropTheme.muted)
                }
                .padding(20)
            }
        }
        .sheet(isPresented: $showScanner) {
            QRScannerView { code in
                showScanner = false
                dismiss()
                onScanned(code)
            }
        }
        .onAppear { controller.refreshPairingURI() }
    }
}

/// QRScannerView 用 AVFoundation 打开后置摄像头扫码；识别到一个二维码即回调并停止。
struct QRScannerView: UIViewControllerRepresentable {
    let onFound: (String) -> Void

    func makeCoordinator() -> Coordinator { Coordinator(onFound: onFound) }

    func makeUIViewController(context: Context) -> ScannerController {
        let vc = ScannerController()
        vc.delegate = context.coordinator
        return vc
    }

    func updateUIViewController(_ uiViewController: ScannerController, context: Context) {}

    final class Coordinator: NSObject, AVCaptureMetadataOutputObjectsDelegate {
        let onFound: (String) -> Void
        private var done = false
        init(onFound: @escaping (String) -> Void) { self.onFound = onFound }

        func metadataOutput(_ output: AVCaptureMetadataOutput,
                            didOutput metadataObjects: [AVMetadataObject],
                            from connection: AVCaptureConnection) {
            guard !done,
                  let obj = metadataObjects.first as? AVMetadataMachineReadableCodeObject,
                  let str = obj.stringValue else { return }
            done = true
            DispatchQueue.main.async { self.onFound(str) }
        }
    }
}

/// ScannerController 承载 AVCaptureSession 与预览层，负责相机权限与生命周期。
final class ScannerController: UIViewController {
    weak var delegate: AVCaptureMetadataOutputObjectsDelegate?
    private let session = AVCaptureSession()
    private var preview: AVCaptureVideoPreviewLayer?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized:
            configure()
        case .notDetermined:
            AVCaptureDevice.requestAccess(for: .video) { [weak self] ok in
                if ok { DispatchQueue.main.async { self?.configure() } }
            }
        default:
            break
        }
    }

    private func configure() {
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input) else { return }
        session.addInput(input)

        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else { return }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(delegate, queue: .main)
        output.metadataObjectTypes = [.qr]

        let layer = AVCaptureVideoPreviewLayer(session: session)
        layer.videoGravity = .resizeAspectFill
        layer.frame = view.bounds
        view.layer.addSublayer(layer)
        preview = layer

        DispatchQueue.global(qos: .userInitiated).async { self.session.startRunning() }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
    }

    override func viewWillDisappear(_ animated: Bool) {
        super.viewWillDisappear(animated)
        if session.isRunning { session.stopRunning() }
    }
}
