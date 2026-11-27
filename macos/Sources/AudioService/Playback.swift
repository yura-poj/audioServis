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
        let makeAsset: () -> AVAsset
    }

    let history = ListeningHistory()
    private var queueID = UUID()
    private var startReason = "queue"
    private var observedItem: AVPlayerItem?
    private var itemFinished = false
    private var awaitingEnd: [ObjectIdentifier: (item: AVPlayerItem, finish: (String) -> Void)] = [:]
    private var terminalObservers = Set<AnyCancellable>()
    private let player = AVQueuePlayer()
    private var entries: [Entry] = []
    private let queueLimit = 2
    private var nextIndex = 0
    private var indices: [ObjectIdentifier: Int] = [:]
    private var currentIndex = 0
    private var currentObservation: NSKeyValueObservation?
    private var transportObservation: NSKeyValueObservation?
    private var itemObservation: NSKeyValueObservation?

    struct Playhead {
        let item: AVPlayerItem
        let position: Double
        let duration: Double
    }

    var playhead: Playhead? {
        guard let item = player.currentItem, item.status == .readyToPlay else { return nil }
        let duration = item.duration.seconds
        let position = item.currentTime().seconds
        guard duration.isFinite, duration > 0, position.isFinite, !item.seekableTimeRanges.isEmpty else { return nil }
        return Playhead(item: item, position: min(max(position, 0), duration), duration: duration)
    }

    func seek(to seconds: Double, in item: AVPlayerItem) {
        guard let head = playhead, head.item === item, seconds.isFinite else { return }
        let target = CMTime(seconds: min(max(seconds, 0), head.duration), preferredTimescale: 600)
        item.seek(to: target, toleranceBefore: .zero, toleranceAfter: .zero, completionHandler: nil)
    }

    init() {
        for (name, outcome) in [(AVPlayerItem.didPlayToEndTimeNotification, "completed"),
                                (AVPlayerItem.failedToPlayToEndTimeNotification, "error")] {
            NotificationCenter.default.publisher(for: name)
                .receive(on: DispatchQueue.main)
                .sink { [weak self] notice in
                    guard let item = notice.object as? AVPlayerItem else { return }
                    self?.finishItem(item, outcome: outcome)
                }
                .store(in: &terminalObservers)
        }
        currentObservation = player.observe(\.currentItem, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in self?.readCurrentItem() }
        }
        transportObservation = player.observe(\.timeControlStatus, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in
                guard let self else { return }
                self.updatePlaybackState()
            }
        }
    }

    private var waveClient: APIClient?
    private var waveGeneration = UUID()
    private var waveExcluded = Set<String>()
    private var waveRequest: Task<Void, Never>?
    private var preparedFor: AVPlayerItem?

    private func endWave() {
        waveGeneration = UUID()
        waveRequest?.cancel()
        waveRequest = nil
        waveExcluded.removeAll()
        preparedFor = nil
        waveClient = nil
    }

    func startWave(_ api: APIClient) {
        guard waveClient == nil else { return }
        waveClient = api
        updateWave()
    }

    private func updateWave() {
        guard let api = waveClient, waveRequest == nil else { return }
        let item = player.currentItem
        if let item, item === preparedFor { return }
        let generation = waveGeneration
        let anchor = item.flatMap { indices[ObjectIdentifier($0)] }.map { entries[$0].track.id }
        if let anchor { waveExcluded.insert(anchor) }
        let excluded = waveExcluded
        waveRequest = Task { @MainActor in
            do {
                let next = try await api.nextWave(current: anchor, excluded: excluded)
                guard generation == waveGeneration else { return }
                waveRequest = nil
                if player.currentItem != nil && player.currentItem !== item { updateWave(); return }
                // Preserve the currently playing item and replace only its continuation.
                let live = player.currentItem
                for queued in player.items() where queued !== live { player.remove(queued) }
                if let live, let index = indices[ObjectIdentifier(live)] {
                    entries = Array(entries.prefix(index + 1))
                } else {
                    entries.removeAll()
                }
                nextIndex = entries.count
                guard let next else { endWave(); return }
                waveExcluded.insert(next.id)
                preparedFor = live
                entries.append(Entry(track: next, makeAsset: { api.asset(next) }))
                if live == nil {
                    if anchor == nil { queueID = UUID(); startReason = "selection" }
                    else { startReason = "automatic" }
                    fillQueue()
                    player.play()
                } else { fillQueue() }
            } catch {
                guard generation == waveGeneration else { return }
                endWave()
                self.error = error.localizedDescription
            }
        }
    }

    func play(_ tracks: [Track], from startID: String? = nil, offline: OfflineLibrary, api: APIClient?, reason: String = "queue") {
        endWave()
        manualTransition("skipped")
        queueID = UUID()
        startReason = reason
        entries = tracks.compactMap { track in
            if let local = offline.file(track) {
                return Entry(track: track, makeAsset: { AVURLAsset(url: local) })
            }
            return api.map { client in Entry(track: track, makeAsset: { client.asset(track) }) }
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
        if waveClient != nil { return }
        guard !entries.isEmpty else { return }
        manualTransition("skipped")
        startReason = "skip"
        rebuild(from: max(0, currentIndex - 1))
    }

    func next() {
        guard current != nil else { return }
        manualTransition("skipped")
        startReason = "skip"
        player.advanceToNextItem()
        readCurrentItem()
    }

    func stop() {
        endWave()
        manualTransition("stopped")
        for old in awaitingEnd.values { old.finish("interrupted") }
        awaitingEnd.removeAll()
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
        nextIndex = start
        fillQueue()
        readCurrentItem()
        player.play()
    }

    private func fillQueue() {
        var queued = player.items()
        let liveIDs = Set(queued.map(ObjectIdentifier.init))
        indices = indices.filter { liveIDs.contains($0.key) }
        while queued.count < queueLimit && nextIndex < entries.count {
            let item = AVPlayerItem(asset: entries[nextIndex].makeAsset())
            indices[ObjectIdentifier(item)] = nextIndex
            nextIndex += 1
            player.insert(item, after: queued.last)
            queued.append(item)
        }
    }

    private func readCurrentItem() {
        observeTransition()
        itemObservation = nil
        guard let item = player.currentItem, let index = indices[ObjectIdentifier(item)], entries.indices.contains(index) else {
            current = nil
            return
        }
        fillQueue()
        currentIndex = index
        current = entries[index].track
        updateWave()
        error = nil
        updatePlaybackState()
        itemObservation = item.observe(\.status, options: [.initial, .new]) { [weak self] _, _ in
            Task { @MainActor in
                guard let self, self.player.currentItem === item else { return }
                if item.status == .failed {
                    self.finishItem(item, outcome: "error")
                    self.error = item.error?.localizedDescription ?? "Не удалось воспроизвести трек."
                } else {
                    self.updatePlaybackState()
                }
            }
        }
    }

    private func manualTransition(_ outcome: String) {
        observeTransition()
        history.finish(outcome)
        observedItem = nil
        itemFinished = false
    }

    private func observeTransition() {
        let next = player.currentItem
        guard observedItem !== next else { return }
        if let old = observedItem {
            if let finish = history.takeCurrent() {
                awaitingEnd[ObjectIdentifier(old)] = (old, finish)
            }
            startReason = "automatic"
        }
        observedItem = next
        itemFinished = false
    }

    private func finishItem(_ item: AVPlayerItem, outcome: String) {
        if let old = awaitingEnd.removeValue(forKey: ObjectIdentifier(item)) {
            old.finish(outcome)
        } else if item === observedItem {
            history.finish(outcome)
            itemFinished = true
        }
    }

    private func updatePlaybackState() {
        observeTransition()
        isPlaying = player.timeControlStatus != .paused
        guard !itemFinished, player.timeControlStatus == .playing, let item = player.currentItem,
              item.status == .readyToPlay, let index = indices[ObjectIdentifier(item)], entries.indices.contains(index) else {
            history.pause()
            return
        }
        let seconds = item.duration.seconds
        let duration = seconds.isFinite && seconds > 0 && seconds < Double(Int64.max / 1000) ? Int64(seconds * 1000) : nil
        history.playing(entries[index].track.id, queueID: queueID, reason: startReason, duration: duration)
    }
}
