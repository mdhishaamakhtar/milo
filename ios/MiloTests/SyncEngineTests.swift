import Connect
import Foundation
import GRDB
import MiloAPI
import Testing
@testable import Milo

/// Sync against a fake server: local changes go up, everything newer comes down, and the
/// newest write to a row wins.
@MainActor
struct SyncEngineTests {
    private let start = Date(timeIntervalSince1970: 1_794_300_000)

    private func workout(_ name: String, at: Date) -> Workout {
        Workout(id: UUID(), name: name, notes: "", startedAt: at, endedAt: at.addingTimeInterval(3600), templateId: nil, healthUUID: nil, updatedAt: at, deleted: false, dirty: true)
    }

    private func reply(cursor: Int64, _ changes: [Milo_Change] = [], more: Bool = false) -> Result<Milo_SyncResponse, FakeServer.Failure> {
        var r = Milo_SyncResponse()
        r.cursor = cursor
        r.changes = changes
        r.hasMore_p = more
        return .success(r)
    }

    private func stored(_ app: AppModel, _ id: UUID) throws -> Workout? {
        try app.db.writer.read { try Workout.fetchOne($0, key: id) }
    }

    @Test func pushesWhatChangedThenSavesWhatCameDown() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let mine = workout("Push", at: start)
        try await app.db.writer.write { try mine.save($0) }
        var theirs = workout("Legs", at: start.addingTimeInterval(-86400))
        theirs.dirty = false
        server.replyToSync(reply(cursor: 7, [mine.change, theirs.change]))

        await app.sync.sync()

        let request = try #require(server.syncRequests.first)
        #expect(request.cursor == 0)
        #expect(request.changes.map(\.workout.id) == [Proto.id(mine.id)])
        #expect(try stored(app, mine.id)?.dirty == false)
        #expect(try stored(app, theirs.id)?.name == "Legs")
        #expect(try app.db.meta(.cursor) == "7")
        #expect(app.sync.lastError == nil && app.sync.lastSynced != nil && !app.sync.isSyncing)

        await app.sync.sync()
        #expect(server.syncRequests[1].cursor == 7 && server.syncRequests[1].changes.isEmpty)
    }

    @Test func anEditMadeWhileItsPushIsInFlightIsKept() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = workout("Push", at: start)
        try await app.db.writer.write { try w.save($0) }
        // The server still has the version that was pushed, older than the edit.
        server.replyToSync(reply(cursor: 1, [w.change]))
        let db = app.db
        server.duringNextSync {
            var renamed = w
            renamed.name = "Push day"
            try? db.save(renamed, now: w.updatedAt.addingTimeInterval(5))
        }

        await app.sync.sync()

        let after = try #require(try stored(app, w.id))
        #expect(after.name == "Push day" && after.dirty)
        await app.sync.sync()
        #expect(server.syncRequests[1].changes.map(\.workout.name) == ["Push day"])
    }

    @Test func aNewerWriteFromAnotherPhoneWins() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = workout("Push", at: start)
        try await app.db.writer.write { try w.save($0) }
        var fromIPad = w
        fromIPad.name = "Chest"
        fromIPad.updatedAt = start.addingTimeInterval(60)
        server.replyToSync(reply(cursor: 2, [fromIPad.change]))
        let db = app.db
        server.duringNextSync {
            var renamed = w
            renamed.name = "Push day"
            try? db.save(renamed, now: w.updatedAt.addingTimeInterval(5))
        }

        await app.sync.sync()

        let after = try #require(try stored(app, w.id))
        #expect(after.name == "Chest" && !after.dirty)
    }

    @Test func pullsPageAfterPageUntilTheServerHasNoMore() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        var a = workout("A", at: start), b = workout("B", at: start)
        a.dirty = false; b.dirty = false
        server.replyToSync(reply(cursor: 3, [a.change], more: true))
        server.replyToSync(reply(cursor: 5, [b.change]))

        await app.sync.sync()

        #expect(server.syncRequests.map(\.cursor) == [0, 3])
        #expect(try stored(app, a.id) != nil && stored(app, b.id) != nil)
        #expect(try app.db.meta(.cursor) == "5")
    }

    @Test func aLongBacklogGoesUpTwoThousandRowsAtATime() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        try await app.db.writer.write { db in
            for i in 0..<2001 {
                try BodyMeasurement(id: UUID(), measuredAt: start.addingTimeInterval(Double(i) * 3600), weightKg: 80, bodyFatPct: nil, source: "manual", updatedAt: start, deleted: false, dirty: true).save(db)
            }
        }

        await app.sync.sync()

        #expect(server.syncRequests.map(\.changes.count) == [2000, 1])
        #expect(try await app.db.writer.read { try BodyMeasurement.filter(Column("dirty") == true).fetchCount($0) } == 0)
    }

    @Test func aRowThatCantBeReadIsSkipped() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        var good = workout("Good", at: start)
        good.dirty = false
        var broken = Milo_Change()
        broken.workout.name = "No id"
        server.replyToSync(reply(cursor: 4, [broken, Milo_Change(), good.change]))

        await app.sync.sync()

        #expect(try await app.db.writer.read { try Workout.fetchCount($0) } == 1)
        #expect(try stored(app, good.id) != nil)
        #expect(try app.db.meta(.cursor) == "4")
    }

    @Test func aFailedSyncKeepsChangesForTheNextTry() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = workout("Push", at: start)
        try await app.db.writer.write { try w.save($0) }
        server.replyToSync(.failure(.init(code: .unavailable, message: "")))

        await app.sync.sync()

        #expect(app.sync.lastError == "Can't reach Milo's server right now.")
        #expect(try stored(app, w.id)?.dirty == true)
        #expect(app.session.isSignedIn)

        await app.sync.sync()

        #expect(server.syncRequests.map(\.changes.count) == [1, 1])
        #expect(try stored(app, w.id)?.dirty == false)
        #expect(app.sync.lastError == nil)
    }

    @Test func anExpiredTokenSignsOutAndKeepsTheLog() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = workout("Push", at: start)
        try await app.db.writer.write { try w.save($0) }
        server.replyToSync(.failure(.init(code: .unauthenticated, message: "Session expired.")))

        await app.sync.sync()

        #expect(!app.session.isSignedIn && app.session.expired)
        #expect(try stored(app, w.id)?.dirty == true)
        // Signed out, there's nothing to sync with.
        await app.sync.sync()
        #expect(server.syncRequests.count == 1)
    }

    @Test func askingAgainMidSyncGoesRoundOnceMore() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let gate = Gate()
        server.holdNextSync(gate)
        let first = Task { await app.sync.sync() }
        try await eventually { server.syncRequests.count == 1 }

        let meanwhile = workout("Logged meanwhile", at: start)
        try await app.db.writer.write { try meanwhile.save($0) }
        await app.sync.sync()
        #expect(app.sync.isSyncing && server.syncRequests.count == 1)

        gate.open()
        await first.value
        #expect(server.syncRequests.map(\.changes.count) == [0, 1])
    }
}
