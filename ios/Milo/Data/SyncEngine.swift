import Foundation
import GRDB
import Network
import MiloAPI
import OSLog
import Observation

/// Push local changes, then pull everything newer than the cursor. Last write
/// wins per row, the same rule the server uses.
@Observable
final class SyncEngine {
    private(set) var isSyncing = false
    private(set) var lastError: String?
    private(set) var lastSynced: Date?

    private let db: AppDatabase
    private let api: APIClient
    private let session: Session
    private var pending: Task<Void, Never>?
    private var again = false

    init(db: AppDatabase, api: APIClient, session: Session) {
        self.db = db
        self.api = api
        self.session = session
    }

    /// Sync about two seconds after the last edit.
    func soon() {
        pending?.cancel()
        pending = Task {
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled else { return }
            await sync()
        }
    }

    /// Sync now. A call while one is running makes it go round once more.
    func sync() async {
        guard session.isSignedIn else { return }
        if isSyncing {
            again = true
            return
        }
        isSyncing = true
        defer { isSyncing = false }
        repeat {
            again = false
            do {
                try await runOnce()
                lastError = nil
                lastSynced = Date()
            } catch let error as APIError where error.isUnauthenticated {
                session.tokenExpired()
                return
            } catch {
                lastError = error.localizedDescription
                log.notice("sync failed: \(error.localizedDescription, privacy: .public)")
                return
            }
        } while again
    }

    private func runOnce() async throws {
        var cursor = Int64(try db.meta(.cursor) ?? "0") ?? 0
        while true {
            let dirty = try await db.writer.read { db in try Synced.dirty(db, limit: 2000) }
            var request = Milo_SyncRequest()
            request.cursor = cursor
            request.changes = dirty.map(\.change)
            let response = try await api.sync.sync(request: request, headers: APIClient.headers(session.token)).value()
            try await apply(response, pushed: dirty)
            cursor = response.cursor
            if !response.hasMore_p && dirty.count < 2000 { break }
        }
    }

    private func apply(_ response: Milo_SyncResponse, pushed: [any SyncedRecord]) async throws {
        try await db.writer.write { db in
            // Pushed rows are clean again, unless they changed while the request was in flight.
            for row in pushed {
                if let current = try Synced.stored(db, of: row), current.updatedAt == row.updatedAt {
                    try current.stamped(current.updatedAt, dirty: false).save(db)
                }
            }
            for change in response.changes {
                guard let incoming = Proto.record(change) else { continue }
                if let local = try Synced.stored(db, of: incoming), local.dirty, local.updatedAt > incoming.updatedAt {
                    continue // our newer edit wins; it goes up on the next push
                }
                try incoming.save(db)
            }
            try Meta(key: AppDatabase.MetaKey.cursor.rawValue, value: String(response.cursor)).save(db)
        }
    }

}

/// Whether the phone has a network path, for Milo's offline notice.
@Observable
final class Connectivity {
    var isOnline = true
    private let monitor = NWPathMonitor()

    /// Unwatched, it says online until told otherwise.
    init(watching: Bool = true) {
        guard watching else { return }
        monitor.pathUpdateHandler = { [weak self] path in
            let online = path.status == .satisfied
            Task { @MainActor in self?.isOnline = online }
        }
        monitor.start(queue: DispatchQueue(label: "dev.hishaam.milo.network"))
    }
}
