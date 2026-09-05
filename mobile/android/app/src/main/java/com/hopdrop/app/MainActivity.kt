package com.hopdrop.app

import android.content.ContentValues
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
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
    private var multicastLock: android.net.wifi.WifiManager.MulticastLock? = null

    // —— Compose 可观察状态 ——
    private val peersState = mutableStateOf<List<PeerItem>>(emptyList())
    private val statusState = mutableStateOf("正在发现设备…")
    private val selectedIdState = mutableStateOf<String?>(null)
    /** 待用户确认的入站传输；非空时展示确认弹窗。 */
    private val pendingOfferState = mutableStateOf<PendingOffer?>(null)

    /** 系统文件选择器：可多选，返回若干 content:// URI。 */
    private val pickFiles = registerForActivityResult(
        ActivityResultContracts.OpenMultipleDocuments()
    ) { uris -> if (!uris.isNullOrEmpty()) startSend(uris) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 组播必须持有 MulticastLock，否则收不到设备发现广播。
        val wifi = applicationContext
            .getSystemService(WIFI_SERVICE) as android.net.wifi.WifiManager
        multicastLock = wifi.createMulticastLock("hopdrop").apply {
            setReferenceCounted(true)
            acquire()
        }

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
                val phase = p.getString("phase")
                val dir = p.optString("direction")
                val verb = if (dir == "send") "发送" else "接收"
                val text = when (phase) {
                    "transfer" -> "$verb ${p.getInt("files")}/${p.getInt("total_files")} · " +
                        p.optString("current_name")
                    "done" -> "$verb完成 · ${p.getInt("files")} 个文件"
                    "rejected" -> "对方拒绝了本次传输"
                    "error" -> "出错: ${p.optString("err")}"
                    else -> "$verb中…"
                }
                runOnUiThread { statusState.value = text }
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

                    HopDropScreen(
                        peers = peers,
                        status = status,
                        selectedId = selectedId,
                        onSelect = { selectedIdState.value = it },
                        onSend = ::onSendClicked,
                    )

                    pending?.let { offer ->
                        OfferDialog(
                            offer = offer,
                            onAccept = { respondOffer(offer.id, true) },
                            onReject = { respondOffer(offer.id, false) },
                        )
                    }
                }
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        if (::client.isInitialized) client.stop()
        multicastLock?.release()
    }

    /** 点“发送文件”：先确认已选设备，再拉起系统文件选择器。 */
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
 * HopDropScreen 是米白主题的主界面：品牌头 + 状态条 + 在线设备卡片列表 + 发送按钮。
 */
@androidx.compose.runtime.Composable
private fun HopDropScreen(
    peers: List<PeerItem>,
    status: String,
    selectedId: String?,
    onSelect: (String) -> Unit,
    onSend: () -> Unit,
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

        // —— 发送按钮 ——
        Button(
            onClick = onSend,
            modifier = Modifier
                .fillMaxWidth()
                .height(52.dp),
            shape = RoundedCornerShape(12.dp),
            colors = ButtonDefaults.buttonColors(
                containerColor = AccentTeal,
                contentColor = CreamSurface,
            ),
        ) {
            Text("发送文件", fontSize = 16.sp, fontWeight = FontWeight.Medium)
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
