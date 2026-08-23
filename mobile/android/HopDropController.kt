package com.hopdrop.app

import android.content.Context
import com.hopdrop.mobile.*    // gomobile 生成的绑定包（build-android.sh 产出的 hopdrop.aar）
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * HopDropController 把 gomobile 生成的 Client 封装成 Android 侧更顺手的形态。
 *
 * 使用步骤：
 *   1. 用 scripts/build-android.sh 生成 hopdrop.aar，作为模块依赖加入本工程。
 *   2. 在 AndroidManifest.xml 声明网络与组播权限（见文件末尾注释）。
 *   3. 在 Activity/ViewModel 中构造 HopDropController，实现回调更新 UI。
 *
 * 注意：Go 回调运行在非主线程，务必用 runOnUiThread / Handler 切回主线程再更新 Compose 状态。
 */
class HopDropController(
    private val context: Context,
    private val onPeers: (List<PeerDevice>) -> Unit,
    private val onOffer: (offerId: String, offer: OfferInfo) -> Unit,
    private val onProgress: (ProgressInfo) -> Unit,
) {
    private val json = Json { ignoreUnknownKeys = true }
    private lateinit var client: Client
    private var multicastLock: android.net.wifi.WifiManager.MulticastLock? = null

    fun start() {
        // 组播必须持有 MulticastLock，否则收不到设备发现广播。
        val wifi = context.applicationContext
            .getSystemService(Context.WIFI_SERVICE) as android.net.wifi.WifiManager
        multicastLock = wifi.createMulticastLock("hopdrop").apply {
            setReferenceCounted(true)
            acquire()
        }

        val callback = object : Callback {
            override fun onPeers(peersJSON: String) {
                onPeers(json.decodeFromString<List<PeerDevice>>(peersJSON))
            }
            override fun onOffer(offerID: String, offerJSON: String) {
                onOffer(offerID, json.decodeFromString(offerJSON))
            }
            override fun onProgress(progressJSON: String) {
                onProgress(json.decodeFromString(progressJSON))
            }
        }

        // MediaStoreSink 由宿主实现，把收到的文件写进相册/下载目录。
        val sink = MediaStoreSink(context)
        client = Mobile.newClient(
            android.os.Build.MODEL ?: "Android",
            "android",
            context.filesDir.absolutePath,
            sink,
            callback,
        )
        client.start(0)
    }

    fun stop() {
        if (::client.isInitialized) client.stop()
        multicastLock?.release()
    }

    /** 用户在 OnOffer 弹窗里点了接受/拒绝后调用。 */
    fun respond(offerId: String, accept: Boolean) = client.respond(offerId, accept)

    /** 把一批本地 uri 对应的文件发送给某台设备。 */
    fun send(deviceId: String, source: Source) = client.sendToDevice(deviceId, source)
}

@Serializable
data class PeerDevice(val id: String, val name: String, val platform: String, val sync_port: Int)

@Serializable
data class FileMeta(
    val id: String, val name: String, val rel_path: String,
    val size: Long, val mod_unix: Long, val mime_type: String = "",
)

@Serializable
data class OfferInfo(val peer: PeerDevice, val files: List<FileMeta>, val total_bytes: Long)

@Serializable
data class ProgressInfo(
    val direction: String, val peer_id: String, val peer_name: String,
    val phase: String, val current_name: String,
    val files: Int, val total_files: Int,
    val bytes: Long, val total_bytes: Long, val err: String = "",
)

/*
AndroidManifest.xml 需要声明：

    <uses-permission android:name="android.permission.INTERNET"/>
    <uses-permission android:name="android.permission.ACCESS_NETWORK_STATE"/>
    <uses-permission android:name="android.permission.ACCESS_WIFI_STATE"/>
    <uses-permission android:name="android.permission.CHANGE_WIFI_MULTICAST_STATE"/>
    <uses-permission android:name="android.permission.READ_MEDIA_IMAGES"/>
    <uses-permission android:name="android.permission.READ_MEDIA_VIDEO"/>

Android 14+ 建议把接收服务放到前台服务（dataSync 类型）以便后台保活。
*/
