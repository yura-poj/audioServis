import SwiftUI
import UniformTypeIdentifiers

struct LibraryView: View {
    @ObservedObject var model: LibraryModel
    @ObservedObject var offline: OfflineLibrary
    @ObservedObject var playback: Playback
    @State private var connectionOpen = false
    @State private var settingsOpen = false
    @State private var importing = false
    @State private var uploadPlaylist: String?
    @State private var editing: EditTarget?
    @State private var deleting: DeleteTarget?

    var body: some View {
        Group {
            if let profile = model.profile {
                connected(profile)
            } else {
                ConnectView(initialAddress: "") { address, key in
                    await model.connect(address: address, key: key)
                } message: {
                    model.error
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(MusicTheme.canvas)
            }
        }
        .onChange(of: model.selection) { _, _ in
            Task { await model.refreshSelection() }
        }
        .fileImporter(isPresented: $importing, allowedContentTypes: [.mp3]) { outcome in
            if case .success(let url) = outcome {
                let playlist = uploadPlaylist
                Task { await model.upload(url, playlist: playlist) }
            }
        }
        .sheet(isPresented: $connectionOpen) {
            ConnectView(initialAddress: model.profile?.base.absoluteString ?? "") { address, key in
                let connected = await model.connect(address: address, key: key)
                if connected { connectionOpen = false }
                return connected
            } message: {
                model.error
            }
            .frame(width: 480)
            .background(MusicTheme.panel)
            .overlay(alignment: .topTrailing) {
                Button("Закрыть") { connectionOpen = false }.padding()
            }
        }
        .sheet(item: $editing) { target in
            EditView(title: target.label, value: target.value) { name in
                editing = nil
                Task {
                    switch target {
                    case .playlist(let playlist): await model.savePlaylist(name, id: playlist?.id)
                    case .track(let track): await model.rename(track, title: name)
                    }
                }
            }
        }
        .sheet(isPresented: $settingsOpen) {
            if let profile = model.profile {
                AccountSettingsView(profile: profile)
                    .id(profile.scope)
            }
        }
        .alert(item: $deleting) { target in
            Alert(
                title: Text(target.label),
                primaryButton: .destructive(Text("Удалить")) {
                    Task {
                        switch target {
                        case .playlist(let playlist): await model.deletePlaylist(playlist)
                        case .track(let track): await model.deleteTrack(track)
                        case .download(let track): offline.remove(track)
                        }
                    }
                },
                secondaryButton: .cancel(Text("Отмена"))
            )
        }
    }

    private func connected(_ profile: Profile) -> some View {
        NavigationSplitView {
            ScrollView {
                VStack(alignment: .leading, spacing: 4) {
                    sidebarItem("Медиатека", icon: "music.note.list", section: .library)
                    sidebarItem("Скачанные", icon: "arrow.down.circle", section: .downloads)
                    Text("Плейлисты")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(MusicTheme.muted)
                        .padding(.top, 16)
                        .padding(.horizontal, 10)
                        .accessibilityAddTraits(.isHeader)
                    ForEach(model.playlists) { playlist in
                        sidebarItem(playlist.name, icon: "music.note", section: .playlist(playlist.id))
                            .contextMenu {
                                Button("Переименовать") { editing = .playlist(playlist) }
                                Button("Удалить", role: .destructive) { deleting = .playlist(playlist) }
                            }
                    }
                }
                .padding(10)
            }
            .navigationSplitViewColumnWidth(min: 180, ideal: 220, max: 280)
            .background(MusicTheme.panel)
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: 0) {
                    Divider()
                    Menu {
                        Button("Настройки…") { settingsOpen = true }
                        Button("Сменить аккаунт…") { connectionOpen = true }
                        Button("Выйти", role: .destructive) { model.logout() }
                    } label: {
                        Label(profile.displayName, systemImage: "person.crop.circle")
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .menuStyle(.borderlessButton)
                    .tint(MusicTheme.muted)
                    .foregroundStyle(MusicTheme.text)
                    .padding(12)
                }
                .background(MusicTheme.panel)
            }
        } detail: {
            VStack(spacing: 0) {
                header
                if let message = (selection == .downloads ? nil : model.error) ?? offline.error ?? playback.error {
                    Label(message, systemImage: "exclamationmark.circle")
                        .font(.callout)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 24)
                        .padding(.bottom, 8)
                }
                Divider()
                if visibleTracks.isEmpty {
                    ContentUnavailableView(
                        selection == .downloads ? "Скачанных треков пока нет" : "Треков пока нет",
                        systemImage: selection == .downloads ? "arrow.down.circle" : "music.note"
                    )
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    List(visibleTracks) { track in
                        row(track)
                            .listRowBackground(MusicTheme.canvas)
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }
                Divider()
                PlayerBar(playback: playback)
            }
            .background(MusicTheme.canvas)
            .toolbarBackground(MusicTheme.panel, for: .windowToolbar)
            .toolbar {
                ToolbarItemGroup {
                    Button { Task { await model.refresh() } } label: {
                        Label("Обновить", systemImage: "arrow.clockwise")
                    }
                    Button { editing = .playlist(nil) } label: {
                        Label("Создать плейлист", systemImage: "folder.badge.plus")
                    }
                }
            }
        }
    }

