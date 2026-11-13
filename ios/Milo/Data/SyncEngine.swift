import Connect
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
    /// Rows the server refused, by the version it refused. They stay dirty here and aren't
    /// sent again until they change or the app next starts, when the server may know better.
    private var refused: [String: Date] = [:]
    /// How many changes are on this phone only because the server wouldn't take them.
    var refusedCount: Int { refused.count }

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
        await sendCardReports()
    }

    // MARK: What became of Milo's cards

    private var reporting = false

    /// Tells the server a card was applied, dismissed or undone, so Milo knows next time.
    /// It is written down first: said offline, it goes up after the next sync that works.
    func report(card id: String, _ status: Milo_CardStatus) {
        var waiting = cardReports
        waiting[id] = status.rawValue
        cardReports = waiting
        Task { await sendCardReports() }
    }

    private var cardReports: [String: Int] {
        get {
            guard let json = try? db.meta(.cardReports), let all = try? JSONDecoder().decode([String: Int].self, from: Data(json.utf8)) else { return [:] }
            return all
        }
        set {
            do {
                try db.setMeta(.cardReports, newValue.isEmpty ? nil : String(decoding: JSONEncoder().encode(newValue), as: UTF8.self))
            } catch {
                log.error("card reports: \(error.localizedDescription)")
            }
        }
    }

    /// One at a time and in order, so "applied" never lands after its "undone".
    private func sendCardReports() async {
        guard session.isSignedIn, !reporting else { return }
        reporting = true
        defer { reporting = false }
        while let (id, status) = cardReports.min(by: { $0.key < $1.key }) {
            var request = Milo_SetCardStatusRequest()
            request.cardID = id
            request.status = Milo_CardStatus(rawValue: status) ?? .unspecified
            do {
                _ = try await api.milo.setCardStatus(request: request, headers: APIClient.headers(session.token)).value()
            } catch let error as APIError where [.notFound, .invalidArgument, .permissionDenied].contains(error.code) {
                // The server will never take this one; asking again won't help.
            } catch {
                return // offline or signed out: it waits for the next sync
            }
            // Unless the card changed again while this was on its way.
            var waiting = cardReports
            if waiting[id] == status {
                waiting[id] = nil
                cardReports = waiting
            }
        }
    }

    private func runOnce() async throws {
        var cursor = Int64(try db.meta(.cursor) ?? "0") ?? 0
        while true {
            let dirty = try await db.writer.read { db in try Synced.dirty(db, limit: 2000) }
            let pushing = dirty.filter { refused[Self.key($0)] != $0.updatedAt }
            var request = Milo_SyncRequest()
            request.cursor = cursor
            request.changes = pushing.map(\.change)
            let response = try await api.sync.sync(request: request, headers: APIClient.headers(session.token)).value()
            var kept = pushing
            // Highest first, so taking one out doesn't move the ones still to go.
            for rejection in response.rejected.sorted(by: { $0.index > $1.index }) where pushing.indices.contains(Int(rejection.index)) {
                let row = kept.remove(at: Int(rejection.index))
                refused[Self.key(row)] = row.updatedAt
                log.error("sync: the server refused a \(type(of: row).syncTable, privacy: .public): \(rejection.reason, privacy: .public)")
            }
            for row in kept { refused[Self.key(row)] = nil }
            try await apply(response, pushed: kept)
            cursor = response.cursor
            // A full batch means more may be waiting, unless none of it could be sent.
            if !response.hasMore_p && (dirty.count < 2000 || pushing.isEmpty) { break }
        }
    }

    private static func key(_ row: any SyncedRecord) -> String {
        "\(type(of: row).syncTable)/\(row.syncKey)"
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
