import SwiftUI

enum LibrarySection: Hashable {
    case library
    case playlist(String)
    case downloads
}

@main
struct AudioServiceApp: App {
    @StateObject private var model = LibraryModel()
    @StateObject private var offline = OfflineLibrary()
    @StateObject private var playback = Playback()

    var body: some Scene {
        WindowGroup("Аудиосервис") {
            LibraryView(model: model, offline: offline, playback: playback)
                .frame(minWidth: 780, minHeight: 480)
                .preferredColorScheme(.dark)
                .tint(MusicTheme.accent)
                .task {
                    model.attach(offline: offline, playback: playback)
                    await model.refresh()
                }
        }
        .defaultSize(width: 980, height: 640)
    }
}

@MainActor
final class LibraryModel: ObservableObject {
    @Published private(set) var profile = AccountStore.active()
    @Published private(set) var tracks: [Track] = []
    @Published private(set) var playlists: [Playlist] = []
    @Published private(set) var playlistTracks: [Track] = []
    @Published var selection: LibrarySection? = .library
    @Published private(set) var error: String?

    private var selectedPlaylistID: String?
    private weak var offline: OfflineLibrary?
    private weak var playback: Playback?
    private var session = UUID()
    private var catalogRequest = UUID()
    private var playlistRequest = UUID()
    private var loadedKey: String?

    func attach(offline: OfflineLibrary, playback: Playback) {
        guard self.offline == nil else { return }
        self.offline = offline
        self.playback = playback
        offline.select(profile)
    }

    func connect(address: String, key: String) async -> Bool {
        error = nil
        do {
            let base = try APIClient.address(address)
            let secret = key.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !secret.isEmpty else { throw ClientError.missingKey }
            let api = APIClient(base: base, key: secret)
            let listener = try await api.listener()
            let profile = Profile(base: base, id: listener.id, displayName: listener.displayName)
            try AccountStore.setActive(profile, key: secret)
            replaceProfile(profile, key: secret)
            await refresh()
            return true
        } catch {
            self.error = error.localizedDescription
            return false
        }
    }

    func logout() {
        AccountStore.clear(profile)
        replaceProfile(nil, key: nil)
    }

    func refresh() async {
        guard profile != nil else { return }
        let request = UUID()
        catalogRequest = request
        await run { api, session in
            async let catalog = api.tracks()
            async let lists = api.playlists()
            let (tracks, playlists) = try await (catalog, lists)
            guard self.session == session, self.catalogRequest == request else { return }
            self.tracks = tracks
            self.playlists = playlists
            offline?.reconcile(tracks)
            if case .playlist(let id) = selection, !playlists.contains(where: { $0.id == id }) {
                selection = .library
            }
            await refreshSelection()
        }
    }

    func refreshSelection() async {
        let request = UUID()
        playlistRequest = request
        guard case .playlist(let id) = selection else {
            selectedPlaylistID = nil
            playlistTracks = []
            return
        }
        if id != selectedPlaylistID {
            selectedPlaylistID = id
            playlistTracks = []
        }
        await run { api, session in
            let items = try await api.tracks(playlist: id)
            guard self.session == session, self.playlistRequest == request, selection == .playlist(id) else { return }
            playlistTracks = items
        }
    }

    func savePlaylist(_ name: String, id: String?) async {
        await mutate { _ = try await $0.savePlaylist(name, id: id) }
    }

    func rename(_ track: Track, title: String) async {
        await mutate { _ = try await $0.renameTrack(track.id, title: title) }
    }

    func upload(_ url: URL, playlist: String?) async {
        let access = url.startAccessingSecurityScopedResource()
        defer { if access { url.stopAccessingSecurityScopedResource() } }
        let title = url.deletingPathExtension().lastPathComponent
        await mutate { _ = try await $0.upload(url, title: title, playlist: playlist) }
    }

    func setMembership(_ track: Track, playlist: String, add: Bool) async {
        await mutate { try await $0.membership(track.id, playlist: playlist, add: add) }
    }

    func deletePlaylist(_ playlist: Playlist) async {
        await mutate { try await $0.deletePlaylist(playlist.id) }
    }

    func deleteTrack(_ track: Track) async {
        await run { api, session in
            try await api.deleteTrack(track.id)
            guard self.session == session else { return }
            offline?.remove(track)
            await refresh()
        }
    }

    func toggleLike(_ track: Track) async {
        guard let profile else { return }
        let downloadOnLike = profile.downloadLikedTracks
        await run { api, session in
            let updated = try await api.like(track.id, liked: !track.liked)
            guard self.session == session else { return }
            if let index = tracks.firstIndex(where: { $0.id == track.id }) { tracks[index] = updated }
            if let index = playlistTracks.firstIndex(where: { $0.id == track.id }) { playlistTracks[index] = updated }
            if updated.liked && downloadOnLike && profile.downloadLikedTracks {
                offline?.enqueue(updated, api: api)
            }
        }
    }

    func start(_ list: [Track], from id: String? = nil, localOnly: Bool = false) {
        guard let offline, let playback, !list.isEmpty else { return }
        let api = localOnly ? nil : try? client()
        playback.play(list, from: id, offline: offline, api: api)
    }

    func shuffle() async {
        await run { api, session in
            let catalog = try await api.tracks()
            guard self.session == session else { return }
            tracks = catalog
            offline?.reconcile(catalog)
            start(catalog.shuffled())
        }
    }

    private func replaceProfile(_ profile: Profile?, key: String?) {
        session = UUID()
        playback?.stop()
        offline?.select(profile)
        tracks = []
        playlists = []
        playlistTracks = []
        selectedPlaylistID = nil
        selection = .library
        loadedKey = key
        self.profile = profile
    }

    private func client() throws -> APIClient {
        guard let profile else { throw ClientError.missingKey }
        if loadedKey == nil { loadedKey = AccountStore.key(profile) }
        guard let loadedKey else { throw ClientError.missingKey }
        return APIClient(base: profile.base, key: loadedKey)
    }

    private func run(_ operation: (APIClient, UUID) async throws -> Void) async {
        let session = self.session
        do {
            let api = try client()
            error = nil
            try await operation(api, session)
        } catch {
            guard self.session == session, !Task.isCancelled else { return }
            self.error = error.localizedDescription
        }
    }

    private func mutate(_ operation: (APIClient) async throws -> Void) async {
        await run { api, session in
            try await operation(api)
            guard self.session == session else { return }
            await refresh()
        }
    }
}
