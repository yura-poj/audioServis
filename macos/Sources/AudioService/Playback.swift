import AVFoundation
import Combine
import Foundation

@MainActor
final class Playback: ObservableObject {
    @Published private(set) var current: Track?
    @Published private(set) var isPlaying = false
    @Published private(set) var error: String?

    private struct Entry {
        let track: Track
        let asset: AVAsset
    }

    private let player = AVQueuePlayer()
    private var entries: [Entry] = []
    private var indices: [ObjectIdentifier: Int] = [:]
    private var currentIndex = 0
    private var currentObservation: NSKeyValueObservation?
    private var transportObservation: NSKeyValueObservation?
    private var itemObservation: NSKeyValueObservation?

    init() {
        currentObservation = player.observe(\.currentItem, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in self?.readCurrentItem() }
        }
        transportObservation = player.observe(\.timeControlStatus, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in
                guard let self else { return }
                self.isPlaying = self.player.timeControlStatus != .paused
            }
        }
    }

    func play(_ tracks: [Track], from startID: String? = nil, offline: OfflineLibrary, api: APIClient?) {
        entries = tracks.compactMap { track in
            if let local = offline.file(track) {
                return Entry(track: track, asset: AVURLAsset(url: local))
            }
            return api.map { Entry(track: track, asset: $0.asset(track)) }
        }
        guard !entries.isEmpty else {
            stop()
            return
        }
        let start = startID.flatMap { id in entries.firstIndex { $0.track.id == id } } ?? 0
        rebuild(from: start)
    }

    func toggle() {
        guard current != nil else { return }
        if isPlaying {
            player.pause()
        } else {
            player.play()
        }
    }

    func previous() {
        guard !entries.isEmpty else { return }
        rebuild(from: max(0, currentIndex - 1))
    }

    func next() {
        guard current != nil else { return }
        player.advanceToNextItem()
        readCurrentItem()
    }

    func stop() {
        player.pause()
        player.removeAllItems()
        entries.removeAll()
        indices.removeAll()
        itemObservation = nil
        current = nil
        isPlaying = false
        error = nil
    }

    private func rebuild(from start: Int) {
        player.pause()
        player.removeAllItems()
        indices.removeAll()
        error = nil
        var previous: AVPlayerItem?
        for index in start..<entries.count {
            let item = AVPlayerItem(asset: entries[index].asset)
            player.insert(item, after: previous)
            indices[ObjectIdentifier(item)] = index
            previous = item
        }
        readCurrentItem()
        player.play()
    }

    private func readCurrentItem() {
        itemObservation = nil
        guard let item = player.currentItem, let index = indices[ObjectIdentifier(item)], entries.indices.contains(index) else {
            current = nil
            return
        }
        currentIndex = index
        current = entries[index].track
        error = nil
        itemObservation = item.observe(\.status, options: [.initial, .new]) { [weak self] _, _ in
            Task { @MainActor in
                guard let self, let item = self.player.currentItem, item.status == .failed else { return }
                self.error = item.error?.localizedDescription ?? "Не удалось воспроизвести трек."
            }
        }
    }
}
