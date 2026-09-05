package com.hopdrop.app

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.ContentValues
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.compose.foundation.background
import androidx.compose.foundation.Image
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.hopdrop.mobile.Callback
import com.hopdrop.mobile.Client
import com.hopdrop.mobile.Mobile
import com.hopdrop.mobile.Sink
import com.hopdrop.mobile.Source
import org.json.JSONArray
import org.json.JSONObject

/**
 * HopDrop 品牌配色（米白 + 深青），与桌面端 hopTheme 保持一致：
 *   - 背景用温润米白，卡片用更亮的暖白拉出层次；
 *   - 深青 teal 作强调色，深墨蓝灰作正文，冷暖平衡、对比清晰。
 */
private val CreamBackground = Color(0xFFF5F1E7)
private val CreamSurface = Color(0xFFFCFAF4)
private val CreamInput = Color(0xFFEEE8D9)
private val AccentTeal = Color(0xFF0C8FA6)
private val AccentTealSoft = Color(0xFFD4EBF0)
private val InkForeground = Color(0xFF242A33)
private val MutedForeground = Color(0xFF8C8676)
private val CreamSeparator = Color(0xFFE2DBC9)
private val OnlineGreen = Color(0xFF12A46E)

private val HopDropColorScheme = lightColorScheme(
    primary = AccentTeal,
    onPrimary = CreamSurface,
    background = CreamBackground,
    onBackground = InkForeground,
    surface = CreamSurface,
    onSurface = InkForeground,
    surfaceVariant = CreamInput,
    onSurfaceVariant = MutedForeground,
    outline = CreamSeparator,
)

/**
 * MainActivity 是 HopDrop Android 参考 App 的唯一界面。
 *
 * 它展示局域网内在线设备，支持：
 *   - 收到传输请求时弹窗询问，用户确认后落到系统下载目录；
 *   - 选中一台在线设备后，用系统文件选择器挑选文件并推送过去。
 *
 * 说明：为保持依赖最小、CI 里能稳定 assembleRelease，本参考实现用 org.json 手工
 * 解析绑定层回传的 JSON（不引入 kotlinx-serialization）。
 */
class MainActivity : ComponentActivity() {

    private lateinit var client: Client
    /** 下拉通知栏的传输进度通知管理器。 */
    private lateinit var transferNotifier: TransferNotifier
    /** 原生 NsdManager 发现（系统 mDNS），把发现到的 peer 喂给 Go 侧。 */
    private var nsd: HopDropNsd? = null

    // —— Compose 可观察状态 ——
    private val peersState = mutableStateOf<List<PeerItem>>(emptyList())
    private val statusState = mutableStateOf("正在发现设备…")
    private val selectedIdState = mutableStateOf<String?>(null)
    /** 待用户确认的入站传输；非空时展示确认弹窗。 */
    private val pendingOfferState = mutableStateOf<PendingOffer?>(null)
    /** 非空时展示全屏传输遮罩（发送/接收进行中及短暂终态）。 */
    private val transferState = mutableStateOf<TransferInfo?>(null)
    /** 本机设备名/平台，用于界面展示。 */
    private val selfNameState = mutableStateOf("Android")
    private val selfPlatformState = mutableStateOf("android")
    /** 本机配对串（hopdrop://host:port?...）；空表示无可用局域网地址。 */
    private val pairingUriState = mutableStateOf("")
    /** 控制配对面板呈现。 */
    private val showPairingState = mutableStateOf(false)
    /** 扫码得到对端端点后暂存，待用户选文件后直连发送。 */
    private var pendingEndpoint: String? = null
    /** 终态遮罩的延时收起句柄，便于被下一条进度取消。 */
    private var dismissRunnable: Runnable? = null

