package local.audioservice.android

import android.app.Application
import android.content.ComponentName
import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MediaMetadata
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.session.MediaController
import androidx.media3.session.SessionToken
import java.io.IOException
import java.io.InputStream
import java.util.UUID
import java.util.concurrent.Executors

class AudioServiceApp : Application() {
    internal val library by lazy { LibraryState(this) }
}

internal class LibraryState(private val app: Application) {
    private val main = Handler(Looper.getMainLooper())
    private val requests = Executors.newSingleThreadExecutor()
    private val accounts = AccountStore(app)
    val offline = OfflineLibrary(app.filesDir.resolve("downloads"), ::changed)

    var onChanged: (() -> Unit)? = null
    var profile: Profile? = accounts.active()
        private set
    @Volatile var api: APIClient? = profile?.let { selected ->
        accounts.credential()?.let { APIClient(selected.base, it) }
    }
        private set
    var playlists: List<Playlist> = emptyList()
        private set
    var tracks: List<Track> = emptyList()
        private set
    var playlistTracks: List<Track> = emptyList()
        private set
    var playlist: Playlist? = null
        private set
    var error: String? = null
        private set
    var connecting: Boolean = false
        private set

    private var epoch = UUID.randomUUID()
    private var playlistLoad = UUID.randomUUID()
    private var catalogLoad = UUID.randomUUID()
    private var controller: MediaController? = null

    val currentTitle: String?
        get() = controller?.currentMediaItem?.mediaMetadata?.title?.toString()

    val currentTrackId: String?
        get() = controller?.currentMediaItem?.mediaId

    val activePlayback: Boolean
        get() = controller?.playWhenReady == true && controller?.playbackState != Player.STATE_ENDED

    init {
        offline.select(profile)
    }

    fun preparePlayback() {
        if (controller != null || connectingPlayer) return
        connectingPlayer = true
        val token = SessionToken(app, ComponentName(app, PlaybackService::class.java))
        val future = MediaController.Builder(app, token).buildAsync()
        future.addListener({
            connectingPlayer = false
            runCatching { future.get() }.onSuccess { player ->
                controller = player
                player.addListener(object : Player.Listener {
                    override fun onEvents(player: Player, events: Player.Events) {
                        changed()
                    }

                    override fun onPlayerError(problem: PlaybackException) {
                        error = "Не удалось воспроизвести трек · ${problem.localizedMessage}"
                        changed()
                    }
                })
            }.onFailure { error = "Не удалось запустить плеер." }
            changed()
        }, { command -> main.post(command) })
    }

    private var connectingPlayer = false

    fun connect(address: String, key: String, reply: (Boolean) -> Unit) {
        if (connecting) return
        val previous = epoch
        connecting = true
        error = null
        changed()
        requests.execute {
            val result = runCatching {
                val secret = key.trim()
                if (secret.isEmpty()) throw IOException("Укажи личный ключ.")
                val client = APIClient(APIClient.address(address), secret)
                client.profile() to client
            }
            main.post {
                if (epoch != previous) return@post
                connecting = false
                val applied = result.mapCatching { (profile, client) ->
                    accounts.setActive(profile, client.key)
                    selectAccount(profile, client)
                }
                applied.onFailure { error = it.localizedMessage }
                reply(applied.isSuccess)
                changed()
                if (applied.isSuccess) refresh()
            }
        }
    }

    fun logout() {
        accounts.clear()
        selectAccount(null, null)
        changed()
    }

    private fun selectAccount(selected: Profile?, client: APIClient?) {
        epoch = UUID.randomUUID()
        connecting = false
        controller?.stop()
        controller?.clearMediaItems()
        tracks = emptyList()
        playlists = emptyList()
        playlistTracks = emptyList()
        playlist = null
        profile = selected
        api = client
        offline.select(selected)
        error = null
    }

