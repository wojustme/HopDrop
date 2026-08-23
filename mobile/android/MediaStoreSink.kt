package com.hopdrop.app

import android.content.ContentValues
import android.content.Context
import android.os.Environment
import android.provider.MediaStore
import com.hopdrop.mobile.Sink
import kotlinx.serialization.json.Json
import java.io.OutputStream
import java.util.concurrent.ConcurrentHashMap

/**
 * MediaStoreSink 是 gomobile Sink 接口的一个参考实现：把 HopDrop 收到的文件写入
 * 系统下载目录（也可改成写相册 MediaStore.Images）。
 *
 * gomobile 会把 Go 侧的 Sink 接口映射成本类需要实现的方法：
 *   openWrite(metaJSON) -> handle
 *   write(handle, data)
 *   close(handle)
 *
 * 我们用一个自增 handle 关联一个已打开的 OutputStream。
 */
class MediaStoreSink(private val context: Context) : Sink {
    private val json = Json { ignoreUnknownKeys = true }
    private val streams = ConcurrentHashMap<String, OutputStream>()
    private var seq = 0

    override fun openWrite(metaJSON: String): String {
        val meta = json.decodeFromString<FileMeta>(metaJSON)
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, meta.name)
            if (meta.mime_type.isNotEmpty()) {
                put(MediaStore.Downloads.MIME_TYPE, meta.mime_type)
            }
            // 保留子目录结构（rel_path 里的目录部分）。
            val sub = meta.rel_path.substringBeforeLast('/', "")
            val rel = if (sub.isEmpty()) "Download/HopDrop" else "Download/HopDrop/$sub"
            put(MediaStore.Downloads.RELATIVE_PATH, rel)
        }
        val uri = context.contentResolver.insert(
            MediaStore.Downloads.EXTERNAL_CONTENT_URI, values
        ) ?: throw IllegalStateException("cannot create MediaStore entry for ${meta.name}")

        val handle = "w${seq++}"
        streams[handle] = context.contentResolver.openOutputStream(uri)
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