    /** 系统文件选择器：可多选，返回若干 content:// URI。 */
    private val pickFiles = registerForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments()
    ) { uris -> if (!uris.isNullOrEmpty()) startSend(uris) }

    /** 扫码后拉起的文件选择器：选完直连发送到 pendingEndpoint。 */
    private val pickFilesForEndpoint = registerForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments()
    ) { uris -> if (!uris.isNullOrEmpty()) startSendEndpoint(uris) }

    /** 扫码：调用 zxing-embedded 的扫码 Activity，返回扫到的文本。 */
    private val scanQr = registerForActivityResult(com.journeyapps.barcodescanner.ScanContract()) { result ->
        val text = result.contents ?: return@registerForActivityResult
        val ep = parsePairingEndpoint(text) ?: run {
            statusState.value = "无法识别的二维码"
            return@registerForActivityResult
        }
        pendingEndpoint = ep
        // 选文件后直连发送。
        pickFilesForEndpoint.launch(arrayOf("*/*"))
    }

    /** Android 13+ 通知权限请求（拒绝也不影响传输，只是没有通知栏进度）。 */
    private val requestNotifPermission = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { /* 用户选择结果无需特殊处理 */ }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        transferNotifier = TransferNotifier(this)
        ensureNotificationPermission()

        val callback = object : Callback {
            override fun onPeers(peersJSON: String) {
                val arr = JSONArray(peersJSON)
                val list = ArrayList<PeerItem>(arr.length())
                for (i in 0 until arr.length()) {
                    val dev = arr.getJSONObject(i).getJSONObject("device")
                    list.add(
                        PeerItem(
                            id = dev.getString("id"),
                            name = dev.getString("name"),
                            platform = dev.getString("platform"),
                        )
                    )
                }
                runOnUiThread {
                    peersState.value = list
                    // 选中的设备若已离线，清空选择。
                    if (list.none { it.id == selectedIdState.value }) {
                        selectedIdState.value = null
                    }
                }
            }

            override fun onOffer(offerID: String, offerJSON: String) {
                val offer = JSONObject(offerJSON)
                val sender = offer.getJSONObject("peer").getString("name")
                val fileCount = offer.getJSONArray("files").length()
                val totalBytes = offer.optLong("total_bytes", 0)
                // 交给主线程弹窗，用户答复后再 client.respond(...)。
                runOnUiThread {
                    pendingOfferState.value = PendingOffer(offerID, sender, fileCount, totalBytes)
                }
            }

            override fun onProgress(progressJSON: String) {
                val p = JSONObject(progressJSON)
                val info = TransferInfo(
                    direction = p.optString("direction"),
                    phase = p.getString("phase"),
                    peerName = p.optString("peer_name"),
                    currentName = p.optString("current_name"),
                    files = p.optInt("files"),
                    totalFiles = p.optInt("total_files"),
                    bytes = p.optLong("bytes"),
                    totalBytes = p.optLong("total_bytes"),
                    err = p.optString("err"),
                )
                runOnUiThread {
                    statusState.value = info.statusText()
                    // 同步更新全屏遮罩与下拉通知栏进度。
                    updateTransferOverlay(info)
                    transferNotifier.update(info)
                }
            }
        }

        client = Mobile.newClient(
            Build.MODEL ?: "Android",
            "android",
            filesDir.absolutePath,
            DownloadSink(this),
            callback,
        )
        client.start(0)
        selfNameState.value = client.selfName()
        selfPlatformState.value = client.selfPlatform()
        pairingUriState.value = client.pairingURI()
        startNsd()

        setContent {
            MaterialTheme(colorScheme = HopDropColorScheme) {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    val peers by peersState
                    val status by statusState
                    val selectedId by selectedIdState
                    val pending by pendingOfferState
                    val transfer by transferState
                    val selfName by selfNameState
                    val selfPlatform by selfPlatformState
                    val showPairing by showPairingState

                    HopDropScreen(
                        peers = peers,
                        status = status,
                        selectedId = selectedId,
                        selfName = selfName,
                        selfPlatform = selfPlatform,
                        onSelect = { selectedIdState.value = it },
                        onSend = ::onSendClicked,
                        onPair = { showPairingState.value = true },
                    )

                    pending?.let { offer ->
                        OfferDialog(
                            offer = offer,
                            onAccept = { respondOffer(offer.id, true) },
                            onReject = { respondOffer(offer.id, false) },
                        )
                    }

                    if (showPairing) {
                        PairingDialog(
                            uri = pairingUriState.value,
                            selfName = selfName,
                            onScan = {
                                showPairingState.value = false
                                launchScanner()
                            },
                            onDismiss = { showPairingState.value = false },
                        )
                    }

                    // 全屏传输遮罩：传输/接收进行中及短暂终态时盖住整个界面。
                    transfer?.let { TransferOverlay(info = it) }
                }
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        nsd?.stop()
        nsd = null
        if (::client.isInitialized) client.stop()
        transferNotifier.cancel()
    }

    /** startNsd 启动系统 NsdManager 发现，把结果喂进 Go 的 Client。 */
    private fun startNsd() {
        // 从 selfJSON 里取稳定设备 ID（与对端看到的一致、且用于过滤自己）。
        val self = JSONObject(client.selfJSON())
        val id = self.optString("id")
        val n = HopDropNsd(
            context = applicationContext,
            selfId = id,
            selfName = client.selfName(),
            selfPlatform = client.selfPlatform(),
            port = client.selfSyncPort().toInt(),
        )
        n.onResolved = { pid, name, platform, addr, port ->
            client.addDiscoveredPeer(pid, name, platform, addr, port.toLong())
        }
        n.onRemoved = { pid -> client.removeDiscoveredPeer(pid) }
        n.start()
        nsd = n
    }

    /** 拉起 zxing 扫码界面。 */
    private fun launchScanner() {
        val opts = com.journeyapps.barcodescanner.ScanOptions().apply {
            setDesiredBarcodeFormats(com.journeyapps.barcodescanner.ScanOptions.QR_CODE)
            setPrompt("对准对方的 HopDrop 二维码")
            setBeepEnabled(false)
            setOrientationLocked(false)
        }
        scanQr.launch(opts)
    }

    /** 扫码得到文件后，直连发送到 pendingEndpoint。 */
    private fun startSendEndpoint(uris: List<Uri>) {
        val endpoint = pendingEndpoint ?: return
        pendingEndpoint = null
        statusState.value = "正在直连发送 ${uris.size} 个文件…"
        Thread {
            try {
                val source = ContentResolverSource(contentResolver, uris)
                client.sendToEndpoint(endpoint, source)
            } catch (e: Exception) {
                runOnUiThread { statusState.value = "发送失败: ${e.message}" }
            }
        }.start()
    }

    /** Android 13+ 首次进入时请求通知权限（用于下拉通知栏进度）。 */
    private fun ensureNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val granted = ContextCompat.checkSelfPermission(
                this, Manifest.permission.POST_NOTIFICATIONS,
            ) == PackageManager.PERMISSION_GRANTED
            if (!granted) requestNotifPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    /**
     * updateTransferOverlay 维护全屏遮罩显隐：
     *   - handshake / offer / transfer：立即显示并保持；
     *   - done / rejected / error：展示终态，短暂停留后自动收起。
     */
    private fun updateTransferOverlay(info: TransferInfo) {
        dismissRunnable?.let { window.decorView.removeCallbacks(it) }
        transferState.value = info
        if (info.isTerminal()) {
            val delay = if (info.phase == "done") 1400L else 1800L
            val r = Runnable { transferState.value = null }
            dismissRunnable = r
            window.decorView.postDelayed(r, delay)
        }
    }

    /** 点"发送文件"：先确认已选设备，再拉起系统文件选择器。 */
    private fun onSendClicked() {
        if (selectedIdState.value == null) {
            statusState.value = "请先在上方列表选择一台设备"
            return
        }
        // "*/*" 允许挑选任意类型文件；可多选。
        pickFiles.launch(arrayOf("*/*"))
    }

    /** 文件选好后，在后台线程把它们推送给当前选中的设备。 */
    private fun startSend(uris: List<Uri>) {
        val deviceId = selectedIdState.value ?: return
        statusState.value = "正在发送 ${uris.size} 个文件…"
        Thread {
            try {
                val source = ContentResolverSource(contentResolver, uris)
                client.sendToDevice(deviceId, source)
            } catch (e: Exception) {
                runOnUiThread { statusState.value = "发送失败: ${e.message}" }
            }
        }.start()
    }

    /** 用户在弹窗里作答后，把决定回给绑定层并收起弹窗。 */
    private fun respondOffer(offerID: String, accept: Boolean) {
        client.respond(offerID, accept)
        pendingOfferState.value = null
        statusState.value = if (accept) "正在接收…" else "已拒绝本次传输"
    }
}

