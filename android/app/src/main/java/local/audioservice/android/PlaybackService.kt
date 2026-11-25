package local.audioservice.android

import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.ResolvingDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.session.MediaSession
import androidx.media3.session.MediaSessionService
import androidx.media3.session.SessionCommands
import java.io.IOException

@OptIn(UnstableApi::class)
class PlaybackService : MediaSessionService() {
    private var session: MediaSession? = null

    override fun onCreate() {
        super.onCreate()
        val http = DefaultHttpDataSource.Factory()
        val authorized = ResolvingDataSource.Factory(http) { request ->
            val client = (application as AudioServiceApp).library.api
            if (client == null || !client.ownsMedia(request.uri)) throw IOException("Missing media authorization")
            request.withAdditionalHeaders(mapOf("Authorization" to "Bearer ${client.key}"))
        }
        val source = DefaultDataSource.Factory(this, authorized)
        val player = ExoPlayer.Builder(this)
            .setMediaSourceFactory(DefaultMediaSourceFactory(source))
            .setAudioAttributes(AudioAttributes.DEFAULT, true)
            .setWakeMode(C.WAKE_MODE_LOCAL)
            .build()
        player.addListener(HistoryListener((application as AudioServiceApp).library))
        session = MediaSession.Builder(this, player).setCallback(Access()).build()
    }

    override fun onGetSession(controllerInfo: MediaSession.ControllerInfo): MediaSession? = session

    override fun onDestroy() {
        (application as AudioServiceApp).library.history.finish("interrupted")
        session?.also {
            it.player.release()
            it.release()
        }
        session = null
        super.onDestroy()
    }

    private class HistoryListener(private val library: LibraryState) : Player.Listener {
        private var reason = "selection"

        override fun onMediaItemTransition(item: MediaItem?, transition: Int) {
            reason = when (transition) {
                Player.MEDIA_ITEM_TRANSITION_REASON_AUTO, Player.MEDIA_ITEM_TRANSITION_REASON_REPEAT -> "automatic"
                Player.MEDIA_ITEM_TRANSITION_REASON_SEEK -> "skip"
                else -> item?.mediaMetadata?.extras?.getString("reason") ?: "selection"
            }
            val outcome = when {
                item == null -> "stopped"
                reason == "automatic" -> "completed"
                else -> "skipped"
            }
            library.history.finish(outcome)
        }

        override fun onPlayerError(error: PlaybackException) { library.history.finish("error") }

        override fun onEvents(player: Player, events: Player.Events) {
            val item = player.currentMediaItem
            val context = item?.mediaMetadata?.extras
            val queue = context?.getString("queue")
            when {
                player.playbackState == Player.STATE_ENDED -> library.history.finish("completed")
                player.playbackState == Player.STATE_IDLE -> library.history.finish("stopped")
                player.isPlaying && item != null && queue != null && context.getString("scope") == library.profile?.scope() ->
                    library.history.playing(item.mediaId, queue, reason, player.duration.takeIf { it > 0 })
                else -> library.history.pause()
            }
        }
    }

    private inner class Access : MediaSession.Callback {
        override fun onConnect(session: MediaSession, controller: MediaSession.ControllerInfo): MediaSession.ConnectionResult {
            if (controller.packageName == packageName && controller.uid == applicationInfo.uid) {
                return super.onConnect(session, controller)
            }
            if (!controller.isTrusted) return MediaSession.ConnectionResult.reject()
            val transport = Player.Commands.Builder()
                .addAll(
                    Player.COMMAND_PLAY_PAUSE,
                    Player.COMMAND_STOP,
                    Player.COMMAND_SEEK_IN_CURRENT_MEDIA_ITEM,
                    Player.COMMAND_SEEK_TO_PREVIOUS,
                    Player.COMMAND_SEEK_TO_PREVIOUS_MEDIA_ITEM,
                    Player.COMMAND_SEEK_TO_NEXT,
                    Player.COMMAND_SEEK_TO_NEXT_MEDIA_ITEM,
                    Player.COMMAND_GET_CURRENT_MEDIA_ITEM,
                    Player.COMMAND_GET_TIMELINE,
                    Player.COMMAND_GET_METADATA
                )
                .build()
            return MediaSession.ConnectionResult.AcceptedResultBuilder(session)
                .setAvailablePlayerCommands(transport)
                .setAvailableSessionCommands(SessionCommands.EMPTY)
                .build()
        }
    }
}
