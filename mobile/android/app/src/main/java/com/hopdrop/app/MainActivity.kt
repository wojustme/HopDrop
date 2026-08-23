package com.hopdrop.app

import android.content.ContentValues
import android.os.Build
import android.os.Bundle
import android.provider.MediaStore
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.hopdrop.mobile.Callback
import com.hopdrop.mobile.Client
import com.hopdrop.mobile.Mobile
import com.hopdrop.mobile.Sink
import org.json.JSONArray
import org.json.JSONObject

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

        val peersState = mutableStateOf<List<String>>(emptyList())
        val statusState = mutableStateOf("正在发现设备…")

        val callback = object : Callback {
            override fun onPeers(peersJSON: String) {
                val arr = JSONArray(peersJSON)
                val names = ArrayList<String>(arr.length())
                for (i in 0 until arr.length()) {
                    val dev = arr.getJSONObject(i).getJSONObject("device")
                    names.add("${dev.getString("name")} · ${dev.getString("platform")}")
                }
                runOnUiThread { peersState.value = names }
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
            MaterialTheme {
                Surface(modifier = Modifier.fillMaxSize()) {
                    val peers by peersState
                    val status by statusState
                    Column(
                        modifier = Modifier.fillMaxSize().padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Text("HopDrop", style = MaterialTheme.typography.headlineSmall)
                        Text(status)
                        Text("在线设备（${peers.size}）:", style = MaterialTheme.typography.titleMedium)
                        LazyColumn(modifier = Modifier.fillMaxWidth()) {
                            items(peers) { name -> Text("• $name", modifier = Modifier.padding(vertical = 4.dp)) }
                        }
                        Button(onClick = { /* 发送功能：选取文件后 client.sendToDevice(...) */ }) {
                            Text("发送文件（示例占位）")
                        }
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