    private var selection: LibrarySection { model.selection ?? .library }

    private func sidebarItem(_ name: String, icon: String, section: LibrarySection) -> some View {
        let selected = selection == section
        return Button {
            model.selection = section
        } label: {
            Label(name, systemImage: icon)
                .foregroundStyle(selected ? MusicTheme.accent : MusicTheme.text)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .background(selected ? MusicTheme.raised : .clear, in: RoundedRectangle(cornerRadius: 7))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private var selectedPlaylist: Playlist? {
        guard case .playlist(let id) = selection else { return nil }
        return model.playlists.first { $0.id == id }
    }

    private var visibleTracks: [Track] {
        switch selection {
        case .library: model.tracks
        case .playlist: model.playlistTracks
        case .downloads: offline.tracks
        }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 14) {
            Text(selectedPlaylist?.name ?? (selection == .downloads ? "Скачанные" : "Медиатека"))
                .font(.largeTitle.weight(.semibold))
                .lineLimit(1)
                .accessibilityAddTraits(.isHeader)
            Spacer()
            Button {
                if selection == .library {
                    Task { await model.shuffle() }
                } else {
                    model.start(visibleTracks, localOnly: selection == .downloads)
                }
            } label: {
                Label(selection == .library ? "Перемешать всё" : "Воспроизвести",
                      systemImage: selection == .library ? "shuffle" : "play.fill")
            }
            .buttonStyle(MusicActionStyle())
            .disabled(visibleTracks.isEmpty)
            if selection != .downloads {
                Button {
                    uploadPlaylist = selectedPlaylist?.id
                    importing = true
                } label: {
                    Label("Загрузить MP3", systemImage: "square.and.arrow.up")
                }
                .controlSize(.large)
            }
        }
        .padding(24)
    }

    private func row(_ track: Track) -> some View {
        HStack(spacing: 12) {
            Button {
                model.start(visibleTracks, from: track.id, localOnly: selection == .downloads)
            } label: {
                HStack(spacing: 12) {
                    Image(systemName: playback.current?.id == track.id ? "waveform" : "play.circle")
                        .font(.title3)
                        .foregroundStyle(playback.current?.id == track.id ? MusicTheme.accent : MusicTheme.muted)
                        .frame(width: 22)
                    Text(track.title)
                        .lineLimit(1)
                        .foregroundStyle(playback.current?.id == track.id ? MusicTheme.accent : MusicTheme.text)
                    Spacer()
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Воспроизвести \(track.title)")
            if offline.downloading.contains(track.id) {
                ProgressView().controlSize(.small)
                    .accessibilityLabel("Скачивание")
            } else if offline.file(track) != nil {
                Image(systemName: "arrow.down.circle.fill").foregroundStyle(MusicTheme.muted)
                    .accessibilityLabel("Скачан")
            }
            if selection != .downloads {
                Button {
                    Task { await model.toggleLike(track) }
                } label: {
                    Image(systemName: track.liked ? "heart.fill" : "heart")
                        .foregroundStyle(track.liked ? MusicTheme.accent : MusicTheme.muted)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(track.liked ? "Убрать лайк" : "Поставить лайк")
                .help(track.liked ? "Убрать лайк" : "Поставить лайк")
            }
            Menu {
                if selection == .downloads {
                    Button("Удалить с устройства", role: .destructive) { deleting = .download(track) }
                } else {
                    Button("Переименовать") { editing = .track(track) }
                    if !model.playlists.isEmpty {
                        Menu("Добавить в плейлист") {
                            ForEach(model.playlists) { playlist in
                                Button(playlist.name) {
                                    Task { await model.setMembership(track, playlist: playlist.id, add: true) }
                                }
                            }
                        }
                    }
                    if let playlist = selectedPlaylist {
                        Button("Убрать из плейлиста") {
                            Task { await model.setMembership(track, playlist: playlist.id, add: false) }
                        }
                    }
                    Button("Удалить из медиатеки", role: .destructive) { deleting = .track(track) }
                }
            } label: {
                Image(systemName: "ellipsis")
                    .frame(width: 20)
                    .accessibilityLabel("Действия с треком")
            }
            .menuStyle(.borderlessButton)
            .tint(MusicTheme.muted)
            .fixedSize()
        }
        .padding(.vertical, 5)
    }
}

private struct AccountSettingsView: View {
    @Environment(\.dismiss) private var dismiss
    @AppStorage private var downloadLikedTracks: Bool

    init(profile: Profile) {
        _downloadLikedTracks = AppStorage(wrappedValue: true, profile.downloadLikedTracksKey)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Настройки").font(.title2.bold())
            Toggle("Скачивать понравившиеся треки на это устройство", isOn: $downloadLikedTracks)
                .toggleStyle(.switch)
            HStack {
                Spacer()
                Button("Готово") { dismiss() }.keyboardShortcut(.defaultAction)
            }
        }
        .padding(24)
        .frame(width: 420)
        .background(MusicTheme.panel)
    }
}

private struct PlayerBar: View {
    @ObservedObject var playback: Playback

    var body: some View {
        VStack(spacing: 4) {
            controls
            if playback.current != nil {
                TimelineView(.periodic(from: .now, by: 0.5)) { _ in
                    let head = playback.playhead
                    TrackSlider(head: head) { position in
                        if let head { playback.seek(to: position, in: head.item) }
                    }
                    .id(head?.item)
                }
            }
        }
        .padding(.horizontal, 22)
        .padding(.vertical, 8)
        .frame(minHeight: 58)
        .background(MusicTheme.panel)
    }

    private var controls: some View {
        HStack {
            Image(systemName: "waveform")
                .font(.title2)
                .foregroundStyle(playback.current == nil ? MusicTheme.muted : MusicTheme.accent)
                .frame(width: 32)
            Text(playback.current?.title ?? "Воспроизведение остановлено")
                .fontWeight(.medium)
                .lineLimit(1)
            Spacer()
            Button { playback.previous() } label: { Image(systemName: "backward.end.fill") }
                .accessibilityLabel("Предыдущий трек")
            Button { playback.toggle() } label: {
                Image(systemName: playback.isPlaying ? "pause.fill" : "play.fill").frame(width: 22)
            }
            .buttonStyle(MusicActionStyle())
            .accessibilityLabel(playback.isPlaying ? "Пауза" : "Воспроизвести")
            Button { playback.next() } label: { Image(systemName: "forward.end.fill") }
                .accessibilityLabel("Следующий трек")
        }
        .buttonStyle(.borderless)
        .tint(MusicTheme.text)
        .disabled(playback.current == nil)
    }
}

private struct TrackSlider: View {
    let head: Playback.Playhead?
    let seek: (Double) -> Void
    @State private var draft: Double?

    var body: some View {
        let position = draft ?? head?.position ?? 0
        HStack(spacing: 10) {
            Text(time(position))
            Slider(value: Binding(get: { position }, set: { value in
                if draft != nil { draft = value } else { seek(value) }
            }), in: 0...(head?.duration ?? 1), onEditingChanged: { editing in
                if editing {
                    draft = head?.position
                } else if let target = draft {
                    draft = nil
                    seek(target)
                }
            })
            .tint(MusicTheme.accent)
            .disabled(head == nil)
            .accessibilityLabel("Позиция трека")
            .accessibilityValue(time(position))
            Text(head.map { time($0.duration) } ?? "–:––")
        }
        .font(.caption.monospacedDigit())
        .foregroundStyle(MusicTheme.muted)
    }

    private func time(_ seconds: Double) -> String {
        let elapsed = Duration.seconds(seconds.rounded(.down))
        return seconds >= 3600 ? elapsed.formatted(.time(pattern: .hourMinuteSecond)) : elapsed.formatted(.time(pattern: .minuteSecond))
    }
}

private struct ConnectView: View {
    @State var address: String
    @State private var key = ""
    @State private var busy = false
    let connect: (String, String) async -> Bool
    let message: () -> String?

    init(initialAddress: String, connect: @escaping (String, String) async -> Bool, message: @escaping () -> String?) {
        _address = State(initialValue: initialAddress)
        self.connect = connect
        self.message = message
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Image(systemName: "waveform").font(.largeTitle).foregroundStyle(MusicTheme.accent)
            Text("Аудиосервис").font(.largeTitle.weight(.semibold))
            TextField("Адрес сервера", text: $address, prompt: Text("https://music.home:8080"))
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("server-address")
            SecureField("Личный ключ", text: $key)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("personal-key")
            if let message = message() {
                Text(message).font(.callout).foregroundStyle(.secondary)
            }
            Button(busy ? "Подключение…" : "Подключиться") {
                busy = true
                Task {
                    _ = await connect(address, key)
                    busy = false
                }
            }
            .buttonStyle(MusicActionStyle())
            .keyboardShortcut(.defaultAction)
            .disabled(busy || address.isEmpty || key.isEmpty)
        }
        .frame(width: 340)
        .padding(48)
    }
}

private enum EditTarget: Identifiable {
    case playlist(Playlist?)
    case track(Track)

    var id: String {
        switch self {
        case .playlist(let item): "playlist-\(item?.id ?? "new")"
        case .track(let track): "track-\(track.id)"
        }
    }

    var label: String {
        switch self {
        case .playlist(nil): "Новый плейлист"
        case .playlist: "Название плейлиста"
        case .track: "Название трека"
        }
    }

    var value: String {
        switch self {
        case .playlist(let item): item?.name ?? ""
        case .track(let item): item.title
        }
    }
}

private enum DeleteTarget: Identifiable {
    case playlist(Playlist)
    case track(Track)
    case download(Track)

    var id: String { label }

    var label: String {
        switch self {
        case .playlist(let item): "Удалить плейлист «\(item.name)»?"
        case .track(let item): "Удалить «\(item.title)» из медиатеки?"
        case .download(let item): "Удалить «\(item.title)» с устройства?"
        }
    }
}

private struct EditView: View {
    @Environment(\.dismiss) private var dismiss
    let title: String
    @State var value: String
    let save: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(title).font(.headline)
            TextField("Название", text: $value)
                .textFieldStyle(.roundedBorder)
            HStack {
                Spacer()
                Button("Отмена") { dismiss() }
                Button("Сохранить") { save(value) }
                    .buttonStyle(MusicActionStyle())
                    .keyboardShortcut(.defaultAction)
                    .disabled(value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .frame(width: 320)
        .padding(22)
        .background(MusicTheme.panel)
    }
}
