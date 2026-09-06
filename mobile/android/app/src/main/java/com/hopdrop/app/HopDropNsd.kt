package com.hopdrop.app

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build

/**
 * HopDropNsd 用 Android 系统的 NsdManager（网络服务发现 / 底层就是标准 mDNS-DNS/SD）
 * 做局域网设备发现，替代在 Go 里自开组播。
 *
 * 与 iOS(NWBrowser/NetService)、桌面(Go mDNS) 用同一个服务类型 `_hopdrop._tcp`，
 * 因此三端能互相发现。设备身份放在 TXT 记录：id / name / platform。
 *
 * 关键点：真正发组播的是 Android 系统服务，App 只调 API —— 无需自己持 MulticastLock，
 * 也不必自研组播。发现到（并解析出 host:port）的 peer 通过回调交给上层喂给 Go 的 Client。
 *
 * @param selfId       本机设备 ID（用于注册实例名 + 过滤掉自己）。
 * @param selfName     本机设备名（写入 TXT，供对端展示）。
 * @param selfPlatform 本机平台（"android"）。
 * @param port         本机 TCP 同步端口（Go 引擎实际监听的端口）。
 */
class HopDropNsd(
    private val context: Context,
    private val selfId: String,
    private val selfName: String,
    private val selfPlatform: String,
    private val selfFingerprint: String,
    private val port: Int,
) {
    /** 解析出一台设备时回调：id/name/platform/fingerprint/host/port。 */
    var onResolved: ((String, String, String, String, String, Int) -> Unit)? = null
    /** 一台设备离线（服务消失）时回调：id。 */
    var onRemoved: ((String) -> Unit)? = null

    private val nsd: NsdManager =
        context.getSystemService(Context.NSD_SERVICE) as NsdManager

    private var registrationListener: NsdManager.RegistrationListener? = null
    private var discoveryListener: NsdManager.DiscoveryListener? = null
    /** 服务名 → 设备 ID，用于服务消失时回调 onRemoved。 */
    private val nameToId = HashMap<String, String>()
    /** 正在解析中的服务名，避免对同一服务重复发起 resolve。 */
    private val resolving = HashSet<String>()

    companion object {
        private const val SERVICE_TYPE = "_hopdrop._tcp."
    }

    /** 启动：注册本机服务 + 开始发现。 */
    fun start() {
        registerService()
        startDiscovery()
    }

    /** 停止：注销服务 + 停止发现，释放监听器。 */
    fun stop() {
        registrationListener?.let { runCatching { nsd.unregisterService(it) } }
        registrationListener = null
        discoveryListener?.let { runCatching { nsd.stopServiceDiscovery(it) } }
        discoveryListener = null
        nameToId.clear()
        resolving.clear()
    }

    // MARK: - 注册本机服务

    private fun registerService() {
        if (port <= 0) return
        val info = NsdServiceInfo().apply {
            serviceName = selfId
            serviceType = SERVICE_TYPE
            setPort(port)
            // TXT 属性需 API 21+，本工程 minSdk=29，直接写即可。
            setAttribute("id", selfId)
            setAttribute("name", selfName)
            setAttribute("platform", selfPlatform)
            setAttribute("fingerprint", selfFingerprint)
        }
        val listener = object : NsdManager.RegistrationListener {
            override fun onServiceRegistered(info: NsdServiceInfo) {}
            override fun onRegistrationFailed(info: NsdServiceInfo, errorCode: Int) {}
            override fun onServiceUnregistered(info: NsdServiceInfo) {}
            override fun onUnregistrationFailed(info: NsdServiceInfo, errorCode: Int) {}
        }
        registrationListener = listener
        runCatching {
            nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, listener)
        }
    }

    // MARK: - 发现设备

    private fun startDiscovery() {
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {}
            override fun onDiscoveryStopped(serviceType: String) {}
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                runCatching { nsd.stopServiceDiscovery(this) }
            }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                runCatching { nsd.stopServiceDiscovery(this) }
            }

            override fun onServiceFound(info: NsdServiceInfo) {
                // 忽略自己（实例名即本机设备 ID）。
                if (info.serviceName == selfId) return
                val name = info.serviceName
                if (resolving.contains(name)) return
                resolving.add(name)
                resolve(info)
            }

            override fun onServiceLost(info: NsdServiceInfo) {
                val name = info.serviceName
                resolving.remove(name)
                val id = nameToId.remove(name)
                if (id != null) onRemoved?.invoke(id)
            }
        }
        discoveryListener = listener
        runCatching {
            nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener)
        }
    }

    /** resolve 把发现到的服务解析成具体 IP:port + TXT。 */
    private fun resolve(service: NsdServiceInfo) {
        // Android 14(API 34)起 resolveService 被弃用，推荐 registerServiceInfoCallback；
        // 为兼容 minSdk=29，这里统一用 resolveService（在 34 上仍可用，仅告警）。
        val resolveListener = object : NsdManager.ResolveListener {
            override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) {
                resolving.remove(info.serviceName)
            }

            override fun onServiceResolved(info: NsdServiceInfo) {
                resolving.remove(info.serviceName)
                val host = info.host ?: return
                val addr = host.hostAddress
                if (addr.isNullOrEmpty()) return

                val txt = readTxt(info)
                val realId = txt.id.ifEmpty { info.serviceName }
                if (realId == selfId) return

                nameToId[info.serviceName] = realId
                onResolved?.invoke(
                    realId,
                    txt.name.ifEmpty { info.serviceName },
                    txt.platform,
                    txt.fingerprint,
                    addr,
                    info.port,
                )
            }
        }
        runCatching { nsd.resolveService(service, resolveListener) }
    }

    private data class IdentityTxt(
        val id: String,
        val name: String,
        val platform: String,
        val fingerprint: String,
    )

    private fun readTxt(info: NsdServiceInfo): IdentityTxt {
        var id = ""
        var name = ""
        var platform = ""
        var fingerprint = ""
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            val attrs = info.attributes ?: emptyMap()
            id = attrs["id"]?.let { String(it) } ?: ""
            name = attrs["name"]?.let { String(it) } ?: ""
            platform = attrs["platform"]?.let { String(it) } ?: ""
            fingerprint = attrs["fingerprint"]?.let { String(it) } ?: ""
        }
        return IdentityTxt(id, name, platform, fingerprint)
    }
}
