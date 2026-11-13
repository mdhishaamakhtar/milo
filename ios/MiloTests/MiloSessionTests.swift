import Connect
import Foundation
import GRDB
import MiloAPI
import Testing
@testable import Milo

/// One conversation with Milo, against a fake server: what the app sends, how the streamed
/// answer turns into lines and cards, and what applying, undoing and dismissing a card does.
@MainActor
struct MiloSessionTests {
    /// The conversation as text, one line per item.
    private func lines(_ session: MiloSession) -> [String] {
        session.items.map {
            switch $0 {
            case .user(_, let t): "you: \(t)"
            case .reply(_, let t): "milo: \(t)"
            case .reading(_, let s): "read: \(s)"
            case .card(let c): "card: \(c.title)"
            case .problem(_, let p): "problem: \(p)"
            }
        }
    }

    // MARK: The stream

    @Test func textStreamsIntoOneReplyAndAReadingStartsTheNext() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([
            .reading("Read your bench history"), .text("Bench went "), .unknownEvent, .text("up 2.5 kg."),
            .reading("Read your squat history"), .text("Squat held."), .done(conversationID: "c1"),
        ])
        let session = MiloSession(app: app)

        await session.send("  how did I do?\n", context: .today)

        #expect(lines(session) == [
            "you: how did I do?",
            "read: Read your bench history", "milo: Bench went up 2.5 kg.",
            "read: Read your squat history", "milo: Squat held.",
        ])
        #expect(session.conversationID == "c1")
        #expect(!session.busy && !session.isWorking)
        let sent = try #require(server.sent.first)
        #expect(sent.text == "how did I do?")
        #expect(sent.conversationID.isEmpty)
        #expect(sent.context.screen == "today" && sent.context.timezone == TimeZone.current.identifier)
    }

    @Test func theNextMessageGoesToTheSameConversationWithItsContext() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let session = MiloSession(app: app)
        server.replyToSend([.text("Hi."), .done(conversationID: "c1")])
        await session.send("hi", context: .today)

        let workout = UUID(), exercise = UUID()
        server.replyToSend([.text("Sure."), .done(conversationID: "c1")])
        await session.send("and this one?", context: MiloContext(screen: "exercise", prompt: "", workoutID: workout, exerciseID: exercise))

        let second = try #require(server.sent.last)
        #expect(second.conversationID == "c1")
        #expect(second.context.screen == "exercise")
        #expect(second.context.workoutID == Proto.id(workout) && second.context.exerciseID == Proto.id(exercise))
        #expect(server.tokens.allSatisfy { $0 == "token-1" })
    }

    @Test func whatWasJustLoggedSyncsBeforeMiloIsAsked() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = try app.db.startWorkout(name: "Push")
        server.replyToSend([.done(conversationID: "c1")])

        await MiloSession(app: app).send("what's next?", context: .today)

        #expect(server.calls == ["Sync", "Send"])
        #expect(server.syncRequests[0].changes.contains { $0.workout.id == Proto.id(w.id) })
    }

    @Test func aProblemFromMiloShowsAsALine() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([.problem("You've used today's messages. Logging by hand still works."), .done(conversationID: "c1")])
        let session = MiloSession(app: app)

        await session.send("one more", context: .today)

        #expect(lines(session) == ["you: one more", "problem: You've used today's messages. Logging by hand still works."])
    }

    @Test func aDroppedStreamSaysTheServerCantBeReached() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([.text("Let me look"), .fail(.init(code: .unavailable, message: ""))])
        let session = MiloSession(app: app)

        await session.send("how was today?", context: .today)

        #expect(lines(session) == ["you: how was today?", "milo: Let me look", "problem: Can't reach Milo's server right now."])
        #expect(app.session.isSignedIn)
    }

    @Test func anExpiredTokenSignsOutAndSaysWhy() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([.fail(.init(code: .unauthenticated, message: "Sign in again."))])
        let session = MiloSession(app: app)

        await session.send("hi", context: .today)

        #expect(lines(session).last == "problem: Sign in again.")
        #expect(!app.session.isSignedIn && app.session.expired)
    }

    @Test func offlineItSaysSoAndCallsNothing() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        app.connectivity.isOnline = false
        let session = MiloSession(app: app)

        await session.send("log 3 sets", context: .today)

        #expect(lines(session) == ["you: log 3 sets", "problem: Milo needs a connection. Logging by hand still works."])
        #expect(server.calls.isEmpty)
    }

    @Test func aBlankMessageIsNotSent() async throws {
        let server = FakeServer()
        let session = MiloSession(app: try signedInApp(server))
        await session.send(" \n ", context: .today)
        #expect(session.isEmpty && server.calls.isEmpty)
    }

    // MARK: While Milo works

    @Test func workingMeansWaitingWithNothingStreamingYet() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let thinking = Gate(), reading = Gate(), answered = Gate(), suggested = Gate()
        var suggestions = Milo_Card()
        suggestions.id = "s1"
        suggestions.kind = .suggestions
        server.replyToSend([
            .wait(thinking), .reading("Read last week"), .wait(reading), .text("Three sessions."), .wait(answered),
            .card(suggestions), .wait(suggested), .done(conversationID: "c1"),
        ])
        let session = MiloSession(app: app)
        let sending = Task { await session.send("how was last week?", context: .today) }

        try await eventually { !server.sent.isEmpty }
        #expect(session.isWorking && !session.hasReadSinceAsking)
        // A second message while one is on its way is ignored.
        await session.send("hello?", context: .today)
        #expect(server.sent.count == 1 && lines(session) == ["you: how was last week?"])

        thinking.open()
        try await eventually { session.items.count == 2 }
        #expect(session.isWorking && session.hasReadSinceAsking)

        reading.open()
        try await eventually { session.items.count == 3 }
        #expect(!session.isWorking)

        answered.open()
        try await eventually { session.items.count == 4 }
        // What to say next comes after the answer: Milo is done, though the stream isn't.
        #expect(session.busy && !session.isWorking)

        suggested.open()
        await sending.value
        #expect(!session.busy)
    }

    // MARK: Cards

    /// A running workout with bench in it, synced: what Milo's cards build on.
    private func runningBench(_ app: AppModel) throws -> (Workout, WorkoutExercise, LoggedSet) {
        let bench = exercise("Bench Press (Barbell)")
        try app.db.writer.write { try bench.save($0) }
        let w = try app.db.startWorkout(name: "Push")
        let we = try app.db.addExercise(bench.id, to: w.id)
        let set = try #require(try app.db.writer.read { try LoggedSet.filter(Column("workoutExerciseId") == we.id).fetchOne($0) })
        try app.db.writer.write { db in
            for r in [w.stamped(w.updatedAt, dirty: false), we.stamped(we.updatedAt, dirty: false)] as [any SyncedRecord] { try r.save(db) }
            try set.stamped(set.updatedAt, dirty: false).save(db)
        }
        return (w, we, set)
    }

    private func card(_ id: String, _ kind: Milo_CardKind, title: String, changes: [Milo_Change], base: [Milo_RowVersion] = [], workoutID: UUID? = nil, autoApply: Bool = false) -> Milo_Card {
        var c = Milo_Card()
        c.id = id; c.kind = kind; c.title = title; c.changes = changes; c.base = base; c.autoApply = autoApply
        c.workoutID = workoutID.map(Proto.id) ?? ""
        return c
    }

    private func version(_ set: LoggedSet) -> Milo_RowVersion {
        var v = Milo_RowVersion()
        v.table = "set"; v.id = Proto.id(set.id); v.updatedAt = Proto.time(set.updatedAt)
        return v
    }

    @Test func aDoneCardIsAppliedOnArrivalAndTheWorkoutOpensOnIt() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let (w, _, _) = try runningBench(app)
        let squat = exercise("Squat (Barbell)")
        try await app.db.writer.write { try squat.save($0) }
        let we = WorkoutExercise(id: UUID(), workoutId: w.id, exerciseId: squat.id, position: 2, groupLabel: "", notes: "", updatedAt: .now, deleted: false, dirty: false)
        let set = LoggedSet(id: UUID(), workoutExerciseId: we.id, position: 1, kind: .normal, reps: 5, weight: "100", unit: .kg, perSide: false, durationS: nil, distanceM: nil, rpe: nil, done: true, updatedAt: .now, deleted: false, dirty: false)
        let done = card("k1", .done, title: "Logged squat", changes: [we.change, set.change], workoutID: w.id, autoApply: true)
        server.replyToSend([.card(done), .text("Logged."), .done(conversationID: "c1")])
        let session = MiloSession(app: app)

        await session.send("squat 100 × 5", context: .today)

        #expect(session.state(done) == .applied)
        #expect(session.addedWorkoutExercise == we.id)
        let stored = try #require(try await app.db.writer.read { try LoggedSet.fetchOne($0, key: set.id) })
        #expect(stored.weight == "100" && stored.dirty)
        try await eventually { server.cardStatuses.count == 1 }
        #expect(server.cardStatuses[0].cardID == "k1" && server.cardStatuses[0].status == .applied)
        #expect(lines(session) == ["you: squat 100 × 5", "card: Logged squat", "milo: Logged."])
    }

    @Test func aWorkoutMiloStartsOpensOnceItsSheetCloses() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = Workout(id: UUID(), name: "Pull day", notes: "", startedAt: .now, endedAt: nil, templateId: nil, healthUUID: nil, updatedAt: .now, deleted: false, dirty: false)
        let started = card("k1", .done, title: "Started Pull day", changes: [w.change], workoutID: w.id, autoApply: true)
        server.replyToSend([.card(started), .done(conversationID: "c1")])
        let session = MiloSession(app: app)

        await session.send("start pull day", context: .today)
        #expect(app.workoutStartedByMilo)

        app.miloClosed()
        #expect(app.showingWorkout && !app.workoutStartedByMilo)
    }

    @Test func undoingAStartedWorkoutTakesItAway() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let w = Workout(id: UUID(), name: "Pull day", notes: "", startedAt: .now, endedAt: nil, templateId: nil, healthUUID: nil, updatedAt: .now, deleted: false, dirty: false)
        let started = card("k1", .done, title: "Started Pull day", changes: [w.change], workoutID: w.id, autoApply: true)
        server.replyToSend([.card(started), .done(conversationID: "c1")])
        let session = MiloSession(app: app)
        await session.send("start pull day", context: .today)

        session.undo(started)

        #expect(session.state(started) == .undone && !app.workoutStartedByMilo)
        let db = app.db
        #expect(try await db.writer.read { try db.runningWorkout($0) } == nil)
        app.miloClosed()
        #expect(!app.showingWorkout)
        try await eventually { server.cardStatuses.count == 2 }
        #expect(server.cardStatuses.map(\.status) == [.applied, .undone])
    }

    @Test func whatBecameOfACardIsToldOnceTheServerIsBack() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let (_, _, set) = try runningBench(app)
        var heavier = set
        heavier.weight = "70"
        let change = card("k1", .change, title: "Fix set 1", changes: [heavier.change], base: [version(set)])
        let session = MiloSession(app: app)
        server.cardStatus(down: true)

        session.apply(change)
        session.undo(change)
        try await eventually { server.calls.filter { $0 == "SetCardStatus" }.count >= 1 }
        #expect(server.cardStatuses.isEmpty)

        // Only where it ended up is worth telling.
        server.cardStatus(down: false)
        await app.sync.sync()
        #expect(server.cardStatuses.map(\.status) == [.undone])
        await app.sync.sync()
        #expect(server.cardStatuses.count == 1)
    }

    @Test func aProposedChangeWaitsThenAppliesAndUndoes() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let (w, _, set) = try runningBench(app)
        var heavier = set
        heavier.weight = "62.5"; heavier.reps = 8
        let change = card("k1", .change, title: "Fix set 1", changes: [heavier.change], base: [version(set)], workoutID: w.id)
        server.replyToSend([.card(change), .done(conversationID: "c1")])
        let session = MiloSession(app: app)
        await session.send("set 1 was 62.5", context: .today)
        #expect(session.state(change) == .pending)
        #expect(try await app.db.writer.read { try LoggedSet.fetchOne($0, key: set.id) }?.weight == "")

        #expect(session.apply(change))
        #expect(session.state(change) == .applied)
        #expect(try await app.db.writer.read { try LoggedSet.fetchOne($0, key: set.id) }?.weight == "62.5")

        session.undo(change)
        #expect(session.state(change) == .undone)
        #expect(try await app.db.writer.read { try LoggedSet.fetchOne($0, key: set.id) }?.weight == "")
        // Undo only works once.
        session.undo(change)
        #expect(session.state(change) == .undone)
    }

    @Test func notNowLeavesTheLogAlone() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let (_, _, set) = try runningBench(app)
        var heavier = set
        heavier.weight = "70"
        let change = card("k1", .change, title: "Fix set 1", changes: [heavier.change], base: [version(set)])
        let session = MiloSession(app: app)

        session.dismiss(change)

        #expect(session.state(change) == .dismissed)
        #expect(try await app.db.writer.read { try LoggedSet.fetchOne($0, key: set.id) }?.weight == "")
        try await eventually { server.cardStatuses.count == 1 }
        #expect(server.cardStatuses[0].status == .dismissed)
        // Nothing was applied, so there's nothing to undo.
        session.undo(change)
        #expect(session.state(change) == .dismissed)
    }

    @Test func aCardForARowChangedSinceIsRefused() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        let (_, _, set) = try runningBench(app)
        let base = version(set)
        var typed = set
        typed.weight = "80"
        try app.db.update(typed, now: set.updatedAt.addingTimeInterval(10))
        var proposed = set
        proposed.weight = "60"
        let change = card("k1", .change, title: "Fix set 1", changes: [proposed.change], base: [base])
        let session = MiloSession(app: app)

        #expect(!session.apply(change))

        #expect(session.state(change) == .stale)
        #expect(try await app.db.writer.read { try LoggedSet.fetchOne($0, key: set.id) }?.weight == "80")
        #expect(server.cardStatuses.isEmpty)
    }

    // MARK: Earlier conversations

    @Test func openingAStoredConversationRestoresItsLinesAndCards() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        var stored = Milo_GetConversationResponse()
        func item(_ fill: (inout Milo_MiloItem) -> Void) -> Milo_MiloItem { var i = Milo_MiloItem(); fill(&i); return i }
        func storedCard(_ id: String, _ status: Milo_CardStatus) -> Milo_Card {
            var c = card(id, .change, title: id, changes: [])
            c.status = status
            return c
        }
        stored.items = [
            item { $0.userText = "fix my bench" },
            item { $0.reading = { var r = Milo_Reading(); r.summary = "Read Tuesday"; return r }() },
            item { $0.assistantText = "Here you go." },
            item { $0.card = storedCard("applied", .applied) },
            item { $0.card = storedCard("dismissed", .dismissed) },
            item { $0.card = storedCard("undone", .undone) },
            item { $0.card = storedCard("pending", .pending) },
            Milo_MiloItem(),
        ]
        server.store(stored, id: "c9")
        let session = MiloSession(app: app)

        await session.open("c9")

        #expect(session.conversationID == "c9")
        #expect(lines(session) == ["you: fix my bench", "read: Read Tuesday", "milo: Here you go.", "card: applied", "card: dismissed", "card: undone", "card: pending"])
        #expect(session.state(storedCard("applied", .applied)) == .applied)
        #expect(session.state(storedCard("dismissed", .dismissed)) == .dismissed)
        #expect(session.state(storedCard("undone", .undone)) == .undone)
        #expect(session.state(storedCard("pending", .pending)) == .pending)
    }

    @Test func aConversationThatCantBeOpenedLeavesThisOne() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([.text("Hi."), .done(conversationID: "c1")])
        let session = MiloSession(app: app)
        await session.send("hi", context: .today)

        await session.open("gone")

        #expect(session.conversationID == "c1" && lines(session) == ["you: hi", "milo: Hi."])
    }

    @Test func halfAnHourAloneStartsAfresh() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([.text("Hi."), .done(conversationID: "c1")])
        let session = MiloSession(app: app)
        await session.send("hi", context: .today)
        let said = session.lastActive

        session.resetIfIdle(now: said.addingTimeInterval(29 * 60))
        #expect(session.conversationID == "c1" && !session.isEmpty)

        session.resetIfIdle(now: said.addingTimeInterval(31 * 60))
        #expect(session.conversationID.isEmpty && session.isEmpty)
    }

    @Test func askingFromAScreenOpensMiloWithTheMessageSent() async throws {
        let server = FakeServer()
        let app = try signedInApp(server)
        server.replyToSend([.text("Found it."), .done(conversationID: "c1")])
        app.miloContext = .history

        app.ask("legs in october")

        #expect(app.showingMilo)
        try await eventually { !app.milo.busy && app.milo.items.count == 2 }
        #expect(server.sent.first?.text == "legs in october" && server.sent.first?.context.screen == "history")
    }
}