/** PeerItem 是设备列表用的最小展示模型（含稳定设备 ID，供发送定位目标）。 */
private data class PeerItem(val id: String, val name: String, val platform: String)

/** PendingOffer 描述一次待确认的入站传输。 */
private data class PendingOffer(
    val id: String,
    val sender: String,
    val fileCount: Int,
    val totalBytes: Long,
)

/**
 * HopDropScreen 是米白主题的主界面：品牌头 + 本机设备名 + 状态条 + 在线设备卡片列表 + 操作按钮。
 */
@androidx.compose.runtime.Composable
private fun HopDropScreen(
    peers: List<PeerItem>,
    status: String,
    selectedId: String?,
    selfName: String,
    selfPlatform: String,
    onSelect: (String) -> Unit,
    onSend: () -> Unit,
    onPair: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(20.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        // —— 品牌头 ——
        Column {
            Text(
                "HopDrop",
                color = AccentTeal,
                fontSize = 30.sp,
                fontWeight = FontWeight.Bold,
            )
            Text("局域网 · 极速互传", color = MutedForeground, fontSize = 13.sp)
        }

        // —— 本机设备名卡片 ——
        Card(
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = CreamSurface),
            shape = RoundedCornerShape(14.dp),
        ) {
            Row(
                modifier = Modifier.padding(14.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(selfName, color = InkForeground, fontSize = 15.sp, fontWeight = FontWeight.Bold)
                    Text("本机 · $selfPlatform", color = MutedForeground, fontSize = 12.sp)
                }
                TextButton(onClick = onPair) { Text("二维码") }
            }
        }

        // —— 状态条 ——
        Card(
            modifier = Modifier.fillMaxWidth(),
            colors = CardDefaults.cardColors(containerColor = CreamSurface),
            shape = RoundedCornerShape(14.dp),
        ) {
            Row(
                modifier = Modifier.padding(16.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Box(
                    modifier = Modifier
                        .size(10.dp)
                        .background(AccentTeal, CircleShape),
                )
                Text(status, color = InkForeground, fontSize = 15.sp)
            }
        }

        // —— 在线设备 ——
        Text(
            "在线设备（${peers.size}）",
            color = InkForeground,
            fontSize = 16.sp,
            fontWeight = FontWeight.Bold,
        )
        Card(
            modifier = Modifier
                .fillMaxWidth()
                .weight(1f),
            colors = CardDefaults.cardColors(containerColor = CreamSurface),
            shape = RoundedCornerShape(14.dp),
        ) {
            if (peers.isEmpty()) {
                Box(
                    modifier = Modifier.fillMaxSize().padding(24.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text("正在扫描局域网设备…", color = MutedForeground, fontSize = 14.sp)
                        Spacer(Modifier.height(4.dp))
                        Text("确保设备处于同一 Wi-Fi 网络", color = MutedForeground, fontSize = 12.sp)
                    }
                }
            } else {
                LazyColumn(
                    modifier = Modifier.fillMaxSize().padding(8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    items(peers) { p ->
                        PeerRow(p, selected = p.id == selectedId, onClick = { onSelect(p.id) })
                    }
                }
            }
        }

        // —— 操作按钮：发送文件 + 扫码 ——
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Button(
                onClick = onSend,
                modifier = Modifier
                    .weight(1f)
                    .height(52.dp),
                shape = RoundedCornerShape(12.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = AccentTeal,
                    contentColor = CreamSurface,
                ),
            ) {
                Text("发送文件", fontSize = 16.sp, fontWeight = FontWeight.Medium)
            }
            Button(
                onClick = onPair,
                modifier = Modifier.height(52.dp),
                shape = RoundedCornerShape(12.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = CreamInput,
                    contentColor = AccentTeal,
                ),
            ) {
                Text("扫码", fontSize = 16.sp, fontWeight = FontWeight.Medium)
            }
        }
    }
}

