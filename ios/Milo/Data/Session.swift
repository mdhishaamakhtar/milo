import Foundation
import Observation
import Security

/// Who is signed in. The token lives in the Keychain; the log lives in the database
/// and survives being signed out, so nothing logged offline is lost.
@Observable
final class Session {
    struct User: Equatable {
        var id: String
        var username: String
    }

    private(set) var user: User?
    /// Set when the server stops accepting the token; the sign-in screen says so.
    private(set) var expired = false

    private let db: AppDatabase
    private let keychain: Keychain

    init(db: AppDatabase, keychain: Keychain = Keychain(service: "dev.hishaam.milo")) {
        self.db = db
        self.keychain = keychain
        if keychain.token != nil, let id = try? db.meta(.userId), let name = try? db.meta(.username) {
            user = User(id: id, username: name)
        }
    }

    var token: String? { keychain.token }
    var isSignedIn: Bool { user != nil && token != nil }

    /// Signs in as `user`. A different account than last time starts from an empty store.
    func signedIn(token: String, user: User) throws {
        if let previous = try db.meta(.userId), previous != user.id {
            try db.eraseAll()
        }
        keychain.token = token
        try db.setMeta(.userId, user.id)
        try db.setMeta(.username, user.username)
        expired = false
        self.user = user
    }

    /// The server rejected the token. Local data, including unsynced work, stays.
    func tokenExpired() {
        keychain.token = nil
        expired = true
        user = nil
    }

    /// Signs out and keeps local data for the same account to pick up again.
    func signOut() {
        keychain.token = nil
        expired = false
        user = nil
    }

    /// After deleting the account: nothing of it stays on the phone.
    func forget() throws {
        keychain.token = nil
        try db.eraseAll()
        user = nil
    }
}

/// The bearer token in the Keychain.
nonisolated struct Keychain: Sendable {
    let service: String

    var token: String? {
        get {
            var query = baseQuery
            query[kSecReturnData as String] = true
            query[kSecMatchLimit as String] = kSecMatchLimitOne
            var item: CFTypeRef?
            guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
            return String(data: data, encoding: .utf8)
        }
        nonmutating set {
            SecItemDelete(baseQuery as CFDictionary)
            guard let newValue, let data = newValue.data(using: .utf8) else { return }
            var query = baseQuery
            query[kSecValueData as String] = data
            query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlock
            SecItemAdd(query as CFDictionary, nil)
        }
    }

    private var baseQuery: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: "token"]
    }
}
