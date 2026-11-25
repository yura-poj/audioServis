import Foundation

@MainActor
final class OfflineLibrary: ObservableObject {
    @Published private(set) var tracks: [Track] = []
    @Published private(set) var downloading: Set<String> = []
    @Published private(set) var error: String?

    private struct Job {
        let id: UUID
        let task: Task<Void, Never>
    }

    private var directory: URL?
    private var jobs: [String: Job] = [:]

    func select(_ profile: Profile?) {
        for job in jobs.values { job.task.cancel() }
        jobs.removeAll()
        downloading.removeAll()
        error = nil
        guard let profile else {
            directory = nil
            tracks = []
            return
        }
        let parent = URL.applicationSupportDirectory.appending(path: "AudioService/Downloads", directoryHint: .isDirectory)
        let dir = parent.appending(path: profile.scope, directoryHint: .isDirectory)
        directory = dir
        let data = try? Data(contentsOf: dir.appending(path: "index.json"))
        tracks = (data.flatMap { try? JSONDecoder().decode([Track].self, from: $0) } ?? [])
            .filter { file($0) != nil }
    }

    func file(_ track: Track) -> URL? {
        guard let directory, let id = UUID(uuidString: track.id) else { return nil }
        let url = directory.appending(path: id.uuidString.lowercased() + ".mp3")
        let size = try? url.resourceValues(forKeys: [.fileSizeKey]).fileSize
        return Int64(size ?? -1) == track.sizeBytes ? url : nil
    }

    func enqueue(_ track: Track, api: APIClient) {
        guard let directory, jobs[track.id] == nil,
              !tracks.contains(where: { $0.id == track.id && file($0) != nil }) else { return }
        let id = UUID()
        downloading.insert(track.id)
        error = nil
        let task = Task { [weak self] in
            do {
                let temp = try await api.download(track)
                defer { try? FileManager.default.removeItem(at: temp) }
                guard let self, self.jobs[track.id]?.id == id, !Task.isCancelled else { return }
                try self.publish(temp, track: track, in: directory)
            } catch {
                if let self, self.jobs[track.id]?.id == id, !Task.isCancelled {
                    self.error = "\(track.title) · \(error.localizedDescription)"
                }
            }
            if let self, self.jobs[track.id]?.id == id {
                self.jobs.removeValue(forKey: track.id)
                self.downloading.remove(track.id)
            }
        }
        jobs[track.id] = Job(id: id, task: task)
    }

    func remove(_ track: Track) {
        jobs.removeValue(forKey: track.id)?.task.cancel()
        downloading.remove(track.id)
        guard let directory else { return }
        let updated = tracks.filter { $0.id != track.id }
        do {
            if updated != tracks {
                try persist(updated, in: directory)
            }
            if let saved = file(track) {
                try FileManager.default.removeItem(at: saved)
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    func reconcile(_ catalog: [Track]) {
        guard let directory else { return }
        let byID = Dictionary(uniqueKeysWithValues: catalog.map { ($0.id, $0) })
        for id in Array(jobs.keys) where byID[id] == nil {
            jobs.removeValue(forKey: id)?.task.cancel()
            downloading.remove(id)
        }
        let updated = tracks.compactMap { byID[$0.id] }
        guard updated != tracks else { return }
        let removedFiles = tracks.filter { byID[$0.id] == nil }.compactMap { file($0) }
        do {
            try persist(updated, in: directory)
            for url in removedFiles { try FileManager.default.removeItem(at: url) }
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func publish(_ temp: URL, track: Track, in directory: URL) throws {
        guard let id = UUID(uuidString: track.id) else { throw ClientError.invalidResponse }
        let files = FileManager.default
        try files.createDirectory(at: directory, withIntermediateDirectories: true)
        let stage = directory.appending(path: ".\(UUID().uuidString).part")
        defer { try? files.removeItem(at: stage) }
        try files.moveItem(at: temp, to: stage)
        let destination = directory.appending(path: id.uuidString.lowercased() + ".mp3")
        if files.fileExists(atPath: destination.path) {
            _ = try files.replaceItemAt(destination, withItemAt: stage)
        } else {
            try files.moveItem(at: stage, to: destination)
        }
        do {
            try persist(tracks.filter { $0.id != track.id } + [track], in: directory)
        } catch {
            try? files.removeItem(at: destination)
            throw error
        }
    }

    private func persist(_ index: [Track], in directory: URL) throws {
        try JSONEncoder().encode(index).write(to: directory.appending(path: "index.json"), options: .atomic)
        tracks = index
    }
}
