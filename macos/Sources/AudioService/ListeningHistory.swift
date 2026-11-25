import Foundation
import OSLog

struct ListeningSnapshot: Codable, Equatable, Sendable {
    let trackID: String
    let queueID: String
    let sequence: Int
    let previousPlayID: String?
    let startedAt: String
    var listenedMS: Int64
    var durationMS: Int64?
    let startReason: String
    var outcome: String

    enum CodingKeys: String, CodingKey {
        case trackID = "track_id", queueID = "queue_id", sequence, previousPlayID = "previous_play_id"
        case startedAt = "started_at", listenedMS = "listened_ms", durationMS = "duration_ms"
        case startReason = "start_reason", outcome
    }
}

@MainActor
final class ListeningHistory {
    private let log = Logger(subsystem: "local.audioservice.macos", category: "ListeningHistory")
    private var folder: URL?
    private var api: APIClient?
    private var pending: [String: ListeningSnapshot] = [:]
    private var active: (id: String, snapshot: ListeningSnapshot)?
    private var since: UInt64?
    private var previous: String?
    private var queue: UUID?
    private var sequence = 0
    private var generation = UUID()
    private var timer: Task<Void, Never>?
    private var sending = false

    func select(_ profile: Profile?, api: APIClient?) {
        finish("stopped")
        timer?.cancel()
        generation = UUID()
        pending.removeAll()
        previous = nil
        queue = nil
        self.api = api
        folder = profile.map { URL.applicationSupportDirectory.appending(path: "AudioService/History/\($0.scope)", directoryHint: .isDirectory) }
        guard let folder else { return }
        let files = (try? FileManager.default.contentsOfDirectory(at: folder, includingPropertiesForKeys: nil)) ?? []
        for file in files where file.pathExtension == "json" {
            do {
                var snapshot = try JSONDecoder().decode(ListeningSnapshot.self, from: Data(contentsOf: file))
                let id = file.deletingPathExtension().lastPathComponent
                if snapshot.outcome == "in_progress" {
                    snapshot.outcome = "interrupted"
                    save(id, snapshot)
                } else {
                    pending[id] = snapshot
                }
            } catch { log.error("Could not read listening history: \(error.localizedDescription)") }
        }
        flush()
        timer = Task { [weak self] in
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(5)) } catch { return }
                guard let self else { return }
                if since != nil { checkpoint() }
                flush()
            }
        }
    }

    func playing(_ track: String, queueID: UUID, reason: String, duration: Int64?) {
        guard folder != nil else { return }
        if active == nil {
            if queue != queueID {
                queue = queueID
                sequence = 0
            }
            sequence += 1
            let id = UUID().uuidString.lowercased()
            let started = Date.now.formatted(Date.ISO8601FormatStyle(includingFractionalSeconds: true))
            let snapshot = ListeningSnapshot(trackID: track, queueID: queueID.uuidString.lowercased(), sequence: sequence,
                previousPlayID: previous, startedAt: started, listenedMS: 0, durationMS: duration, startReason: reason, outcome: "in_progress")
            active = (id, snapshot)
            previous = id
            save(id, snapshot)
            flush()
        } else if active?.snapshot.durationMS == nil {
            active?.snapshot.durationMS = duration
        }
        if since == nil { since = DispatchTime.now().uptimeNanoseconds }
    }

    func pause() {
        if since != nil { checkpoint() }
        since = nil
    }

    func finish(_ outcome: String) {
        takeCurrent()?(outcome)
    }

    // AVQueuePlayer can change currentItem before it delivers the old item's end notification.
    func takeCurrent() -> ((String) -> Void)? {
        guard active != nil else { return nil }
        accumulate()
        guard let last = active else { return nil }
        active = nil
        since = nil
        let owner = generation
        return { [weak self] outcome in
            guard let self, generation == owner else { return }
            var snapshot = last.snapshot
            snapshot.outcome = outcome
            save(last.id, snapshot)
            flush()
        }
    }

    private func accumulate() {
        guard let since else { return }
        let now = DispatchTime.now().uptimeNanoseconds
        active?.snapshot.listenedMS += Int64((now - since) / 1_000_000)
        self.since = now
    }

    private func checkpoint() {
        accumulate()
        if let active { save(active.id, active.snapshot) }
    }

    private func save(_ id: String, _ snapshot: ListeningSnapshot) {
        guard let folder else { return }
        pending[id] = snapshot
        do {
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            try JSONEncoder().encode(snapshot).write(to: folder.appending(path: id + ".json"), options: .atomic)
        } catch { log.error("Could not persist listening history: \(error.localizedDescription)") }
    }

    private func flush() {
        guard let api, let folder, !sending, !pending.isEmpty else { return }
        sending = true
        let batch = pending
        let owner = generation
        Task {
            defer { sending = false }
            for (id, snapshot) in batch {
                guard generation == owner else { return }
                do { try await api.sendPlay(id, snapshot: snapshot) } catch { return }
                guard generation == owner, pending[id] == snapshot else { continue }
                if snapshot.outcome != "in_progress" {
                    do { try FileManager.default.removeItem(at: folder.appending(path: id + ".json")) }
                    catch { log.error("Could not remove delivered history: \(error.localizedDescription)") }
                }
                pending.removeValue(forKey: id)
            }
        }
    }
}
