import Foundation
import GRDB
import Testing
@testable import Milo

/// The workout screen's state: which exercise is open, ticking sets and the rest after
/// them, and typing numbers on the pad.
@MainActor
struct WorkoutModelTests {
    let bench = exercise("Bench Press (Barbell)")
    let squat = exercise("Squat (Barbell)")
    let plank = exercise("Plank", .timed)
    let dips = exercise("Dip", .bodyweight)

    /// A running workout of bench (two sets), squat (one) and plank (one), on screen.
    private func running(_ extra: Exercise...) async throws -> (AppModel, WorkoutModel) {
        let db = try freshDB(bench, squat, plank, dips)
        let w = try db.startWorkout(name: "Monday", now: .now)
        let we = try db.addExercise(bench.id, to: w.id)
        _ = try db.addSet(to: we.id)
        try db.addExercise(squat.id, to: w.id)
        try db.addExercise(plank.id, to: w.id)
        for e in extra { try db.addExercise(e.id, to: w.id) }
        let app = offlineApp(db)
        let model = WorkoutModel(app: app)
        model.watch()
        try await eventually { model.entries.count == 3 + extra.count }
        return (app, model)
    }

    private func entry(_ model: WorkoutModel, _ e: Exercise) throws -> ExerciseEntry {
        try #require(model.entries.first { $0.exercise.id == e.id })
    }

    /// Waits for the screen to show what was just written.
    private func settle(_ model: WorkoutModel, _ condition: () -> Bool) async throws {
        try await eventually("the workout screen to update", condition)
    }

    @Test func theOpenExerciseIsTheFirstNotFinished() async throws {
        let (_, model) = try await running()
        #expect(model.current?.exercise.id == bench.id)
        #expect(model.finished.isEmpty && model.upNext.map(\.exercise.id) == [squat.id, plank.id])

        for s in try entry(model, bench).sets { model.tick(s, in: try entry(model, bench)) }
        try await settle(model) { model.current?.exercise.id == squat.id }
        #expect(model.finished.map(\.exercise.id) == [bench.id] && model.upNext.map(\.exercise.id) == [plank.id])

        // Tapping one opens it, finished or not.
        model.openID = try entry(model, bench).id
        #expect(model.current?.exercise.id == bench.id && model.finished.isEmpty)
        #expect(!model.isPast && model.untickedCount == 2)
    }

    @Test func aTickedSetStartsTheRestUntilWhatComesNext() async throws {
        let (app, model) = try await running()
        let sets = try entry(model, bench).sets

        model.tick(sets[0], in: try entry(model, bench))
        #expect(app.rest.endsAt != nil && app.rest.next == "set 2" && app.rest.total == 60)

        try await settle(model) { (try? entry(model, bench).doneSets.count) == 1 }
        try app.db.setRest(90, for: bench.id)
        try await settle(model) { (try? entry(model, bench).restS) == 90 }
        model.tick(sets[1], in: try entry(model, bench))
        #expect(app.rest.next == "Squat" && app.rest.total == 90)

        // Unticking starts no rest.
        app.rest.stop()
        try await settle(model) { (try? entry(model, bench).isFinished) == true }
        model.tick(sets[1], in: try entry(model, bench))
        #expect(app.rest.endsAt == nil)
    }

    @Test func theLastSetOfTheWorkoutRestsWithNothingNext() async throws {
        let db = try freshDB(bench)
        let w = try db.startWorkout(name: "Short")
        try db.addExercise(bench.id, to: w.id)
        let app = offlineApp(db)
        let model = WorkoutModel(app: app)
        model.watch()
        try await settle(model) { model.entries.count == 1 }

        model.tick(try entry(model, bench).sets[0], in: try entry(model, bench))

        #expect(app.rest.endsAt != nil && app.rest.next == nil)
        #expect(model.openID == nil)
    }

