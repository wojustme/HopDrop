package com.hopdrop.app

import android.content.ContentValues
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
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
 * 它展示局域网内在线设备，并把收到的传输请求自动接受、落到系统下载目录，
 * 用于验证 gomobile 绑定（hopdrop.aar）在真机/APK 里能正常发现与收发。
 *
 * 说明：为保持依赖最小、CI 里能稳定 assembleRelease，本参考实现：
 *   - 用 org.json 手工解析绑定层回传的 JSON（不引入 kotlinx-serialization）。
 *   - 收到 Offer 默认自动接受（真实产品应弹窗询问）。
 */
class MainActivity : ComponentActivity() {

    private lateinit var client: Client
    private var multicastLock: android.net.wifi.WifiManager.MulticastLock? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 组播必须持有 MulticastLock，否则收不到设备发现广播。
        val wifi = applicationContext
            .getSystemService(WIFI_SERVICE) as android.net.wifi.WifiManager
        multicastLock = wifi.createMulticastLock("hopdrop").apply {
            setReferenceCounted(true)
            acquire()
        }

        val peersState = mutableStateOf<List<PeerItem>>(emptyList())
        val statusState = mutableStateOf("正在发现设备…")

        val callback = object : Callback {
            override fun onPeers(peersJSON: String) {
                val arr = JSONArray(peersJSON)
                val list = ArrayList<PeerItem>(arr.length())
                for (i in 0 until arr.length()) {
                    val dev = arr.getJSONObject(i).getJSONObject("device")
                    list.add(PeerItem(dev.getString("name"), dev.getString("platform")))
                }
                runOnUiThread { peersState.value = list }
            }

            override fun onOffer(offerID: String, offerJSON: String) {
                val offer = JSONObject(offerJSON)
                val n = offer.getJSONArray("files").length()
                runOnUiThread { statusState.value = "收到 $n 个文件，正在接收…" }
                // 参考实现：默认接受。
                client.respond(offerID, true)
            }

            override fun onProgress(progressJSON: String) {
                val p = JSONObject(progressJSON)
                runOnUiThread {
                    statusState.value = "${p.getString("phase")} " +
                        "${p.getInt("files")}/${p.getInt("total_files")}"
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

        setContent {
            MaterialTheme(colorScheme = HopDropColorScheme) {
                Surface(
                    modifier = Modifier.fillMaxSize(),
                    color = MaterialTheme.colorScheme.background,
                ) {
                    val peers by peersState
                    val status by statusState
                    HopDropScreen(peers = peers, status = status)
                }
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        if (::client.isInitialized) client.stop()
        multicastLock?.release()
    }
}

/** PeerItem 是设备列表用的最小展示模型。 */
private data class PeerItem(val name: String, val platform: String)

/**
 * HopDropScreen 是米白主题的主界面：品牌头 + 状态条 + 在线设备卡片列表 + 发送按钮。
 */
@androidx.compose.runtime.Composable
private fun HopDropScreen(peers: List<PeerItem>, status: String) {
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
                    items(peers) { p -> PeerRow(p) }
                }
            }
        }

        // —— 发送按钮 ——
        Button(
            onClick = { /* 发送功能：选取文件后 client.sendToDevice(...) */ },
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

/** PeerRow 是单台设备的卡片行：状态点 + 名称 + 平台标签。 */
@androidx.compose.runtime.Composable
private fun PeerRow(p: PeerItem) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(CreamInput, RoundedCornerShape(10.dp))
            .border(1.dp, CreamSeparator, RoundedCornerShape(10.dp))
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
        Text(p.platform, color = MutedForeground, fontSize = 13.sp)
    }
}

/**
 * DownloadSink 是 gomobile Sink 接口的参考实现：把收到的文件写入系统下载目录。
 * 用一个自增 handle 关联已打开的 OutputStream。
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
        val uri = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            resolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
        } else {
            resolver.insert(MediaStore.Files.getContentUri("external"), values)
        } ?: throw IllegalStateException("cannot create MediaStore entry for $name")

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
