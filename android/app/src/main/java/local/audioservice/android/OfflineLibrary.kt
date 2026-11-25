package local.audioservice.android

import android.os.Handler
import android.os.Looper
import android.util.AtomicFile
import org.json.JSONArray
import java.io.File
import java.io.IOException
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.Future

internal class OfflineLibrary(private val root: File, private val changed: () -> Unit) {
    private data class Job(val token: UUID, val future: Future<*>)

    private val main = Handler(Looper.getMainLooper())
    private val downloads = Executors.newSingleThreadExecutor()
    private val jobs = mutableMapOf<String, Job>()
    private var directory: File? = null

    var tracks: List<Track> = emptyList()
        private set
    var error: String? = null
        private set

    fun select(profile: Profile?) {
        jobs.values.forEach { it.future.cancel(true) }
        jobs.clear()
        error = null
        val dir = profile?.let { File(root, it.scope()) }
        directory = dir
        tracks = if (dir == null) emptyList() else runCatching {
            val data = JSONArray(index(dir).readFully().toString(Charsets.UTF_8))
            List(data.length()) { Track.parse(data.getJSONObject(it)) }.filter { file(it) != null }
        }.getOrDefault(emptyList())
    }

    fun file(track: Track): File? = directory?.let { dir ->
        audio(dir, track.id)?.takeIf { it.isFile && it.length() == track.sizeBytes }
    }

    fun downloading(track: Track): Boolean = jobs.containsKey(track.id)

    fun enqueue(track: Track, api: APIClient) {
        val dir = directory ?: return
        if (jobs.containsKey(track.id) || tracks.any { it.id == track.id && file(it) != null }) return
        val token = UUID.randomUUID()
        error = null
        val future = downloads.submit {
            var temporary: File? = null
            val result = runCatching {
                if (!dir.isDirectory && !dir.mkdirs() && !dir.isDirectory) throw IOException("Не удалось создать папку загрузок.")
                val stage = File.createTempFile("track-", ".part", dir)
                temporary = stage
                api.download(track, stage)
                stage
            }
            main.post {
                try {
                    if (jobs[track.id]?.token != token) return@post
                    publish(dir, track, result.getOrThrow())
                } catch (problem: Exception) {
                    error = "${track.title} · ${problem.localizedMessage}"
                } finally {
                    temporary?.delete()
                    if (jobs[track.id]?.token == token) {
                        jobs.remove(track.id)
                        changed()
                    }
                }
            }
        }
        jobs[track.id] = Job(token, future)
        changed()
    }

    fun remove(track: Track) {
        jobs.remove(track.id)?.future?.cancel(true)
        val dir = directory ?: return
        runCatching {
            val updated = tracks.filter { it.id != track.id }
            if (updated != tracks) persist(dir, updated)
            audio(dir, track.id)?.delete()
        }.onFailure { error = it.localizedMessage }
        changed()
    }

    fun reconcile(catalog: List<Track>) {
        val dir = directory ?: return
        val byId = catalog.associateBy { it.id }
        for (id in jobs.keys.toList()) if (id !in byId) jobs.remove(id)?.future?.cancel(true)
        val updated = tracks.mapNotNull { byId[it.id] }
        if (updated == tracks) return
        val deleted = tracks.filter { it.id !in byId }
        runCatching {
            persist(dir, updated)
            deleted.forEach { audio(dir, it.id)?.delete() }
        }.onFailure { error = it.localizedMessage }
    }

    private fun publish(dir: File, track: Track, staged: File) {
        val destination = audio(dir, track.id) ?: throw IOException("Сервер вернул неверный идентификатор трека.")
        if (!staged.renameTo(destination)) throw IOException("Не удалось сохранить MP3.")
        try {
            persist(dir, tracks.filter { it.id != track.id } + track)
        } catch (problem: Exception) {
            destination.delete()
            throw problem
        }
    }

    private fun persist(dir: File, updated: List<Track>) {
        val document = JSONArray().apply { updated.forEach { put(it.json()) } }
        val file = index(dir)
        val stream = file.startWrite()
        try {
            stream.write(document.toString().toByteArray(Charsets.UTF_8))
            file.finishWrite(stream)
        } catch (problem: Exception) {
            file.failWrite(stream)
            throw problem
        }
        tracks = updated
    }

    private fun audio(dir: File, id: String): File? =
        runCatching { File(dir, "${UUID.fromString(id)}.mp3") }.getOrNull()

    private fun index(dir: File) = AtomicFile(File(dir, "index.json"))
}