/** PeerRow 是单台设备的卡片行：状态点 + 名称 + 平台标签；点击可选中为发送目标。 */
@androidx.compose.runtime.Composable
private fun PeerRow(p: PeerItem, selected: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .background(if (selected) AccentTealSoft else CreamInput, RoundedCornerShape(10.dp))
            .border(
                1.dp,
                if (selected) AccentTeal else CreamSeparator,
                RoundedCornerShape(10.dp),
            )
            .padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Box(
            modifier = Modifier
                .size(9.dp)
                .background(OnlineGreen, CircleShape),
        )
        Text(
            p.name,
            color = InkForeground,
            fontSize = 15.sp,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.weight(1f),
        )
        Text(
            if (selected) "已选 · ${p.platform}" else p.platform,
            color = if (selected) AccentTeal else MutedForeground,
            fontSize = 13.sp,
        )
    }
}

/** OfferDialog 是入站传输的确认弹窗，替代此前的“默认自动接受”。 */
@androidx.compose.runtime.Composable
private fun OfferDialog(offer: PendingOffer, onAccept: () -> Unit, onReject: () -> Unit) {
    AlertDialog(
        onDismissRequest = onReject,
        title = { Text("收到文件传输") },
        text = {
            Text(
                "${offer.sender} 想给你发送 ${offer.fileCount} 个文件" +
                    "（共 ${humanBytes(offer.totalBytes)}）。\n接收后将保存到「下载/HopDrop」。",
            )
        },
        confirmButton = { TextButton(onClick = onAccept) { Text("接收") } },
        dismissButton = { TextButton(onClick = onReject) { Text("拒绝") } },
    )
}

