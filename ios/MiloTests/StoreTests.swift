import Foundation
import GRDB
import MiloAPI
import Testing
@testable import Milo

private func exercise(_ name: String, _ m: MeasurementKind = .repsLoad, unilateral: Bool = false) -> Exercise {
    Exercise(id: UUID(), builtin: true, name: name, aliases: [], primaryMuscles: ["chest"], secondaryMuscles: [], movementPattern: "", equipment: "", measurement: m, unilateral: unilateral, met: 4.5, about: "", approximate: false, updatedAt: .now, deleted: false, dirty: false)
}

private func freshDB(_ exercises: Exercise...) throws -> AppDatabase {
    let db = try AppDatabase.inMemory()
    try db.writer.write { d in for e in exercises { try e.save(d) } }
    return db
}

struct WorkoutStoreTests {
    @Test func finishDropsUntickedSets() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout()
        let we = try db.addExercise(bench.id, to: w.id)
        let second = try db.addSet(to: we.id)
        let sets = try db.writer.read { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).order(Column("position")).fetchAll(d) }
        var first = try #require(sets.first)
        first.weight = "60"; first.reps = 10; first.done = true
        try db.update(first)
        try db.finishWorkout(w.id, name: "", notes: "")

        let detail = try #require(try db.writer.read { d in try db.workoutDetail(d, id: w.id) })
        #expect(detail.entries.count == 1)
        #expect(detail.entries[0].sets.map(\.id) == [first.id])
        #expect(detail.workout.endedAt != nil)
        let tomb = try #require(try db.writer.read { d in try LoggedSet.fetchOne(d, key: second.id) })
        #expect(tomb.deleted && tomb.dirty)
    }

    @Test func unitSwitchKeepsWhatWasTyped() throws {
        let sumo = exercise("Sumo Squat (Dumbbell)")
        let db = try freshDB(sumo)
        let w = try db.startWorkout()
        let we = try db.addExercise(sumo.id, to: w.id)
        var set = try #require(try db.writer.read { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).fetchOne(d) })
        set.weight = "45"; set.unit = .lb; set.reps = 10
        try db.update(set)
        for unit: WeightUnit in [.kg, .lb, .kg] {
            try db.setUnit(unit, for: sumo.id)
        }
        let stored = try #require(try db.writer.read { d in try LoggedSet.fetchOne(d, key: set.id) })
        #expect(stored.weight == "45" && stored.unit == .lb)
        let entry = try #require(try db.writer.read { d in try db.workoutDetail(d, id: w.id) }?.entries.first)
        #expect(entry.unit == .kg)
        #expect(Format.weight(stored, in: entry.unit) == "20.4")
    }

    @Test func templatePrefillsFromLastTime() throws {
        let press = exercise("Chest Press (Machine)")
        let db = try freshDB(press)
        let first = try db.startWorkout(now: .now.addingTimeInterval(-86400))
        let we = try db.addExercise(press.id, to: first.id)
        var s = try #require(try db.writer.read { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).fetchOne(d) })
        s.weight = "30"; s.reps = 15; s.done = true
        try db.update(s)
        try db.finishWorkout(first.id, name: "Push", notes: "", at: .now.addingTimeInterval(-82800))

        let template = Template(id: UUID(), name: "Push day", notes: "", items: [TemplateItem(exerciseId: press.id, groupLabel: "")], updatedAt: .now, deleted: false, dirty: true)
        try db.save(template)
        let second = try db.startWorkout(template: template)
        let entry = try #require(try db.writer.read { d in try db.workoutDetail(d, id: second.id) }?.entries.first)
        #expect(entry.sets.count == 1)
        #expect(entry.sets[0].weight == "30" && entry.sets[0].reps == 15 && !entry.sets[0].done)
        #expect(entry.lastTime.map(\.weight) == ["30"])
        #expect(second.name == "Push day")
    }

    @Test func tombstoneOfUnsyncedRowGoesUpWithoutError() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout()
        let we = try db.addExercise(bench.id, to: w.id)
        let set = try #require(try db.writer.read { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).fetchOne(d) })
        try db.deleteSet(set.id)
        let detail = try #require(try db.writer.read { d in try db.workoutDetail(d, id: w.id) })
        #expect(detail.entries[0].sets.isEmpty)
        let dirty = try db.writer.read { d in try Synced.dirty(d, limit: 100) }
        #expect(dirty.contains { $0.syncKey == set.id && $0.deleted })
    }
}

struct ChangeApplierTests {
    @Test func applyAndUndo() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout()
        let we = WorkoutExercise(id: UUID(), workoutId: w.id, exerciseId: bench.id, position: 1, groupLabel: "", notes: "", updatedAt: .now, deleted: false, dirty: false)
        let set = LoggedSet(id: UUID(), workoutExerciseId: we.id, position: 1, kind: .normal, reps: 10, weight: "60", unit: .kg, perSide: false, durationS: nil, distanceM: nil, rpe: nil, done: true, updatedAt: .now, deleted: false, dirty: false)
        let applier = ChangeApplier(db: db)
        let token = try applier.apply([we.change, set.change], base: [])
        #expect(try db.writer.read { d in try LoggedSet.fetchOne(d, key: set.id) }?.dirty == true)

        try applier.undo(token)
        let undone = try #require(try db.writer.read { d in try LoggedSet.fetchOne(d, key: set.id) })
        #expect(undone.deleted)
    }

    @Test func staleCardRefused() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout()
        let we = try db.addExercise(bench.id, to: w.id)
        var set = try #require(try db.writer.read { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).fetchOne(d) })

        var version = Milo_RowVersion()
        version.table = "set"; version.id = Proto.id(set.id); version.updatedAt = Proto.time(set.updatedAt)
        // The user edits the set by hand after Milo looked at it.
        set.weight = "70"
        try db.update(set, now: set.updatedAt.addingTimeInterval(5))

        var proposed = set
        proposed.weight = "25"
        #expect(throws: ChangeApplier.ApplyError.stale) {
            try ChangeApplier(db: db).apply([proposed.change], base: [version])
        }
        #expect(try db.writer.read { d in try LoggedSet.fetchOne(d, key: set.id) }?.weight == "70")
    }
}

struct SessionTests {
    private func session(_ db: AppDatabase) -> Session {
        Session(db: db, keychain: Keychain(service: "dev.hishaam.milo.tests.\(UUID().uuidString)"))
    }

    @Test func sameUserKeepsDirtyRows() throws {
        let db = try freshDB()
        let s = session(db)
        try s.signedIn(token: "a", user: .init(id: "u1", username: "hishaam"))
        let w = try db.startWorkout()
        s.tokenExpired()
        #expect(!s.isSignedIn)
        try s.signedIn(token: "b", user: .init(id: "u1", username: "hishaam"))
        #expect(try db.writer.read { d in try Workout.fetchOne(d, key: w.id) }?.dirty == true)
    }

    @Test func otherUserStartsEmpty() throws {
        let db = try freshDB()
        let s = session(db)
        try s.signedIn(token: "a", user: .init(id: "u1", username: "hishaam"))
        _ = try db.startWorkout()
        s.signOut()
        try s.signedIn(token: "b", user: .init(id: "u2", username: "someone"))
        #expect(try db.writer.read { d in try Workout.fetchCount(d) } == 0)
    }
}
