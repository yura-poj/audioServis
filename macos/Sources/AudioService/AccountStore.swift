import CryptoKit
import Foundation
import Security

struct Profile: Codable, Equatable, Sendable {
    let base: URL
    let id: String
    let displayName: String

    var scope: String {
        let value = Data("\(base.absoluteString)\u{0}\(id)".utf8)
        return SHA256.hash(data: value).map { String(format: "%02x", $0) }.joined()
    }
}

@MainActor
enum AccountStore {
    private static let selection = "active-profile"
    private static let service = "local.audioservice.macos"

    static func active() -> Profile? {
        UserDefaults.standard.data(forKey: selection).flatMap { try? JSONDecoder().decode(Profile.self, from: $0) }
    }

    static func setActive(_ profile: Profile, key: String) throws {
        let query = keyQuery(profile)
        let data = Data(key.utf8)
        var status = SecItemUpdate(query as CFDictionary, [kSecValueData: data] as CFDictionary)
        if status == errSecItemNotFound {
            var entry = query
            entry[kSecValueData as String] = data
            status = SecItemAdd(entry as CFDictionary, nil)
        }
        guard status == errSecSuccess else {
            throw ClientError.server("Не удалось сохранить личный ключ в связке ключей macOS.")
        }
        UserDefaults.standard.set(try JSONEncoder().encode(profile), forKey: selection)
    }

    static func key(_ profile: Profile) -> String? {
        var query = keyQuery(profile)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        guard status == errSecSuccess, let data = result as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func clear(_ profile: Profile?) {
        UserDefaults.standard.removeObject(forKey: selection)
        if let profile {
            SecItemDelete(keyQuery(profile) as CFDictionary)
        }
    }

    private static func keyQuery(_ profile: Profile) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: profile.scope
        ]
    }
}