/**
 * PairingDialog 是手动配对面板：展示本机二维码（供对方扫）+ 一个「扫码发送给对方」按钮。
 *
 * 组播被限制时（如对端是未授权 iOS），扫码可直连 TCP 传输，绕过设备发现。
 */
@androidx.compose.runtime.Composable
private fun PairingDialog(
    uri: String,
    selfName: String,
    onScan: () -> Unit,
    onDismiss: () -> Unit,
) {
    val qr = remember(uri) { if (uri.isNotEmpty()) generateQr(uri, 640) else null }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("手动配对") },
        text = {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(12.dp),
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text("让对方扫这个码", color = InkForeground, fontSize = 15.sp, fontWeight = FontWeight.Bold)
                if (qr != null) {
                    Image(
                        bitmap = qr.asImageBitmap(),
                        contentDescription = "配对二维码",
                        modifier = Modifier
                            .size(220.dp)
                            .background(Color.White, RoundedCornerShape(12.dp))
                            .padding(10.dp),
                    )
                    Text(selfName, color = InkForeground, fontSize = 14.sp)
                    val ep = parsePairingEndpoint(uri)
                    if (ep != null) {
                        Text(ep, color = MutedForeground, fontSize = 12.sp)
                    }
                } else {
                    Text(
                        "暂无可用局域网地址\n请确认已连接 Wi-Fi",
                        color = MutedForeground,
                        fontSize = 13.sp,
                    )
                }
            }
        },
        confirmButton = { TextButton(onClick = onScan) { Text("扫码发送给对方") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
    )
}

/**
 * parsePairingEndpoint 从配对串解析出可直连的 "host:port"。
 * 支持 hopdrop://host:port?... 与裸 host:port 两种输入。
 */
private fun parsePairingEndpoint(raw: String): String? {
    val s = raw.trim()
    if (s.isEmpty()) return null
    if (!s.startsWith("hopdrop://")) {
        return if (s.contains(":")) s else null
    }
    var rest = s.removePrefix("hopdrop://")
    val q = rest.indexOf('?')
    if (q >= 0) rest = rest.substring(0, q)
    return rest.ifEmpty { null }
}