    @Test func thePadTypesAWeightAndSavesAsItGoes() async throws {
        let (app, model) = try await running()
        let set = try entry(model, bench).sets[0]
        model.edit(set, .weight, in: try entry(model, bench))
        #expect(model.editing == .init(setID: set.id, field: .weight) && model.padText == "")

        for k in ["6", "0", ".", "5"] { model.key(k) }
        #expect(model.padText == "60.5")
        // A second point, or a fourth decimal: ignored.
        model.key("."); model.key("2"); model.key("5")
        #expect(model.padText == "60.525")
        model.key("1")
        #expect(model.padText == "60.525")
        model.key("⌫"); model.key("⌫"); model.key("⌫")
        #expect(model.padText == "60.")
        #expect(try stored(app, set.id).weight == "60")

        model.key("⌫"); model.key("⌫"); model.key("⌫"); model.key("⌫")
        model.key(".")
        #expect(model.padText == "0.")
        try await settle(model) { model.editingTarget?.0.weight == "0" }
        // Opening the field again, the first key replaces what was there.
        model.edit(try entry(model, bench).sets[0], .weight, in: try entry(model, bench))
        model.key("7")
        #expect(try model.padText == "7" && stored(app, set.id).weight == "7")
    }

    @Test func repsAndSecondsTakeNoPoint() async throws {
        let (app, model) = try await running()
        let set = try entry(model, bench).sets[0]
        model.edit(set, .reps, in: try entry(model, bench))
        model.key("1"); model.key("."); model.key("2")
        #expect(try model.padText == "12" && stored(app, set.id).reps == 12)

        let hold = try entry(model, plank).sets[0]
        model.edit(hold, .seconds, in: try entry(model, plank))
        model.key("4"); model.key("5")
        #expect(try stored(app, hold.id).durationS == 45)

        // Seven characters at most.
        model.edit(set, .reps, in: try entry(model, bench))
        for k in ["1", "2", "3", "4", "5", "6", "7", "8"] { model.key(k) }
        #expect(model.padText == "1234567")
    }

    @Test func plusAndMinusStepByThePlates() async throws {
        let (app, model) = try await running()
        let set = try entry(model, bench).sets[0]
        model.edit(set, .weight, in: try entry(model, bench))
        model.step(1); model.step(1)
        #expect(model.padText == "5")
        model.step(-1); model.step(-1); model.step(-1)
        #expect(model.padText == "0" && model.padFresh)

        model.setUnit(.lb, for: try entry(model, bench))
        try await settle(model) { (try? entry(model, bench).unit) == .lb }
        model.step(1)
        #expect(try model.padText == "5" && stored(app, set.id).unit == .lb)

        // Steps are for weight only.
        model.edit(try entry(model, bench).sets[0], .reps, in: try entry(model, bench))
        model.step(1)
        #expect(model.padText == "")
    }

    @Test func switchingUnitConvertsWhatIsTyped() async throws {
        let (_, model) = try await running()
        model.edit(try entry(model, bench).sets[0], .weight, in: try entry(model, bench))
        for k in ["1", "0", "0"] { model.key(k) }

        model.toggleUnit()

        #expect(model.padText == "220.5" && model.padFresh)
        try await settle(model) { (try? entry(model, bench).unit) == .lb }
        model.edit(try entry(model, bench).sets[0], .reps, in: try entry(model, bench))
        model.toggleUnit()
        try await settle(model) { (try? entry(model, bench).unit) == .kg }
        #expect(model.padText == "")
    }

    @Test func nextGoesWeightRepsThenTheNextSet() async throws {
        let (_, model) = try await running(dips)
        let sets = try entry(model, bench).sets
        model.edit(sets[0], .weight, in: try entry(model, bench))
        model.next()
        #expect(model.editing == .init(setID: sets[0].id, field: .reps))
        model.next()
        #expect(model.editing == .init(setID: sets[1].id, field: .weight))
        model.next(); model.next()
        #expect(model.editing == nil)

        // A timed exercise's weight goes to its seconds.
        let hold = try entry(model, plank).sets[0]
        model.edit(hold, .weight, in: try entry(model, plank))
        model.next()
        #expect(model.editing?.field == .seconds)

        // A bodyweight exercise's next set opens on reps.
        try model.app.db.addSet(to: try entry(model, dips).id)
        try await settle(model) { (try? entry(model, dips).sets.count) == 2 }
        let dipSets = try entry(model, dips).sets
        model.edit(dipSets[0], .reps, in: try entry(model, dips))
        model.next()
        #expect(model.editing == .init(setID: dipSets[1].id, field: .reps))

        model.closePad()
        model.next()
        model.key("1")
        #expect(model.editing == nil && model.editingTarget == nil)
    }

