import Foundation
import GRDB
import MiloAPI
import SwiftProtobuf

/// Applies a Milo card's changes to the local store in one transaction. Milo never
/// writes to the server; the phone applies cards and syncs them like any other edit.
nonisolated struct ChangeApplier {
    let db: AppDatabase
    var now: @Sendable () -> Date = { Date() }

    enum ApplyError: Error, Equatable {
        /// A row the card was based on changed after Milo looked.
        case stale
    }

    /// How to put things back.
    struct UndoToken: Sendable {
        var previous: [any SyncedRecord]
        var created: [any SyncedRecord]
    }

    /// Server times have microseconds; ours have milliseconds.
    private let tolerance: TimeInterval = 0.002

    func apply(_ changes: [Milo_Change], base: [Milo_RowVersion]) throws -> UndoToken {
        let stamp = now()
        return try db.writer.write { db in
            for version in base {
                guard let key = UUID(protoID: version.id) else { continue }
                guard let current = try Synced.stored(db, table: version.table, key: key), !current.deleted,
                      current.updatedAt.timeIntervalSince(version.updatedAt.date) <= tolerance
                else { throw ApplyError.stale }
            }
            var token = UndoToken(previous: [], created: [])
            for change in changes {
                guard let record = Proto.record(change) else { continue }
                if let existing = try Synced.stored(db, of: record), !existing.deleted {
                    token.previous.append(existing)
                } else {
                    token.created.append(record)
                }
                try record.stamped(stamp, dirty: true).save(db)
            }
            return token
        }
    }

    func undo(_ token: UndoToken) throws {
        let stamp = now()
        try db.writer.write { db in
            for record in token.previous {
                try record.stamped(stamp, dirty: true).save(db)
            }
            for record in token.created {
                // Even if it never synced, the tombstone goes up; the server accepts it.
                try record.tombstone(at: stamp).save(db)
            }
        }
    }
}