/** generateQr 用 zxing 生成一张 size×size 的黑白二维码位图。 */
private fun generateQr(content: String, size: Int): android.graphics.Bitmap? {
    return try {
        val hints = mapOf(com.google.zxing.EncodeHintType.MARGIN to 1)
        val matrix = com.google.zxing.qrcode.QRCodeWriter().encode(
            content, com.google.zxing.BarcodeFormat.QR_CODE, size, size, hints,
        )
        val bmp = android.graphics.Bitmap.createBitmap(size, size, android.graphics.Bitmap.Config.ARGB_8888)
        for (x in 0 until size) {
            for (y in 0 until size) {
                bmp.setPixel(x, y, if (matrix.get(x, y)) android.graphics.Color.BLACK else android.graphics.Color.WHITE)
            }
        }
        bmp
    } catch (e: Exception) {
        null
    }
}

/** humanBytes 把字节数格式化为可读字符串。 */
private fun humanBytes(n: Long): String {
    if (n < 1024) return "$n B"
    val units = arrayOf("KB", "MB", "GB", "TB")
    var v = n.toDouble() / 1024
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return String.format("%.1f %s", v, units[i])
}

/**
 * TransferInfo 是一次传输进度的展示模型，字段与 mobile/dto.go 的 ProgressJSON 对齐，
 * 同时驱动全屏遮罩与下拉通知栏进度。
 */
private data class TransferInfo(
    val direction: String,
    val phase: String,
    val peerName: String,
    val currentName: String,
    val files: Int,
    val totalFiles: Int,
    val bytes: Long,
    val totalBytes: Long,
    val err: String,
) {
    val verb: String get() = if (direction == "send") "发送" else "接收"

    /** 进度（0..1）。总字节为 0 时回退到文件数比例。 */
    fun fraction(): Float = when {
        totalBytes > 0 -> (bytes.toFloat() / totalBytes).coerceIn(0f, 1f)
        totalFiles > 0 -> (files.toFloat() / totalFiles).coerceIn(0f, 1f)
        else -> 0f
    }

    fun isTerminal(): Boolean = phase == "done" || phase == "rejected" || phase == "error"

    fun title(): String = when (phase) {
        "done" -> "${verb}完成"
        "rejected" -> "对方已拒绝"
        "error" -> "传输出错"
        "transfer" -> "正在$verb"
        else -> "${verb}准备中…"
    }

    fun subtitle(): String = when (phase) {
        "done" -> "共 $files 个文件 · ${humanBytes(bytes)}"
        "rejected" -> peerName
        "error" -> err
        "transfer" -> "$currentName（$files/$totalFiles）"
        else -> peerName
    }

    /** 主界面状态条用的一行文案。 */
    fun statusText(): String = when (phase) {
        "transfer" -> "$verb $files/$totalFiles · $currentName"
        "done" -> "${verb}完成 · $files 个文件"
        "rejected" -> "对方拒绝了本次传输"
        "error" -> "出错: $err"
        else -> "${verb}中…"
    }
}

/**
 * TransferOverlay 是全屏传输遮罩：半透明蒙层 + 居中卡片，展示进度环、文件名与统计。
 * 传输/接收进行中盖住整个界面，避免误操作；完成/被拒/出错短暂停留后由外部收起。
 */