    @Test func setsAddKindAndGo() async throws {
        let (app, model) = try await running()
        let e = try entry(model, squat)
        model.addSet(to: e)
        try await settle(model) { (try? entry(model, squat).sets.count) == 2 }
        let added = try entry(model, squat).sets[1]
        #expect(!added.done)

        model.setKind(.drop, for: added)
        #expect(try stored(app, added.id).kind == .drop)

        model.edit(added, .reps, in: try entry(model, squat))
        model.delete(added)
        #expect(try model.editing == nil && stored(app, added.id).deleted)
        // Deleting another set leaves the pad open.
        model.edit(try entry(model, bench).sets[0], .reps, in: try entry(model, bench))
        model.delete(try entry(model, squat).sets[0])
        #expect(model.editing != nil)
    }

    @Test func removingAnExerciseOffersUndoOnlyWhenSetsWereLogged() async throws {
        let (app, model) = try await running()
        model.openID = try entry(model, squat).id
        model.remove(try entry(model, squat))
        #expect(app.undo == nil && model.openID == nil)
        try await settle(model) { model.entries.count == 2 }

        model.tick(try entry(model, bench).sets[0], in: try entry(model, bench))
        try await settle(model) { (try? entry(model, bench).doneSets.count) == 1 }
        model.edit(try entry(model, bench).sets[1], .weight, in: try entry(model, bench))
        model.remove(try entry(model, bench))
        #expect(app.undo?.message == "Removed Bench Press" && model.editing == nil)

        app.undo?.restore()
        try await settle(model) { model.entries.count == 2 }
    }

    @Test func addingAnExerciseOpensIt() async throws {
        let (_, model) = try await running()
        model.query = "dip"
        model.add(dips)
        #expect(model.query.isEmpty)
        try await settle(model) { model.entries.count == 4 }
        #expect(model.current?.exercise.id == dips.id)
        model.setRest(45, for: try entry(model, dips))
        try await settle(model) { (try? entry(model, dips).restS) == 45 }
    }

    @Test func discardingThrowsTheWorkoutAwayWithUndo() async throws {
        let (app, model) = try await running()
        app.rest.start(seconds: 60, next: nil, alert: "")
        model.discard()
        #expect(app.rest.endsAt == nil && app.undo?.message == "Workout discarded")
        try await settle(model) { model.detail.value == nil }
        model.discard()
        model.add(dips)
        model.saveEdits()
    }

    @Test func editingAPastWorkoutTicksNewSetsAndDropsUntickedOnSave() async throws {
        let db = try freshDB(bench)
        let w = try logWorkout(db, bench, [("60", 8)], daysAgo: 1)
        let ended = try #require(try await db.writer.read { try Workout.fetchOne($0, key: w.id) }?.endedAt)
        let app = offlineApp(db)
        let model = WorkoutModel(app: app, workoutID: w.id)
        model.watch()
        try await settle(model) { model.entries.count == 1 }
        #expect(model.isPast)

        model.addSet(to: try entry(model, bench))
        try await settle(model) { (try? entry(model, bench).sets.count) == 2 }
        #expect(try entry(model, bench).sets[1].done)
        let added = try entry(model, bench).sets[1]
        model.tick(added, in: try entry(model, bench))
        try await settle(model) { model.untickedCount == 1 }
        // Ticked again: a past workout has no rest after it.
        model.tick(added, in: try entry(model, bench))
        #expect(app.rest.endsAt == nil)
        try await settle(model) { model.untickedCount == 0 }
        model.tick(added, in: try entry(model, bench))
        try await settle(model) { model.untickedCount == 1 }

        model.saveEdits()
        try await settle(model) { model.untickedCount == 0 }
        #expect(try entry(model, bench).sets.count == 1)
        #expect(try await db.writer.read { try Workout.fetchOne($0, key: w.id) }?.endedAt == ended)
    }

    private func stored(_ app: AppModel, _ id: UUID) throws -> LoggedSet {
        try #require(try app.db.writer.read { try LoggedSet.fetchOne($0, key: id) })
    }
}

/// The rest between sets.
@MainActor
struct RestTimerTests {
    @Test func moreTimeMovesTheEndAndStopClearsIt() {
        let rest = RestTimer(alerts: false)
        rest.add(30)
        #expect(rest.endsAt == nil)
        rest.start(seconds: 60, next: "set 2", alert: "Bench, set 2")
        let end = rest.endsAt
        rest.add(30)
        #expect(end != nil && rest.total == 90 && rest.endsAt == end?.addingTimeInterval(30) && rest.next == "set 2")
        rest.stop()
        #expect(rest.endsAt == nil)
    }
}
