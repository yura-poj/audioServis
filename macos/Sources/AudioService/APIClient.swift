import AVFoundation
import Foundation

struct Track: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let title: String
    let sizeBytes: Int64
    let createdAt: String
    let liked: Bool
}

struct Playlist: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let name: String
    let createdAt: String
}

struct Listener: Codable, Sendable {
    let id: String
    let displayName: String
}

enum ClientError: LocalizedError {
    case invalidAddress
    case missingKey
    case invalidResponse
    case unexpectedSize
    case server(String)

    var errorDescription: String? {
        switch self {
        case .invalidAddress: "Укажи HTTPS-адрес сервера или http://localhost для локальной проверки."
        case .missingKey: "Укажи личный ключ."
        case .invalidResponse: "Сервер вернул неожиданный ответ."
        case .unexpectedSize: "Загрузка завершилась раньше, чем был получен весь файл."
        case .server(let message): message
        }
    }
}

struct APIClient: Sendable {
    private struct ListResponse<Item: Decodable>: Decodable {
        let items: [Item]
    }

    private struct ErrorResponse: Decodable {
        struct Detail: Decodable {
            let code: String?
            let message: String
        }
        let error: Detail
    }

    private final class Redirects: NSObject, URLSessionTaskDelegate, Sendable {
        func urlSession(
            _ session: URLSession,
            task: URLSessionTask,
            willPerformHTTPRedirection response: HTTPURLResponse,
            newRequest request: URLRequest,
            completionHandler: @escaping @Sendable (URLRequest?) -> Void
        ) {
            completionHandler(nil)
        }
    }

    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.httpShouldSetCookies = false
        config.urlCache = nil
        config.timeoutIntervalForRequest = 20
        return URLSession(configuration: config, delegate: Redirects(), delegateQueue: nil)
    }()

    let base: URL
    let key: String

    static func address(_ value: String) throws -> URL {
        let raw = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var pieces = URLComponents(string: raw), let host = pieces.host?.lowercased(),
              !host.isEmpty, pieces.user == nil, pieces.password == nil,
              pieces.fragment == nil, pieces.query == nil, ["", "/"].contains(pieces.path),
              let scheme = pieces.scheme?.lowercased(),
              scheme == "https" || (scheme == "http" && host == "localhost") else {
            throw ClientError.invalidAddress
        }
        pieces.scheme = scheme
        pieces.host = host
        pieces.path = ""
        if (scheme == "https" && pieces.port == 443) || (scheme == "http" && pieces.port == 80) {
            pieces.port = nil
        }
        guard let normalized = pieces.url else { throw ClientError.invalidAddress }
        return normalized
    }

    func listener() async throws -> Listener {
        try await decode("me")
    }

    func playlists() async throws -> [Playlist] {
        let reply: ListResponse<Playlist> = try await decode("playlists")
        return reply.items
    }

    func tracks(playlist: String? = nil) async throws -> [Track] {
        let path = playlist.map { "playlists/\($0)/tracks" } ?? "tracks"
        let reply: ListResponse<Track> = try await decode(path)
        return reply.items
    }

    func savePlaylist(_ name: String, id: String?) async throws -> Playlist {
        try await decode(id.map { "playlists/\($0)" } ?? "playlists",
                         method: id == nil ? "POST" : "PATCH", json: ["name": name])
    }

    func renameTrack(_ id: String, title: String) async throws -> Track {
        try await decode("tracks/\(id)", method: "PATCH", json: ["title": title])
    }

    func deleteTrack(_ id: String) async throws {
        _ = try await send("tracks/\(id)", method: "DELETE")
    }

    func deletePlaylist(_ id: String) async throws {
        _ = try await send("playlists/\(id)", method: "DELETE")
    }

    func membership(_ id: String, playlist: String, add: Bool) async throws {
        _ = try await send("playlists/\(playlist)/tracks/\(id)", method: add ? "PUT" : "DELETE")
    }

    func like(_ id: String, liked: Bool) async throws -> Track {
        try await decode("tracks/\(id)/like", method: liked ? "PUT" : "DELETE")
    }

    func nextWave(current: String?, excluded: Set<String>) async throws -> Track? {
        var request = authorized(endpoint("wave/next"), method: "POST")
        request.httpBody = try JSONSerialization.data(withJSONObject: [
            "current_track_id": current as Any, "excluded_track_ids": Array(excluded)
        ])
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let (body, reply) = try await Self.session.data(for: request)
        try check(reply, body: body)
        if (reply as? HTTPURLResponse)?.statusCode == 204 { return nil }
        return try decoder().decode(Track.self, from: body)
    }

    func sendPlay(_ id: String, snapshot: ListeningSnapshot) async throws {
        var request = authorized(endpoint("history/\(id)"), method: "PUT")
        request.httpBody = try JSONEncoder().encode(snapshot)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let (body, reply) = try await Self.session.data(for: request)
        if (reply as? HTTPURLResponse)?.statusCode == 404,
           (try? decoder().decode(ErrorResponse.self, from: body))?.error.code == "track_not_found" { return }
        try check(reply, body: body)
    }

    func upload(_ file: URL, title: String, playlist: String?) async throws -> Track {
        var pieces = URLComponents(url: endpoint("tracks"), resolvingAgainstBaseURL: false)!
        let values = [URLQueryItem(name: "title", value: title)] +
            (playlist.map { [URLQueryItem(name: "playlist_id", value: $0)] } ?? [])
        pieces.queryItems = values
        // Go's query parser interprets a bare plus as a space.
        pieces.percentEncodedQuery = pieces.percentEncodedQuery?.replacingOccurrences(of: "+", with: "%2B")
        guard let url = pieces.url else { throw ClientError.invalidAddress }
        var request = authorized(url, method: "POST")
        request.setValue("audio/mpeg", forHTTPHeaderField: "Content-Type")
        let (body, reply) = try await Self.session.upload(for: request, fromFile: file)
        try check(reply, body: body)
        return try decoder().decode(Track.self, from: body)
    }

    func download(_ track: Track) async throws -> URL {
        let request = authorized(endpoint("media/\(track.id)"), method: "GET")
        let (file, reply) = try await Self.session.download(for: request)
        do {
            try check(reply, body: nil)
            let size = try file.resourceValues(forKeys: [.fileSizeKey]).fileSize
            guard (reply as? HTTPURLResponse)?.statusCode == 200, Int64(size ?? -1) == track.sizeBytes else {
                throw ClientError.unexpectedSize
            }
            return file
        } catch {
            try? FileManager.default.removeItem(at: file)
            throw error
        }
    }

    @MainActor
    func asset(_ track: Track) -> AVURLAsset {
        let url = endpoint("media/\(track.id)")
        let secure = base.scheme == "https" ? "; Secure" : ""
        let header = "audioservice_media=\(key); Path=/api/v1/media/; HttpOnly\(secure)"
        let cookies = HTTPCookie.cookies(withResponseHeaderFields: ["Set-Cookie": header], for: url)
        return AVURLAsset(url: url, options: [AVURLAssetHTTPCookiesKey: cookies])
    }

    private func endpoint(_ path: String) -> URL {
        base.appending(path: "api/v1/\(path)")
    }

    private func authorized(_ url: URL, method: String) -> URLRequest {
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        return request
    }

    private func decode<Output: Decodable>(
        _ path: String,
        method: String = "GET",
        json: [String: String]? = nil
    ) async throws -> Output {
        try await decoder().decode(Output.self, from: send(path, method: method, json: json))
    }

    private func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return decoder
    }

    private func send(_ path: String, method: String, json: [String: String]? = nil) async throws -> Data {
        var request = authorized(endpoint(path), method: method)
        if let json {
            request.httpBody = try JSONEncoder().encode(json)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (body, reply) = try await Self.session.data(for: request)
        try check(reply, body: body)
        return body
    }

    private func check(_ reply: URLResponse, body: Data?) throws {
        guard let http = reply as? HTTPURLResponse else { throw ClientError.invalidResponse }
        guard !(200..<300).contains(http.statusCode) else { return }
        if http.statusCode == 401 {
            throw ClientError.server("Сервер отклонил личный ключ.")
        }
        let detail = body.flatMap { try? decoder().decode(ErrorResponse.self, from: $0) }
        throw ClientError.server(detail?.error.message ?? "Запрос завершился ошибкой HTTP \(http.statusCode).")
    }
}
