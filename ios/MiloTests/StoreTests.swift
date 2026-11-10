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

/// Logs one finished workout of `sets` (weight, reps) for an exercise.
private func logWorkout(_ db: AppDatabase, _ e: Exercise, _ sets: [(String, Int)], daysAgo: Double) throws -> Workout {
    let start = Date.now.addingTimeInterval(-daysAgo * 86400)
    let w = try db.startWorkout(name: "Day", items: [TemplateItem(exerciseId: e.id, groupLabel: "")], now: start)
    let we = try #require(try db.writer.read { d in try WorkoutExercise.filter(Column("workoutId") == w.id).fetchOne(d) })
    try db.writer.write { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).deleteAll(d) }
    for (i, (weight, reps)) in sets.enumerated() {
        var s = try db.addSet(to: we.id, done: true, now: start)
        s.weight = weight; s.reps = reps; s.unit = .kg; s.position = i + 1
        try db.update(s, now: start)
    }
    try db.finishWorkout(w.id, name: "Day", notes: "", at: start.addingTimeInterval(3600), now: start.addingTimeInterval(3600))
    return w
}

struct EditingTests {
    @Test func deletingAWorkoutTombstonesEverythingInIt() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try logWorkout(db, bench, [("60", 8), ("60", 8)], daysAgo: 1)
        try db.deleteWorkout(w.id)
        #expect(try db.writer.read { d in try db.workoutDetail(d, id: w.id) } == nil)
        #expect(try db.writer.read { d in try db.finishedWorkouts(d) }.isEmpty)
        let sets = try db.writer.read { d in try LoggedSet.fetchAll(d) }
        #expect(!sets.isEmpty && sets.allSatisfy { $0.deleted && $0.dirty })
    }

    @Test func doItAgainCopiesExercisesNotTicks() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let past = try logWorkout(db, bench, [("60", 8)], daysAgo: 2)
        let again = try db.startWorkout(name: "Day", items: try db.items(ofWorkout: past.id))
        let entry = try #require(try db.writer.read { d in try db.workoutDetail(d, id: again.id) }?.entries.first)
        #expect(entry.exercise.id == bench.id)
        #expect(entry.sets.map(\.weight) == ["60"] && entry.sets.allSatisfy { !$0.done })
    }

    @Test func editingAPastWorkoutKeepsItsEndTime() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try logWorkout(db, bench, [("60", 8)], daysAgo: 3)
        let before = try #require(try db.writer.read { d in try Workout.fetchOne(d, key: w.id) })
        try db.finishWorkout(w.id, name: before.name, notes: "felt strong", at: try #require(before.endedAt))
        let after = try #require(try db.writer.read { d in try Workout.fetchOne(d, key: w.id) })
        #expect(after.endedAt == before.endedAt)
        #expect(after.updatedAt > before.updatedAt)
        #expect(after.notes == "felt strong")
    }

    @Test func heaviestYetAndEarlierTop() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        _ = try logWorkout(db, bench, [("60", 8)], daysAgo: 3)
        let heavier = try logWorkout(db, bench, [("62.5", 6), ("65", 5), ("65", 4)], daysAgo: 2)
        let lighter = try logWorkout(db, bench, [("55", 10)], daysAgo: 1)

        let up = try #require(try db.writer.read { d in try db.workoutDetail(d, id: heavier.id) })
        let upInsight = try db.writer.read { d in try db.insights(d, for: up) }
        let topSet = up.entries[0].sets.first { $0.weight == "65" }
        #expect(upInsight[up.entries[0].id]?.heaviestSet == topSet?.id)

        let down = try #require(try db.writer.read { d in try db.workoutDetail(d, id: lighter.id) })
        let downInsight = try #require(try db.writer.read { d in try db.insights(d, for: down) }[down.entries[0].id])
        #expect(downInsight.heaviestSet == nil)
        #expect(downInsight.earlierTop?.weight == "65")
    }

    @Test func companionsAreExercisesDoneTogether() throws {
        let bench = exercise("Bench Press (Barbell)"), fly = exercise("Cable Fly"), squat = exercise("Squat (Barbell)")
        let db = try freshDB(bench, fly, squat)
        let w = try db.startWorkout(name: "Push", items: [bench, fly].map { TemplateItem(exerciseId: $0.id, groupLabel: "") })
        try db.writer.write { d in try d.execute(sql: "UPDATE loggedSet SET done = 1") }
        try db.finishWorkout(w.id, name: "Push", notes: "")
        _ = try db.startWorkout(name: "Legs", items: [TemplateItem(exerciseId: squat.id, groupLabel: "")])
        let found = try db.writer.read { d in try db.companions(d, of: [bench.id]) }
        #expect(found.map(\.id) == [fly.id])
    }

    @Test func deletedOwnExerciseStaysInPastWorkouts() throws {
        let db = try freshDB()
        let mine = try db.createExercise(name: "Landmine Rotation (Barbell)", equipment: "barbell", measurement: .repsLoad, unilateral: true)
        let w = try logWorkout(db, mine, [("20", 12)], daysAgo: 1)
        try db.deleteExercise(mine.id)
        #expect(try db.writer.read { d in try db.allExercises(d) }.isEmpty)
        let entry = try db.writer.read { d in try db.workoutDetail(d, id: w.id) }?.entries.first
        #expect(entry?.exercise.name == "Landmine Rotation (Barbell)")
    }
}

struct SupersetLabelTests {
    @Test func lettersNeighboursInAGroup() {
        #expect(Superset.labels(["", "", "g", "g"]) == ["1", "2", "3a", "3b"])
        #expect(Superset.labels(["a", "a", "a", ""]) == ["1a", "1b", "1c", "2"])
        // Not neighbours, so not a superset.
        #expect(Superset.labels(["g", "", "g"]) == ["1", "2", "3"])
        #expect(Superset.labels([]) == [])
    }
}
