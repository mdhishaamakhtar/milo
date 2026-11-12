import Foundation
import GRDB
import Testing
@testable import Milo

/// What the screens write. Every write marks its rows for the next sync.
struct StoreWriteTests {
    private let now = Date(timeIntervalSince1970: 1_794_300_000) // Tuesday 10 November 2026

    private func dirty(_ db: AppDatabase) throws -> Set<UUID> {
        Set(try db.writer.read { try Synced.dirty($0, limit: 1000) }.map(\.syncKey))
    }

    @Test func aNewWorkoutIsNamedForItsDayOrItsTemplate() throws {
        let db = try freshDB()
        #expect(try db.startWorkout(now: now).name == "Tuesday workout")
        let t = Template(id: UUID(), name: "Pull day", notes: "", items: [], updatedAt: now, deleted: false, dirty: false)
        let w = try db.startWorkout(template: t, now: now)
        #expect(w.name == "Pull day" && w.templateId == t.id && w.endedAt == nil && w.dirty)
    }

    @Test func aNewSetCopiesTheOneBefore() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout(now: now)
        let we = try db.addExercise(bench.id, to: w.id)
        var first = try #require(try sets(db, of: we.id).first)
        first.weight = "60"; first.reps = 8; first.kind = .warmup; first.done = true
        try db.update(first)

        let second = try db.addSet(to: we.id)
        #expect(second.weight == "60" && second.reps == 8 && second.kind == .warmup && second.position == 2 && !second.done)