@androidx.compose.runtime.Composable
private fun TransferOverlay(info: TransferInfo) {
    val tint = when (info.phase) {
        "done" -> OnlineGreen
        "rejected", "error" -> Color(0xFFD63B5A)
        else -> AccentTeal
    }
    Box(
        modifier = Modifier
            .fillMaxSize()
            .background(Color(0x73000000))
            // 吞掉点击，禁止穿透到底层界面。
            .clickable(enabled = true, onClick = {}),
        contentAlignment = Alignment.Center,
    ) {
        Card(
            modifier = Modifier
                .width(300.dp)
                .padding(24.dp),
            colors = CardDefaults.cardColors(containerColor = CreamSurface),
            shape = RoundedCornerShape(20.dp),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(28.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.spacedBy(16.dp),
            ) {
                // 进度环。
                Box(contentAlignment = Alignment.Center, modifier = Modifier.size(96.dp)) {
                    if (info.isTerminal()) {
                        Text(
                            when (info.phase) {
                                "done" -> "✓"
                                "rejected" -> "✕"
                                else -> "!"
                            },
                            color = tint,
                            fontSize = 44.sp,
                            fontWeight = FontWeight.Bold,
                        )
                    } else {
                        CircularProgressIndicator(
                            progress = { info.fraction().coerceAtLeast(0.02f) },
                            modifier = Modifier.size(96.dp),
                            color = tint,
                            strokeWidth = 8.dp,
                            trackColor = CreamSeparator,
                        )
                        Text(
                            "${(info.fraction() * 100).toInt()}%",
                            color = tint,
                            fontSize = 18.sp,
                            fontWeight = FontWeight.Bold,
                        )
                    }
                }

                Column(horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(info.title(), color = InkForeground, fontSize = 19.sp, fontWeight = FontWeight.Bold)
                    if (info.peerName.isNotEmpty() && info.phase != "rejected") {
                        Spacer(Modifier.height(4.dp))
                        Text(info.peerName, color = MutedForeground, fontSize = 13.sp)
                    }
                    val sub = info.subtitle()
                    if (sub.isNotEmpty()) {
                        Spacer(Modifier.height(2.dp))
                        Text(sub, color = MutedForeground, fontSize = 13.sp)
                    }
                }

                if (!info.isTerminal()) {
                    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        LinearProgressIndicator(
                            progress = { info.fraction() },
                            modifier = Modifier.fillMaxWidth(),
                            color = tint,
                            trackColor = CreamSeparator,
                        )
                        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                            Text("${(info.fraction() * 100).toInt()}%", color = tint, fontSize = 12.sp, fontWeight = FontWeight.Medium)
                            Text("${humanBytes(info.bytes)} / ${humanBytes(info.totalBytes)}", color = MutedForeground, fontSize = 12.sp)
                        }
                    }
                }
            }
        }
    }
}

/**
 * TransferNotifier 把传输进度写到系统下拉通知栏。
 *
 * - transfer 阶段：一条 ongoing（不可滑动清除）的进度通知，随进度更新进度条；
 * - done / rejected / error：更新为终态文案并去掉进度条，改为可清除、几秒后自动消失。
 *
 * 无通知权限（用户拒绝）时静默降级，不影响传输本身。
 */
private class TransferNotifier(private val context: Context) {
    private val manager = NotificationManagerCompat.from(context)

    init {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID, "文件传输进度", NotificationManager.IMPORTANCE_LOW,
            ).apply { description = "HopDrop 发送/接收文件时在通知栏展示进度" }
            val sys = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            sys.createNotificationChannel(channel)
        }
    }

    fun update(info: TransferInfo) {
        if (!hasPermission()) return

        val builder = NotificationCompat.Builder(context, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_upload)
            .setContentTitle("HopDrop · ${info.title()}")
            .setContentText(if (info.subtitle().isNotEmpty()) info.subtitle() else info.peerName)
            .setOnlyAlertOnce(true)

        if (info.isTerminal()) {
            // 终态：去掉进度条，允许滑动清除，几秒后自动消失。
            builder.setProgress(0, 0, false)
                .setOngoing(false)
                .setAutoCancel(true)
                .setTimeoutAfter(4000)
            builder.setSmallIcon(
                if (info.phase == "done") android.R.drawable.stat_sys_download_done
                else android.R.drawable.stat_notify_error
            )
        } else {
            // 进行中：ongoing 进度条。总字节已知时用百分比，未知时用不确定进度。
            val indeterminate = info.totalBytes <= 0 && info.totalFiles <= 0
            builder.setOngoing(true)
                .setProgress(100, (info.fraction() * 100).toInt(), indeterminate)
            builder.setSmallIcon(
                if (info.direction == "send") android.R.drawable.stat_sys_upload
                else android.R.drawable.stat_sys_download
            )
        }

        try {
            manager.notify(NOTIF_ID, builder.build())
        } catch (e: SecurityException) {
            // 权限在运行期被撤销等极端情况，忽略即可。
        }
    }

    fun cancel() {
        try {
            manager.cancel(NOTIF_ID)
        } catch (e: Exception) {
        }
    }

    private fun hasPermission(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return true
        return ContextCompat.checkSelfPermission(
            context, Manifest.permission.POST_NOTIFICATIONS,
        ) == PackageManager.PERMISSION_GRANTED
    }

    companion object {
        private const val CHANNEL_ID = "hopdrop_transfer"
        private const val NOTIF_ID = 1001
    }
}

