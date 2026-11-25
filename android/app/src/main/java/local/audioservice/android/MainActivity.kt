package local.audioservice.android

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.content.res.ColorStateList
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.InsetDrawable
import android.graphics.drawable.RippleDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.OpenableColumns
import android.text.InputType
import android.text.TextUtils
import android.text.format.DateUtils
import android.text.method.PasswordTransformationMethod
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.widget.Button
import android.widget.EditText
import android.widget.ImageButton
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.PopupMenu
import android.widget.ScrollView
import android.widget.SeekBar
import android.widget.TextView
import kotlin.math.roundToInt
import kotlin.math.roundToLong

class MainActivity : Activity() {
    private enum class Tab(val label: String) {
        LIBRARY("Медиатека"), PLAYLISTS("Плейлисты"), DOWNLOADS("Скачанные")
    }

    private data class LoginForm(val submit: Button, val message: TextView)
    private enum class ButtonTone { NORMAL, PRIMARY, SELECTED, QUIET }

    private val model by lazy { (application as AudioServiceApp).library }
    private var tab = Tab.LIBRARY
    private var scroll: ScrollView? = null
    private var loginForm: LoginForm? = null
    private var screenKey = ""
    private var timeline: TrackSlider? = null
    private var renderPending = false
    private val clock = Handler(Looper.getMainLooper())
    private val updatePosition = object : Runnable {
        override fun run() {
            timeline?.update()
            clock.postDelayed(this, 500)
        }
    }
    private var uploadPlaylist: String? = null
    private val ink get() = getColor(R.color.music_text)
    private val muted get() = getColor(R.color.music_muted)
    private val accent get() = getColor(R.color.music_accent)
    private val canvas get() = getColor(R.color.music_canvas)
    private val panel get() = getColor(R.color.music_panel)
    private val raised get() = getColor(R.color.music_raised)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        tab = savedInstanceState?.getString("tab")?.let { Tab.valueOf(it) } ?: Tab.LIBRARY
        uploadPlaylist = savedInstanceState?.getString("uploadPlaylist")
        model.onChanged = ::render
        render()
        model.preparePlayback()
        model.refresh()
    }

    override fun onDestroy() {
        model.onChanged = null
        super.onDestroy()
    }

    override fun onStart() {
        super.onStart()
        if (renderPending) render()
        clock.post(updatePosition)
    }

    override fun onStop() {
        clock.removeCallbacks(updatePosition)
        timeline?.dragging = null
        super.onStop()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        outState.putString("tab", tab.name)
        outState.putString("uploadPlaylist", uploadPlaylist)
        super.onSaveInstanceState(outState)
    }

    private fun render() {
        val profile = model.profile
        val nextKey = "${profile?.id}:$tab:${model.playlist?.id}"
        val drag = timeline?.dragging
        if (profile != null && drag != null && drag == model.playhead?.entry && screenKey == nextKey) {
            renderPending = true
            return
        }
        val restoreSliderFocus = timeline?.hasFocus() == true && screenKey == nextKey
        renderPending = false
        timeline = null
        if (profile == null) {
            showLogin()
            return
        }
        loginForm = null
        val scrollY = if (screenKey == nextKey) scroll?.scrollY ?: 0 else 0
        screenKey = nextKey
        val page = root()
        renderAccountStrip(page, profile)
        renderTabs(page)
        line(page)
        renderHeading(page)
        val issue = (if (tab == Tab.DOWNLOADS) null else model.error) ?: model.offline.error
        if (issue != null) page.addView(label(issue, 13, muted).apply { setPadding(dp(16), 0, dp(16), dp(8)) })

        val list = vertical().apply { setPadding(dp(16), 0, dp(16), dp(8)) }
        if (tab == Tab.PLAYLISTS && model.playlist == null) {
            if (model.playlists.isEmpty()) emptyState(list, "Плейлистов пока нет")
            model.playlists.forEach { playlistRow(list, it) }
        } else {
            val tracks = visibleTracks()
            if (tracks.isEmpty()) emptyState(list, if (tab == Tab.DOWNLOADS) "Скачанных треков пока нет" else "Треков пока нет")
            tracks.forEach { trackRow(list, it, tracks) }
        }
        val scroller = ScrollView(this).apply {
            isFillViewport = true
            addView(list)
        }
        scroll = scroller
        page.addView(scroller, LinearLayout.LayoutParams(-1, 0, 1f))
        line(page)
        renderPlayer(page)
        setContentView(page)
        if (restoreSliderFocus) timeline?.requestFocus()
        if (scrollY > 0) scroller.post { scroller.scrollTo(0, scrollY) }
    }

    private fun showLogin() {
        if (loginForm == null) {
            screenKey = ""
            val page = root().apply {
                gravity = Gravity.CENTER_VERTICAL
                setPadding(dp(24), dp(20), dp(24), dp(20))
            }
            page.addView(label("Аудиосервис", 34).apply {
                setTypeface(typeface, Typeface.BOLD)
                setPadding(0, 0, 0, dp(20))
            })
            val address = input("Адрес сервера", "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
            val key = input("Личный ключ", "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)
            val message = label("", 14, muted)
            page.addView(address)
            page.addView(key)
            page.addView(message)
            val submit = control("Подключиться", tone = ButtonTone.PRIMARY) {
                model.connect(address.text.toString(), key.text.toString()) { success ->
                    if (success) (getSystemService(INPUT_METHOD_SERVICE) as android.view.inputmethod.InputMethodManager)
                        .hideSoftInputFromWindow(key.windowToken, 0)
                }
            }
            page.addView(submit)
            loginForm = LoginForm(submit, message)
            setContentView(page)
        }
        loginForm?.submit?.isEnabled = !model.connecting
        loginForm?.message?.text = model.error ?: if (model.connecting) "Подключение…" else ""
        loginForm?.message?.visibility = if (model.error != null || model.connecting) View.VISIBLE else View.GONE
    }

    private fun renderAccountStrip(page: LinearLayout, profile: Profile) {
        val strip = horizontal().apply {
            setPadding(dp(14), 0, dp(10), 0)
            setBackgroundColor(panel)
        }
        strip.addView(label("Аудиосервис", 18).apply { setTypeface(typeface, Typeface.BOLD) }, weighted())
        strip.addView(control("↻", "Обновить") { model.refresh() })
        val account = control(profile.displayName) {}
        account.setOnClickListener {
            menu(account, listOf(
                "Настройки" to { showSettings() },
                "Сменить аккаунт…" to { showConnectDialog(profile.base) },
                "Выйти" to {
                    tab = Tab.LIBRARY
                    model.logout()
                }
            ))
        }
        strip.addView(account)
        page.addView(strip)
    }

    private fun renderTabs(page: LinearLayout) {
        val tabs = horizontal().apply {
            setPadding(dp(8), 0, dp(8), dp(5))
            setBackgroundColor(panel)
        }
        for (item in Tab.entries) {
            val tone = if (item == tab) ButtonTone.SELECTED else ButtonTone.QUIET
            val button = control(item.label, tone = tone) {
                tab = item
                if (item != Tab.PLAYLISTS) model.selectPlaylist(null) else render()
            }
            button.isSelected = tab == item
            tabs.addView(button, weighted())
        }
        page.addView(tabs)
    }

    private fun renderHeading(page: LinearLayout) {
        val section = vertical().apply { setPadding(dp(16), dp(16), dp(16), dp(8)) }
        val playlist = if (tab == Tab.PLAYLISTS) model.playlist else null
        val heading = horizontal()
        heading.addView(label(playlist?.name ?: tab.label, 27).apply {
            setTypeface(typeface, Typeface.BOLD)
            maxLines = 2
            ellipsize = TextUtils.TruncateAt.END
        }, weighted())
        if (playlist != null) heading.addView(control("Назад", "К плейлистам") { model.selectPlaylist(null) })
        section.addView(heading)

        val actions = horizontal()
        when {
            tab == Tab.LIBRARY -> {
                val shuffle = control("Перемешать всё", tone = ButtonTone.PRIMARY) { model.shuffle() }
                shuffle.isEnabled = model.tracks.isNotEmpty()
                actions.addView(shuffle)
                actions.addView(control("Загрузить MP3") { chooseFile(null) })
            }
            tab == Tab.PLAYLISTS && playlist == null ->
                actions.addView(control("Создать плейлист", tone = ButtonTone.PRIMARY) {
                    edit("Новый плейлист", "") { model.savePlaylist(it, null) }
                })
            playlist != null -> {
                val play = control("Воспроизвести", tone = ButtonTone.PRIMARY) { model.play(model.playlistTracks) }
                play.isEnabled = model.playlistTracks.isNotEmpty()
                actions.addView(play)
                actions.addView(control("Загрузить MP3") { chooseFile(playlist.id) })
            }
            tab == Tab.DOWNLOADS && model.offline.tracks.isNotEmpty() ->
                actions.addView(control("Воспроизвести", tone = ButtonTone.PRIMARY) {
                    model.play(model.offline.tracks, localOnly = true)
                })
        }
        if (actions.childCount != 0) section.addView(actions)
        page.addView(section)
    }

    private fun playlistRow(list: LinearLayout, playlist: Playlist) {
        val row = horizontal()
        val title = label(playlist.name, 16).apply {
            gravity = Gravity.CENTER_VERTICAL
            minHeight = dp(52)
            setOnClickListener { model.selectPlaylist(playlist) }
        }
        row.addView(title, weighted())
        val more = control("⋯", "Действия с плейлистом", ButtonTone.QUIET) {}
        more.setOnClickListener {
            menu(more, listOf(
                "Переименовать" to { edit("Название плейлиста", playlist.name) { model.savePlaylist(it, playlist.id) } },
                "Удалить" to { confirm("Удалить плейлист «${playlist.name}»?") { model.deletePlaylist(playlist) } }
            ))
        }
        row.addView(more)
        list.addView(row)
        line(list)
    }

    private fun trackRow(list: LinearLayout, track: Track, tracks: List<Track>) {
        val row = horizontal()
        val title = label(track.title, 16, if (track.id == model.currentTrackId) accent else ink).apply {
            minHeight = dp(52)
            gravity = Gravity.CENTER_VERTICAL
            maxLines = 2
            ellipsize = TextUtils.TruncateAt.END
            contentDescription = "Воспроизвести ${track.title}"
            setOnClickListener { model.play(tracks, track.id, localOnly = tab == Tab.DOWNLOADS) }
        }
        row.addView(title, weighted())
        if (model.offline.downloading(track)) {
            row.addView(label("↓", 16, muted).apply { contentDescription = "Скачивание" })
        } else if (model.offline.file(track) != null) {
            row.addView(label("↓", 16, muted).apply { contentDescription = "Скачан" })
        }
        if (tab != Tab.DOWNLOADS) {
            val label = if (track.liked) "Убрать лайк" else "Поставить лайк"
            val heart = ImageButton(this, null, android.R.attr.borderlessButtonStyle).apply {
                setImageResource(R.drawable.ic_heart)
                scaleType = ImageView.ScaleType.CENTER
                isSelected = track.liked
                imageTintList = ColorStateList.valueOf(if (track.liked) accent else muted)
                contentDescription = "$label · ${track.title}"
                setPadding(0, 0, 0, 0)
                setOnClickListener { model.toggleLike(track) }
            }
            row.addView(heart, LinearLayout.LayoutParams(dp(48), dp(48)))
        }
        val more = control("⋯", "Действия с треком", ButtonTone.QUIET) {}
        more.setOnClickListener {
            if (tab == Tab.DOWNLOADS) {
                menu(more, listOf("Удалить с устройства" to {
                    confirm("Удалить «${track.title}» с устройства?") { model.offline.remove(track) }
                }))
            } else {
                val actions = mutableListOf<Pair<String, () -> Unit>>()
                actions += "Переименовать" to { edit("Название трека", track.title) { model.renameTrack(track, it) } }
                if (model.playlists.isNotEmpty()) actions += "Добавить в плейлист" to { selectDestination(track) }
                if (tab == Tab.PLAYLISTS) model.playlist?.let { playlist ->
                    actions += "Убрать из плейлиста" to { model.membership(track, playlist.id, false) }
                }
                actions += "Удалить из медиатеки" to {
                    confirm("Удалить «${track.title}» из медиатеки?") { model.deleteTrack(track) }
                }
                menu(more, actions)
            }
        }
        row.addView(more)
        list.addView(row)
        line(list)
    }

    private fun selectDestination(track: Track) {
        val playlists = model.playlists
        AlertDialog.Builder(this)
            .setTitle("Добавить в плейлист")
            .setItems(playlists.map { it.name }.toTypedArray()) { _, index ->
                model.membership(track, playlists[index].id, true)
            }
            .setNegativeButton("Отмена", null)
            .show()
    }

    private fun renderPlayer(page: LinearLayout) {
        val bar = horizontal().apply {
            setPadding(dp(16), dp(4), dp(8), dp(4))
            setBackgroundColor(panel)
        }
        val title = model.currentTitle
        bar.addView(label(title ?: "Воспроизведение остановлено", 14, if (title == null) muted else ink).apply {
            maxLines = 2
            ellipsize = TextUtils.TruncateAt.END
        }, weighted())
        val controls = horizontal()
        val playIcon = if (model.activePlayback) "Ⅱ" else "▶"
        val playLabel = if (model.activePlayback) "Пауза" else "Воспроизвести"
        controls.addView(control("⏮", "Предыдущий трек", ButtonTone.QUIET) { model.previous() })
        controls.addView(control(playIcon, playLabel, ButtonTone.PRIMARY) {
            model.togglePlayback()
        })
        controls.addView(control("⏭", "Следующий трек", ButtonTone.QUIET) { model.next() })
        for (index in 0 until controls.childCount) controls.getChildAt(index).isEnabled = title != null
        bar.addView(controls)
        page.addView(bar)
        if (title != null) {
            timeline = TrackSlider().also { page.addView(it) }
        }
    }

    private inner class TrackSlider : LinearLayout(this@MainActivity), SeekBar.OnSeekBarChangeListener {
        var dragging: LibraryState.QueueEntry? = null
        private var shown: LibraryState.Playhead? = null
        private val elapsed = label("0:00", 12, muted)
        private val total = label("–:––", 12, muted)
        private val slider = object : SeekBar(this@MainActivity) {
            override fun dispatchTouchEvent(event: MotionEvent): Boolean {
                if (event.actionMasked == MotionEvent.ACTION_CANCEL) dragging = null
                return super.dispatchTouchEvent(event)
            }
        }

        init {
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(16), 0, dp(16), dp(4))
            setBackgroundColor(panel)
            slider.max = 10000
            slider.contentDescription = "Позиция трека"
            slider.progressTintList = enabledColors(accent, muted)
            slider.thumbTintList = enabledColors(accent, muted)
            addView(elapsed)
            addView(slider, LinearLayout.LayoutParams(0, dp(48), 1f))
            addView(total)
            slider.setOnSeekBarChangeListener(this)
            update()
        }

        fun update() {
            if (dragging != null) return
            val head = model.playhead
            shown = head
            slider.isEnabled = head != null
            slider.progress = head?.let { (it.position.toDouble() / it.duration * slider.max).roundToInt() } ?: 0
            elapsed.text = time(head?.position ?: 0)
            total.text = head?.let { time(it.duration) } ?: "–:––"
        }

        override fun onStartTrackingTouch(bar: SeekBar) {
            dragging = shown?.entry
        }

        override fun onProgressChanged(bar: SeekBar, progress: Int, fromUser: Boolean) {
            val head = shown ?: return
            if (!fromUser || timeline !== this) return
            val target = targetTime(head, progress)
            elapsed.text = time(target)
            if (dragging == null) model.seek(head.entry, target)
        }

        override fun onStopTrackingTouch(bar: SeekBar) {
            val entry = dragging
            dragging = null
            val head = shown
            if (timeline !== this) return
            if (head != null && entry == head.entry) model.seek(entry, targetTime(head, bar.progress))
            update()
            if (renderPending) post { if (renderPending) render() }
        }

        private fun targetTime(head: LibraryState.Playhead, progress: Int) =
            (progress.toDouble() / slider.max * head.duration).roundToLong()

        private fun time(milliseconds: Long) = DateUtils.formatElapsedTime(milliseconds / 1000)
    }

    private fun showSettings() {
        val options = arrayOf("Скачивать понравившиеся треки на это устройство")
        AlertDialog.Builder(this).setTitle("Настройки")
            .setMultiChoiceItems(options, booleanArrayOf(model.downloadLikedTracks)) { _, _, enabled ->
                model.downloadLikedTracks = enabled
            }
            .setPositiveButton("Готово", null)
            .show()
    }

    private fun showConnectDialog(initialAddress: String) {
        val body = vertical().apply { setPadding(dp(20), dp(8), dp(20), 0) }
        val address = input("Адрес сервера", initialAddress, InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI)
        val key = input("Личный ключ", "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)
        val message = label("", 14, muted)
        body.addView(address)
        body.addView(key)
        body.addView(message)
        val dialog = AlertDialog.Builder(this).setTitle("Сменить аккаунт").setView(body)
            .setPositiveButton("Подключиться", null).setNegativeButton("Отмена", null).create()
        dialog.setOnShowListener {
            val submit = dialog.getButton(AlertDialog.BUTTON_POSITIVE)
            submit.setOnClickListener {
                submit.isEnabled = false
                model.connect(address.text.toString(), key.text.toString()) { success ->
                    if (success) {
                        tab = Tab.LIBRARY
                        dialog.dismiss()
                    } else {
                        submit.isEnabled = true
                        message.text = model.error
                    }
                }
            }
        }
        dialog.show()
    }

    private fun edit(title: String, current: String, save: (String) -> Unit) {
        val field = input("Название", current, InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_SENTENCES)
        val body = vertical().apply {
            setPadding(dp(20), dp(8), dp(20), 0)
            addView(field)
        }
        val dialog = AlertDialog.Builder(this).setTitle(title).setView(body)
            .setNegativeButton("Отмена", null).setPositiveButton("Сохранить", null).create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                val name = field.text.toString().trim()
                if (name.isEmpty()) field.error = "Введи название"
                else {
                    save(name)
                    dialog.dismiss()
                }
            }
        }
        dialog.show()
        field.requestFocus()
    }

    private fun confirm(title: String, action: () -> Unit) {
        AlertDialog.Builder(this).setTitle(title).setNegativeButton("Отмена", null)
            .setPositiveButton("Удалить") { _, _ -> action() }.show()
    }

    private fun menu(anchor: View, actions: List<Pair<String, () -> Unit>>) {
        PopupMenu(this, anchor).apply {
            actions.forEachIndexed { index, action -> menu.add(0, index, index, action.first) }
            setOnMenuItemClickListener { actions[it.itemId].second(); true }
            show()
        }
    }

    @Suppress("DEPRECATION")
    private fun chooseFile(playlist: String?) {
        uploadPlaylist = playlist
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE)
            .setType("audio/mpeg")
        startActivityForResult(intent, PICK_MP3)
    }

    @Deprecated("Result from the platform document picker")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != PICK_MP3 || resultCode != RESULT_OK) return
        val uri = data?.data ?: return
        val name = filename(uri).removeSuffix(".mp3").removeSuffix(".MP3")
        model.upload(name, uploadPlaylist) { contentResolver.openInputStream(uri) }
    }

    private fun filename(uri: Uri): String =
        contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use {
            if (it.moveToFirst()) it.getString(0) else null
        } ?: "Трек MP3"

    private fun visibleTracks(): List<Track> = when (tab) {
        Tab.LIBRARY -> model.tracks
        Tab.PLAYLISTS -> model.playlistTracks
        Tab.DOWNLOADS -> model.offline.tracks
    }

    private fun emptyState(list: LinearLayout, text: String) {
        list.addView(label(text, 16, muted).apply { setPadding(0, dp(30), 0, 0) })
    }

    private fun root(): LinearLayout {
        val root = vertical().apply { setBackgroundColor(canvas) }
        root.setOnApplyWindowInsetsListener { view, insets ->
            val left = view.paddingLeft
            val right = view.paddingRight
            if (Build.VERSION.SDK_INT >= 30) {
                val bars = insets.getInsets(WindowInsets.Type.systemBars() or WindowInsets.Type.ime())
                view.setPadding(left, bars.top, right, bars.bottom)
            }
            insets
        }
        return root
    }

    private fun vertical() = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
    private fun horizontal() = LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
    }

    private fun line(group: LinearLayout) {
        group.addView(View(this).apply { setBackgroundColor(getColor(R.color.music_divider)) }, LinearLayout.LayoutParams(-1, dp(1)))
    }

    private fun weighted() = LinearLayout.LayoutParams(0, -2, 1f)

    private fun label(value: String, size: Int, color: Int = ink) = TextView(this).apply {
        text = value
        textSize = size.toFloat()
        setTextColor(color)
    }

    private fun input(prompt: String, value: String, format: Int) = EditText(this).apply {
        hint = prompt
        setTextColor(ink)
        setHintTextColor(muted)
        contentDescription = prompt
        isSingleLine = true
        inputType = format
        if ((format and InputType.TYPE_MASK_VARIATION) == InputType.TYPE_TEXT_VARIATION_PASSWORD) {
            transformationMethod = PasswordTransformationMethod.getInstance()
        }
        setText(value)
    }

    private fun control(value: String, description: String = value, tone: ButtonTone = ButtonTone.NORMAL, action: () -> Unit) = Button(this).apply {
        val fill = when (tone) {
            ButtonTone.PRIMARY -> accent
            ButtonTone.NORMAL, ButtonTone.SELECTED -> raised
            ButtonTone.QUIET -> Color.TRANSPARENT
        }
        val foreground = when (tone) {
            ButtonTone.PRIMARY -> canvas
            ButtonTone.SELECTED -> accent
            ButtonTone.NORMAL -> ink
            ButtonTone.QUIET -> muted
        }
        val shape = GradientDrawable().apply {
            cornerRadius = dp(24).toFloat()
            color = enabledColors(fill, if (tone == ButtonTone.PRIMARY) raised else fill)
        }
        val ripple = if (tone == ButtonTone.PRIMARY) 0x33000000 else 0x33FFFFFF
        val mask = GradientDrawable().apply {
            cornerRadius = shape.cornerRadius
            setColor(Color.WHITE)
        }
        backgroundTintList = null
        background = InsetDrawable(RippleDrawable(ColorStateList.valueOf(ripple), shape, mask), dp(2), dp(5), dp(2), dp(5))
        stateListAnimator = null
        setTextColor(enabledColors(foreground, muted))
        text = value
        contentDescription = description
        isAllCaps = false
        minWidth = dp(40)
        minimumWidth = dp(40)
        setPadding(dp(12), 0, dp(12), 0)
        setOnClickListener { action() }
    }

    private fun enabledColors(enabled: Int, disabled: Int) = ColorStateList(
        arrayOf(intArrayOf(-android.R.attr.state_enabled), intArrayOf()), intArrayOf(disabled, enabled)
    )

    private fun dp(size: Int): Int = (resources.displayMetrics.density * size).toInt()

    private companion object { const val PICK_MP3 = 10 }
}