        // An exercise with no sets left starts a blank one, in the exercise's unit.
        try db.setUnit(.lb, for: bench.id)
        let other = try db.addExercise(bench.id, to: w.id)
        for s in try sets(db, of: other.id) { try db.deleteSet(s.id) }
        let blank = try db.addSet(to: other.id, done: true)
        #expect(blank.weight == "" && blank.unit == .lb && blank.position == 1 && blank.done)
    }

    @Test func aUnilateralExerciseStartsEachSide() throws {
        let press = exercise("Single Arm Press (Dumbbell)", unilateral: true)
        let db = try freshDB(press)
        let w = try db.startWorkout(now: now)
        let we = try db.addExercise(press.id, to: w.id, groupLabel: "a")
        #expect(try sets(db, of: we.id).map(\.perSide) == [true])
        #expect(we.groupLabel == "a" && we.position == 1)
        #expect(try db.addExercise(press.id, to: w.id).position == 2)
    }

    @Test func tickingTogglesAndAMissingSetIsNotTicked() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout(now: now)
        let set = try #require(try sets(db, of: db.addExercise(bench.id, to: w.id).id).first)
        #expect(try db.toggleDone(set.id))
        #expect(try !db.toggleDone(set.id))
        #expect(try !db.toggleDone(UUID()))
        try db.deleteSet(UUID())
    }

    @Test func removingAnExerciseTombstonesItAndItsSets() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout(now: now)
        let we = try db.addExercise(bench.id, to: w.id)
        _ = try db.addSet(to: we.id)
        let gone = try db.removeExercise(we.id)
        #expect(gone.count == 3)
        #expect(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.isEmpty == true)
        #expect(try db.removeExercise(UUID()).isEmpty)

        try db.restore(gone)
        #expect(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.first?.sets.count == 2)
    }

    @Test func notesRestAndUnitAreRemembered() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        var settings = UserSettings.fresh
        settings.displayUnit = .lb
        try db.save(settings)
        let w = try db.startWorkout(now: now)
        let we = try db.addExercise(bench.id, to: w.id)

        try db.setNote("Seat on 4", for: we.id)
        try db.setNote("lost", for: UUID())
        try db.setRest(150, for: bench.id)
        let pref = try #require(try db.writer.read { try ExercisePref.fetchOne($0, key: bench.id) })
        // Setting rest first keeps the unit the exercise was already shown in.
        #expect(pref.unit == .lb && pref.restS == 150 && pref.dirty)
        try db.setUnit(.kg, for: bench.id)
        #expect(try db.writer.read { try db.unit($0, for: bench.id) } == (.kg, 150))
        #expect(try db.writer.read { try WorkoutExercise.fetchOne($0, key: we.id) }?.notes == "Seat on 4")
    }

    @Test func finishingDropsUntickedSetsAndEmptyExercises() throws {
        let bench = exercise("Bench Press (Barbell)"), fly = exercise("Cable Fly")
        let db = try freshDB(bench, fly)
        let w = try db.startWorkout(name: "Push", items: [bench, fly].map { TemplateItem(exerciseId: $0.id, groupLabel: "") }, now: now)
        let unticked = try db.addSet(to: try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.first).id)
        let detail = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) })
        try db.toggleDone(detail.entries[0].sets[0].id)

        try db.finishWorkout(w.id, name: "  ", notes: "Short one", at: now.addingTimeInterval(1800))

        let after = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) })
        #expect(after.workout.name == "Push" && after.workout.notes == "Short one")
        #expect(after.workout.endedAt == now.addingTimeInterval(1800))
        #expect(after.entries.map(\.exercise.name) == ["Bench Press (Barbell)"])
        #expect(after.entries[0].sets.map(\.id) == [detail.entries[0].sets[0].id])
        // What was dropped goes up as a tombstone.
        let tomb = try #require(try db.writer.read { try LoggedSet.fetchOne($0, key: unticked.id) })
        #expect(tomb.deleted && tomb.dirty)
        try db.finishWorkout(UUID(), name: "", notes: "")
    }

    @Test func editingAFinishedWorkoutKeepsItsEndTime() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try logWorkout(db, bench, [("60", 8)], daysAgo: 3, now: now)
        let before = try #require(try db.writer.read { try Workout.fetchOne($0, key: w.id) })
        try db.finishWorkout(w.id, name: before.name, notes: "felt strong", at: try #require(before.endedAt), now: now)
        let after = try #require(try db.writer.read { try Workout.fetchOne($0, key: w.id) })
        #expect(after.endedAt == before.endedAt && after.updatedAt > before.updatedAt && after.notes == "felt strong")
    }

    @Test func aTemplateFillsInFromLastTime() throws {
        let press = exercise("Chest Press (Machine)")
        let db = try freshDB(press)
        try logWorkout(db, press, [("30", 15)], daysAgo: 1, now: now)
        let template = Template(id: UUID(), name: "Push day", notes: "", items: [TemplateItem(exerciseId: press.id, groupLabel: "")], updatedAt: now, deleted: false, dirty: true)

        let w = try db.startWorkout(template: template, now: now)

        let entry = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.first)
        #expect(entry.sets.count == 1)
        #expect(entry.sets[0].weight == "30" && entry.sets[0].reps == 15 && !entry.sets[0].done)
        #expect(entry.lastTime.map(\.weight) == ["30"])
    }

    @Test func switchingUnitKeepsWhatWasTyped() throws {
        let sumo = exercise("Sumo Squat (Dumbbell)")
        let db = try freshDB(sumo)
        let w = try db.startWorkout(now: now)
        var set = try #require(try sets(db, of: db.addExercise(sumo.id, to: w.id).id).first)
        set.weight = "45"; set.unit = .lb; set.reps = 10
        try db.update(set)
        for unit: WeightUnit in [.kg, .lb, .kg] {
            try db.setUnit(unit, for: sumo.id)
        }
        let stored = try #require(try db.writer.read { try LoggedSet.fetchOne($0, key: set.id) })
        #expect(stored.weight == "45" && stored.unit == .lb)
        let entry = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries.first)
        #expect(entry.unit == .kg && Format.weight(stored, in: entry.unit) == "20.4")
    }

    @Test func deletingASetNeverSyncedStillSendsItsTombstone() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try db.startWorkout(now: now)
        let set = try #require(try sets(db, of: db.addExercise(bench.id, to: w.id).id).first)
        try db.deleteSet(set.id)
        #expect(try db.writer.read { try db.workoutDetail($0, id: w.id) }?.entries[0].sets.isEmpty == true)
        #expect(try db.writer.read { try Synced.dirty($0, limit: 100) }.contains { $0.syncKey == set.id && $0.deleted })
    }

    @Test func aDeletedWorkoutComesBackWithUndo() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        let w = try logWorkout(db, bench, [("60", 8), ("60", 8)], daysAgo: 1, now: now)
        let rows = try db.deleteWorkout(w.id)
        #expect(rows.count == 4)
        #expect(try db.writer.read { try db.finishedWorkouts($0) }.isEmpty)
        #expect(try db.writer.read { try LoggedSet.fetchAll($0) }.allSatisfy { $0.deleted && $0.dirty })
        #expect(try db.deleteWorkout(UUID()).isEmpty)

        try db.restore(rows)

        let back = try #require(try db.writer.read { try db.workoutDetail($0, id: w.id) })
        #expect(back.entries.first?.sets.count == 2)
        #expect(try dirty(db).isSuperset(of: rows.map(\.syncKey)))
    }

    @Test func templatesFromAWorkoutKeepOrderAndSupersets() throws {
        let row = exercise("Row (Cable)"), curl = exercise("Curl (Cable)"), pull = exercise("Pulldown (Cable)")
        let db = try freshDB(row, curl, pull)
        let items = [TemplateItem(exerciseId: pull.id, groupLabel: ""), TemplateItem(exerciseId: row.id, groupLabel: "s"), TemplateItem(exerciseId: curl.id, groupLabel: "s")]
        let w = try db.startWorkout(name: "Pull", items: items, now: now)

        let t = try db.templateFrom(workout: w.id, name: "Pull day")
        #expect(t.items == items && t.name == "Pull day")
        let gone = try db.deleteTemplate(t.id)
        #expect(try db.writer.read { try db.templates($0) }.isEmpty)
        #expect(try db.deleteTemplate(UUID()).isEmpty)
        try db.restore(gone)
        #expect(try db.writer.read { try db.templates($0) }.map(\.template.name) == ["Pull day"])
    }

    @Test func aHealthReadingIsAddedOnceAndAManualOneIsReplaced() throws {
        let db = try freshDB()
        let id = UUID()
        try db.addBody(weightKg: 80, bodyFatPct: nil, at: now, source: "health", id: id)
        try db.addBody(weightKg: 81, bodyFatPct: nil, at: now, source: "health", id: id)
        #expect(try db.writer.read { try BodyMeasurement.fetchOne($0, key: id) }?.weightKg == 80)

        let mine = UUID()
        try db.addBody(weightKg: 80, bodyFatPct: nil, at: now, source: "manual", id: mine)
        try db.addBody(weightKg: 79.5, bodyFatPct: 17, at: now, source: "manual", id: mine)
        let stored = try #require(try db.writer.read { try BodyMeasurement.fetchOne($0, key: mine) })
        #expect(stored.weightKg == 79.5 && stored.bodyFatPct == 17)
    }

    @Test func savingAWorkoutMarksItForSync() throws {
        let db = try freshDB()
        var w = try db.startWorkout(now: now)
        try db.writer.write { try w.stamped(w.updatedAt, dirty: false).save($0) }
        w.name = "Renamed"
        try db.save(w, now: now.addingTimeInterval(60))
        let stored = try #require(try db.writer.read { try Workout.fetchOne($0, key: w.id) })
        #expect(stored.name == "Renamed" && stored.dirty && stored.updatedAt == now.addingTimeInterval(60))
    }

    @Test func eraseAllEmptiesThePhone() throws {
        let bench = exercise("Bench Press (Barbell)")
        let db = try freshDB(bench)
        try logWorkout(db, bench, [("60", 8)], daysAgo: 1, now: now)
        try db.setMeta(.cursor, "42")
        try db.eraseAll()
        #expect(try db.writer.read { try Workout.fetchCount($0) + Exercise.fetchCount($0) } == 0)
        #expect(try db.meta(.cursor) == nil)
        try db.setMeta(.username, "hishaam")
        try db.setMeta(.username, nil)
        #expect(try db.meta(.username) == nil)
    }
}
