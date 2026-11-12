import Connect
import Foundation
import GRDB
import Testing
@testable import Milo

/// What the screens share: starting workouts, deleting with Undo, and where Milo is asked from.
@MainActor
struct AppModelTests {
    @Test func startingAWorkoutOpensIt() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let app = offlineApp(db)
        let push = Template(id: UUID(), name: "Push", notes: "", items: [TemplateItem(exerciseId: bench.id, groupLabel: "")], updatedAt: .now, deleted: false, dirty: true)

        app.startWorkout(template: push)

        #expect(app.showingWorkout)
        let running = try #require(try db.writer.read { try db.runningWorkout($0) })
        #expect(running.name == "Push" && running.templateId == push.id)
    }

    @Test func withOneRunningStartingAgainOpensThatOne() throws {
        let db = try freshDB()
        let app = offlineApp(db)
        let first = try db.startWorkout(name: "Already going")

        app.startWorkout()

        #expect(app.showingWorkout)
        #expect(try db.writer.read { try Workout.fetchCount($0) } == 1)
        #expect(try db.writer.read { try db.runningWorkout($0) }?.id == first.id)
    }

    @Test func doItAgainCopiesAPastWorkout() throws {
        let row = exercise("Row (Cable)"), curl = exercise("Curl (Cable)")
        let db = try freshDB(row, curl)
        let t = UUID()
        let past = try db.startWorkout(name: "Pull", items: [TemplateItem(exerciseId: row.id, groupLabel: "s"), TemplateItem(exerciseId: curl.id, groupLabel: "s")], templateId: t)
        try db.writer.write { try $0.execute(sql: "UPDATE loggedSet SET done = 1, weight = '40', reps = 10") }
        try db.finishWorkout(past.id, name: "Pull", notes: "")
        let app = offlineApp(db)

        app.startWorkout(copying: past.id)

        let again = try #require(try db.writer.read { try db.runningWorkout($0) })
        #expect(again.id != past.id && again.name == "Pull" && again.templateId == t)
        #expect(try db.items(ofWorkout: again.id) == [TemplateItem(exerciseId: row.id, groupLabel: "s"), TemplateItem(exerciseId: curl.id, groupLabel: "s")])
        // The numbers come from last time, not ticked.
        let sets = try #require(try db.writer.read { try db.workoutDetail($0, id: again.id) }).entries.flatMap(\.sets)
        #expect(sets.map(\.weight) == ["40", "40"] && sets.allSatisfy { !$0.done })
    }

    @Test func aDeletedWorkoutCanBeTakenBack() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try logWorkout(db, bench, [("60", 8)], daysAgo: 1)
        let app = offlineApp(db)

        app.deleteWorkout(w)
        #expect(app.undo?.message == "Deleted Day")
        #expect(try db.writer.read { try db.finishedWorkouts($0) }.isEmpty)

        app.undo?.restore()
        #expect(try db.writer.read { try db.finishedWorkouts($0) }.map(\.workout.id) == [w.id])

        app.deleteWorkout(w, message: "Workout discarded")
        #expect(app.undo?.message == "Workout discarded")
    }

    @Test func nothingDeletedNothingToUndo() throws {
        let app = offlineApp(try freshDB())
        app.offerUndo("Deleted nothing", rows: [])
        #expect(app.undo == nil)
    }

    @Test func closingMiloOpensOnlyAWorkoutItStarted() throws {
        let db = try freshDB()
        let app = offlineApp(db)
        app.miloClosed()
        #expect(!app.showingWorkout)
        app.workoutStartedByMilo = true
        app.miloClosed()
        // Nothing is running any more (it was undone), so nothing opens.
        #expect(!app.showingWorkout && !app.workoutStartedByMilo)
    }
}

/// The API's errors, in words a person can act on.
struct APIErrorTests {
    @Test func errorsReadPlainly() {
        // A network failure doesn't show its technical detail.
        #expect(APIError(.init(code: .unavailable, message: "dial tcp")).message == "Can't reach Milo's server right now.")
        #expect(APIError(.init(code: .deadlineExceeded, message: nil)).message == "Can't reach Milo's server right now.")
        #expect(APIError(.init(code: .unknown, message: "")).message == "Something went wrong (unknown).")
        #expect(APIError(.init(code: .invalidArgument, message: "Username taken.")).message == "Username taken.")
        #expect(APIError(.init(code: .internalError, message: nil)).message == "Something went wrong (internalError).")
        #expect(APIError(.init(code: .unauthenticated, message: "")).isUnauthenticated)
    }
}

/// Signing in, out, and away. The log on the phone belongs to one account.
@MainActor
struct SessionTests {
    private func session(_ db: AppDatabase) -> Session {
        Session(db: db, keychain: Keychain(service: "dev.hishaam.milo.tests.\(UUID().uuidString)"))
    }

    @Test func theSameUserKeepsWhatWasntSyncedYet() throws {
        let db = try freshDB()
        let s = session(db)
        try s.signedIn(token: "a", user: .init(id: "u1", username: "hishaam"))
        let w = try db.startWorkout()
        s.tokenExpired()
        #expect(!s.isSignedIn && s.expired)
        try s.signedIn(token: "b", user: .init(id: "u1", username: "hishaam"))
        #expect(try db.writer.read { try Workout.fetchOne($0, key: w.id) }?.dirty == true)
        #expect(!s.expired && s.token == "b")
    }

    @Test func anotherUserStartsEmpty() throws {
        let db = try freshDB()
        let s = session(db)
        try s.signedIn(token: "a", user: .init(id: "u1", username: "hishaam"))
        try db.startWorkout()
        s.signOut()
        try s.signedIn(token: "b", user: .init(id: "u2", username: "someone"))
        #expect(try db.writer.read { try Workout.fetchCount($0) } == 0)
    }

    @Test func forgettingTheAccountClearsThePhone() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let s = session(db)
        try s.signedIn(token: "t", user: .init(id: "u1", username: "hishaam"))
        try logWorkout(db, bench, [("60", 8)], daysAgo: 1)

        try s.forget()

        #expect(!s.isSignedIn && s.token == nil)
        #expect(try db.writer.read { try Workout.fetchCount($0) } == 0)
    }

    @Test func aSignedInUserIsRememberedAcrossLaunches() throws {
        let db = try freshDB()
        let keychain = Keychain(service: "dev.hishaam.milo.tests.\(UUID().uuidString)")
        try Session(db: db, keychain: keychain).signedIn(token: "t", user: .init(id: "u1", username: "hishaam"))
        let relaunched = Session(db: db, keychain: keychain)
        #expect(relaunched.user == .init(id: "u1", username: "hishaam") && relaunched.isSignedIn)
        relaunched.signOut()
        #expect(!Session(db: db, keychain: keychain).isSignedIn)
    }
}
