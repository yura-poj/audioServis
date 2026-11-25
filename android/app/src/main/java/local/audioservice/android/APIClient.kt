package local.audioservice.android

import android.net.Uri
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import java.util.Locale

internal data class Track(val id: String, val title: String, val sizeBytes: Long, val createdAt: String, val liked: Boolean) {
    fun json(): JSONObject = JSONObject().put("id", id).put("title", title)
        .put("size_bytes", sizeBytes).put("created_at", createdAt).put("liked", liked)

    companion object {
        fun parse(item: JSONObject) = Track(item.getString("id"), item.getString("title"),
            item.getLong("size_bytes"), item.getString("created_at"), item.getBoolean("liked"))
    }
}

internal data class Playlist(val id: String, val name: String) {
    companion object {
        fun parse(item: JSONObject) = Playlist(item.getString("id"), item.getString("name"))
    }
}

internal class APIClient(val base: String, val key: String) {
    companion object {
        fun address(input: String): String {
            val message = "Укажи HTTPS-адрес сервера или http://localhost для локальной проверки."
            val uri = try { URI(input.trim()) } catch (_: Exception) { throw IOException(message) }
            val scheme = uri.scheme?.lowercase(Locale.ROOT)
            val host = uri.host?.lowercase(Locale.ROOT)
            if (host.isNullOrEmpty() || (scheme != "https" && !(scheme == "http" && host == "localhost")) ||
                uri.userInfo != null || uri.query != null || uri.fragment != null ||
                uri.path !in listOf("", "/") || uri.port !in -1..65535 || uri.port == 0
            ) throw IOException(message)
            val port = if (scheme == "https" && uri.port == 443 || scheme == "http" && uri.port == 80) -1 else uri.port
            return URI(scheme, null, host, port, null, null, null).toASCIIString()
        }
    }

    fun profile(): Profile {
        val listener = jsonRequest("GET", api("me"))
        return Profile(base, listener.getString("id"), listener.getString("display_name"))
    }

    fun playlists(): List<Playlist> = items(jsonRequest("GET", api("playlists")), Playlist::parse)

    fun tracks(playlist: String? = null): List<Track> {
        val path = if (playlist == null) "tracks" else "playlists/$playlist/tracks"
        return items(jsonRequest("GET", api(path)), Track::parse)
    }

    fun savePlaylist(name: String, id: String?) {
        val input = JSONObject().put("name", name)
        if (id == null) jsonRequest("POST", api("playlists"), input)
        else jsonRequest("PATCH", api("playlists/$id"), input)
    }

    fun renameTrack(id: String, title: String) {
        jsonRequest("PATCH", api("tracks/$id"), JSONObject().put("title", title))
    }

    fun deletePlaylist(id: String) { jsonRequest("DELETE", api("playlists/$id")) }
    fun deleteTrack(id: String) { jsonRequest("DELETE", api("tracks/$id")) }

    fun membership(track: String, playlist: String, add: Boolean) {
        jsonRequest(if (add) "PUT" else "DELETE", api("playlists/$playlist/tracks/$track"))
    }

    fun like(track: String, liked: Boolean) = Track.parse(
        jsonRequest(if (liked) "PUT" else "DELETE", api("tracks/$track/like"))
    )

    fun upload(input: InputStream, title: String, playlist: String?) {
        val address = api("tracks").buildUpon().appendQueryParameter("title", title)
        if (playlist != null) address.appendQueryParameter("playlist_id", playlist)
        connected(address.build(), "POST") { connection ->
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "audio/mpeg")
            connection.setChunkedStreamingMode(64 * 1024)
            connection.outputStream.use { input.copyTo(it) }
            check(connection)
        }
    }

    fun download(track: Track, temporary: File) {
        connected(media(track.id), "GET") { connection ->
            check(connection)
            if (connection.responseCode != 200) throw IOException("Сервер не вернул полный файл.")
            connection.inputStream.use { input -> temporary.outputStream().use { input.copyTo(it) } }
            if (temporary.length() != track.sizeBytes) throw IOException("Получен неполный файл.")
        }
    }

    fun media(id: String): Uri = api("media/$id")

    fun ownsMedia(uri: Uri): Boolean {
        val origin = Uri.parse(base)
        fun port(url: Uri) = if (url.port != -1) url.port else if (url.scheme == "https") 443 else 80
        return uri.scheme == origin.scheme && uri.host.equals(origin.host, ignoreCase = true) &&
            port(uri) == port(origin) && uri.path.orEmpty().startsWith("/api/v1/media/")
    }

    private fun api(path: String) = Uri.parse("$base/api/v1/$path")

    private fun jsonRequest(method: String, uri: Uri, json: JSONObject? = null) =
        connected(uri, method) { connection ->
            if (json != null) {
                val bytes = json.toString().toByteArray(Charsets.UTF_8)
                connection.doOutput = true
                connection.setFixedLengthStreamingMode(bytes.size)
                connection.setRequestProperty("Content-Type", "application/json")
                connection.outputStream.use { it.write(bytes) }
            }
            check(connection)
            if (connection.responseCode == 204) JSONObject()
            else connection.inputStream.bufferedReader(Charsets.UTF_8).use { JSONObject(it.readText()) }
        }

    private fun <T> connected(uri: Uri, method: String, block: (HttpURLConnection) -> T): T {
        val connection = URL(uri.toString()).openConnection() as HttpURLConnection
        try {
            connection.requestMethod = method
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 8_000
            connection.readTimeout = 20_000
            connection.setRequestProperty("Authorization", "Bearer $key")
            return block(connection)
        } finally {
            connection.disconnect()
        }
    }

    private fun check(connection: HttpURLConnection) {
        val code = connection.responseCode
        if (code in 200..299) return
        if (code == 401) throw IOException("Сервер отклонил личный ключ.")
        val message = runCatching {
            connection.errorStream?.bufferedReader(Charsets.UTF_8)?.use {
                JSONObject(it.readText()).getJSONObject("error").getString("message")
            }
        }.getOrNull()
        throw IOException(message ?: "Запрос завершился ошибкой HTTP $code.")
    }

    private fun <T> items(reply: JSONObject, parse: (JSONObject) -> T): List<T> {
        val array = reply.getJSONArray("items")
        return List(array.length()) { parse(array.getJSONObject(it)) }
    }
}