/**
 * DownloadSink 是 gomobile Sink 接口的参考实现：把收到的文件写入系统下载目录。
 * 用一个自增 handle 关联已打开的 OutputStream。仅面向 Android 10+（MediaStore.Downloads）。
 */
private class DownloadSink(private val activity: ComponentActivity) : Sink {
    private val streams = HashMap<String, java.io.OutputStream>()
    private var seq = 0

    override fun openWrite(metaJSON: String): String {
        val meta = JSONObject(metaJSON)
        val name = meta.getString("name")
        val mime = meta.optString("mime_type", "")
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            if (mime.isNotEmpty()) put(MediaStore.Downloads.MIME_TYPE, mime)
            put(MediaStore.Downloads.RELATIVE_PATH, "Download/HopDrop")
        }
        val resolver = activity.contentResolver
        val uri = resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            ?: throw IllegalStateException("cannot create MediaStore entry for $name")

        val handle = "w${seq++}"
        streams[handle] = resolver.openOutputStream(uri)
            ?: throw IllegalStateException("cannot open output stream")
        return handle
    }

    override fun write(handle: String, data: ByteArray) {
        streams[handle]?.write(data)
    }

    override fun close(handle: String) {
        streams.remove(handle)?.use { it.flush() }
    }
}

/**
 * ContentResolverSource 是 gomobile Source 接口的参考实现：从系统文件选择器返回的
 * content:// URI 读取文件供发送。
 *
 * 元数据（名字/大小/MIME）来自 ContentResolver 查询；内容按需以 InputStream 分段读出，
 * 通过 readChunk 返回给 Go（返回空数组表示 EOF）。
 */
private class ContentResolverSource(
    private val resolver: android.content.ContentResolver,
    uris: List<Uri>,
) : Source {

    private class Entry(val uri: Uri, val meta: JSONObject)

    private val entries = ArrayList<Entry>(uris.size)
    private val streams = HashMap<String, java.io.InputStream>()

    init {
        uris.forEachIndexed { i, uri ->
            val id = i.toString()
            var name = "file_$id"
            var size = -1L
            resolver.query(uri, null, null, null, null)?.use { c ->
                val nameIdx = c.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                val sizeIdx = c.getColumnIndex(OpenableColumns.SIZE)
                if (c.moveToFirst()) {
                    if (nameIdx >= 0 && !c.isNull(nameIdx)) name = c.getString(nameIdx)
                    if (sizeIdx >= 0 && !c.isNull(sizeIdx)) size = c.getLong(sizeIdx)
                }
            }
            if (size < 0) {
                // 少数 provider 不给 SIZE 列，退回文件描述符长度，确保发送时字节数精确。
                size = try {
                    resolver.openAssetFileDescriptor(uri, "r")?.use { it.length } ?: 0L
                } catch (e: Exception) {
                    0L
                }
            }
            val mime = resolver.getType(uri) ?: ""
            val meta = JSONObject().apply {
                put("id", id)
                put("name", name)
                put("rel_path", name)
                put("size", size)
                put("mod_unix", 0)
                if (mime.isNotEmpty()) put("mime_type", mime)
            }
            entries.add(Entry(uri, meta))
        }
    }

    override fun listJSON(): String {
        val arr = JSONArray()
        entries.forEach { arr.put(it.meta) }
        return arr.toString()
    }

    override fun openRead(fileID: String): String {
        val entry = entries[fileID.toInt()]
        val stream = resolver.openInputStream(entry.uri)
            ?: throw IllegalStateException("cannot open input stream for ${entry.uri}")
        val handle = "r$fileID"
        streams[handle] = stream
        return handle
    }

    override fun readChunk(handle: String): ByteArray {
        val stream = streams[handle] ?: return ByteArray(0)
        val buf = ByteArray(128 * 1024)
        val n = stream.read(buf)
        if (n <= 0) return ByteArray(0)
        return if (n == buf.size) buf else buf.copyOf(n)
    }

    override fun closeRead(handle: String) {
        streams.remove(handle)?.close()
    }
}