    fun refresh() {
        if (profile == null) return
        val request = UUID.randomUUID()
        catalogLoad = request
        request({ it.tracks() to it.playlists() }) { (catalog, lists) ->
            if (catalogLoad != request) return@request
            tracks = catalog
            playlists = lists
            offline.reconcile(catalog)
            val selected = playlist
            if (selected != null) selectPlaylist(lists.firstOrNull { it.id == selected.id })
        }
    }

    fun selectPlaylist(item: Playlist?) {
        val request = UUID.randomUUID()
        playlistLoad = request
        if (playlist?.id != item?.id) playlistTracks = emptyList()
        playlist = item
        changed()
        if (item == null) return
        request({ it.tracks(item.id) }) { tracks ->
            if (playlistLoad == request && playlist?.id == item.id) playlistTracks = tracks
        }
    }

    fun savePlaylist(name: String, id: String?) = mutate { it.savePlaylist(name, id) }
    fun renameTrack(track: Track, title: String) = mutate { it.renameTrack(track.id, title) }
    fun deletePlaylist(playlist: Playlist) = mutate { it.deletePlaylist(playlist.id) }

    fun deleteTrack(track: Track) {
        request({ it.deleteTrack(track.id) }) {
            offline.remove(track)
            refresh()
        }
    }

    fun membership(track: Track, playlist: String, add: Boolean) =
        mutate { it.membership(track.id, playlist, add) }

    fun upload(title: String, playlist: String?, source: () -> InputStream?) {
        mutate { client ->
            val input = source() ?: throw IOException("Не удалось открыть файл.")
            input.use { client.upload(it, title, playlist) }
        }
    }

    fun toggleLike(track: Track) {
        request({ it.like(track.id, !track.liked) }) { updated ->
            tracks = tracks.map { if (it.id == updated.id) updated else it }
            playlistTracks = playlistTracks.map { if (it.id == updated.id) updated else it }
            if (updated.liked) api?.let { offline.enqueue(updated, it) }
        }
    }

    fun shuffle() {
        request({ it.tracks() }) { catalog ->
            tracks = catalog
            offline.reconcile(catalog)
            play(catalog.shuffled())
        }
    }

    fun play(tracks: List<Track>, startId: String? = null, localOnly: Boolean = false) {
        val player = controller
        if (player == null) {
            preparePlayback()
            error = "Плеер запускается. Повтори воспроизведение."
            changed()
            return
        }
        val items = tracks.mapNotNull { track ->
            val local = offline.file(track)
            val source = local?.let { Uri.fromFile(it) } ?: if (localOnly) null else api?.media(track.id)
            source?.let {
                MediaItem.Builder().setUri(it).setMediaId(track.id)
                    .setMediaMetadata(MediaMetadata.Builder().setTitle(track.title).build())
                    .build()
            }
        }
        if (items.isEmpty()) return
        val index = items.indexOfFirst { it.mediaId == startId }.coerceAtLeast(0)
        error = null
        player.setMediaItems(items, index, C.TIME_UNSET)
        player.prepare()
        player.play()
        changed()
    }

    fun togglePlayback() {
        val player = controller ?: return
        if (activePlayback) player.pause() else player.play()
    }

    fun next() {
        controller?.seekToNextMediaItem()
    }

    fun previous() {
        controller?.seekToPreviousMediaItem()
    }

    private fun changed() { onChanged?.invoke() }
    private fun mutate(work: (APIClient) -> Unit) = request(work) { refresh() }

    private fun <T> request(work: (APIClient) -> T, completed: (T) -> Unit) {
        val client = api
        if (client == null) {
            error = "Подключись к серверу с личным ключом."
            changed()
            return
        }
        val started = epoch
        requests.execute {
            val result = runCatching { work(client) }
            main.post {
                if (started != epoch) return@post
                error = null
                result.onSuccess(completed).onFailure { error = it.localizedMessage }
                changed()
            }
        }
    }
}
