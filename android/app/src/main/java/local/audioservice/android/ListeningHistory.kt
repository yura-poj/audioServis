package local.audioservice.android

import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.AtomicFile
import android.util.Log
import org.json.JSONObject
import java.io.File
import java.io.IOException
import java.time.Instant
import java.util.UUID
import java.util.concurrent.Executors

internal data class ListeningPlay(
    val id: String, val track: String, val queue: String, val sequence: Long, val previous: String?,
    val started: String, val listened: Long, val duration: Long?, val reason: String, val outcome: String
) {
    fun json(): JSONObject = JSONObject().put("track_id", track).put("queue_id", queue)
        .put("sequence", sequence).put("previous_play_id", previous).put("started_at", started)
        .put("listened_ms", listened).put("duration_ms", duration).put("start_reason", reason).put("outcome", outcome)

    companion object {
        fun read(id: String, json: JSONObject) = ListeningPlay(id, json.getString("track_id"), json.getString("queue_id"),
            json.getLong("sequence"), json.optString("previous_play_id").takeIf { it.isNotEmpty() },
            json.getString("started_at"), json.getLong("listened_ms"), json.optLong("duration_ms").takeIf { it > 0 },
            json.getString("start_reason"), json.getString("outcome"))
    }
}

// Player callbacks and the journal run on the main looper. Only immutable snapshots go to the network worker.
internal class ListeningHistory(private val root: File) {
    private val main = Handler(Looper.getMainLooper())
    private val network = Executors.newSingleThreadExecutor()
    private val pending = mutableMapOf<String, ListeningPlay>()
    private var folder: File? = null
    private var api: APIClient? = null
    @Volatile private var generation = 0L
    private var sending = false
    private var active: ListeningPlay? = null
    private var since: Long? = null
    private var queue: String? = null
    private var sequence = 0L
    private var previous: String? = null

    private val tick = object : Runnable {
        override fun run() {
            if (since != null) checkpoint()
            flush()
            main.postDelayed(this, 5000)
        }
    }

    fun select(profile: Profile?, client: APIClient?) {
        finish("stopped")
        main.removeCallbacks(tick)
        generation++
        folder = profile?.let { File(root, it.scope()) }
        api = client
        pending.clear()
        previous = null
        queue = null
        val dir = folder ?: return
        for (file in dir.listFiles().orEmpty()) {
            if (file.extension != "json") continue
            runCatching {
                val json = JSONObject(String(AtomicFile(file).readFully(), Charsets.UTF_8))
                val saved = ListeningPlay.read(file.nameWithoutExtension, json)
                if (saved.outcome == "in_progress") save(saved.copy(outcome = "interrupted"))
                else pending[saved.id] = saved
            }.onFailure { Log.w("ListeningHistory", "Could not read saved playback", it) }
        }
        flush()
        main.postDelayed(tick, 5000)
    }

    fun playing(track: String, queueId: String, reason: String, duration: Long?) {
        if (folder == null) return
        if (active == null) {
            if (queue != queueId) {
                queue = queueId
                sequence = 0
            }
            val id = UUID.randomUUID().toString()
            sequence++
            val play = ListeningPlay(id, track, queueId, sequence, previous, Instant.now().toString(),
                0, duration, reason, "in_progress")
            active = play
            previous = id
            save(play)
            flush()
        } else if (active?.duration == null && duration != null) {
            active = active?.copy(duration = duration)
        }
        if (since == null) since = SystemClock.elapsedRealtime()
    }

    fun pause() {
        if (since != null) checkpoint()
        since = null
    }

    fun finish(outcome: String) {
        val play = active ?: return
        save(play.copy(listened = elapsed(play), outcome = outcome))
        active = null
        since = null
        flush()
    }

    private fun elapsed(play: ListeningPlay): Long = play.listened + (since?.let { SystemClock.elapsedRealtime() - it } ?: 0)

    private fun checkpoint() {
        val play = active ?: return
        val now = SystemClock.elapsedRealtime()
        val updated = play.copy(listened = play.listened + (since?.let { now - it } ?: 0))
        since = now
        active = updated
        save(updated)
    }

    private fun save(play: ListeningPlay) {
        val dir = folder ?: return
        pending[play.id] = play
        try {
            dir.mkdirs()
            val file = AtomicFile(File(dir, "${play.id}.json"))
            val out = file.startWrite()
            try {
                out.write(play.json().toString().toByteArray(Charsets.UTF_8))
                file.finishWrite(out)
            } catch (e: IOException) {
                file.failWrite(out)
                throw e
            }
        } catch (e: IOException) { Log.w("ListeningHistory", "Could not persist playback", e) }
    }

    private fun flush() {
        val client = api ?: return
        if (sending || pending.isEmpty()) return
        val batch = pending.values.toList()
        val owner = generation
        sending = true
        network.execute {
            val acknowledged = mutableListOf<ListeningPlay>()
            for (play in batch) {
                if (owner != generation || runCatching { client.sendPlay(play) }.isFailure) break
                acknowledged.add(play)
            }
            main.post {
                sending = false
                if (owner != generation) {
                    flush()
                    return@post
                }
                for (play in acknowledged) {
                    if (pending[play.id] != play) continue
                    if (play.outcome != "in_progress") AtomicFile(File(folder, "${play.id}.json")).delete()
                    pending.remove(play.id)
                }
                if (acknowledged.size == batch.size) flush()
            }
        }
    }
}
