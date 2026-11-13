import Foundation
import GRDB
import Testing
@testable import Milo

// Rows the tests build on.

func exercise(_ name: String, _ m: MeasurementKind = .repsLoad, unilateral: Bool = false, builtin: Bool = true) -> Exercise {
    Exercise(id: UUID(), builtin: builtin, name: name, aliases: [], primaryMuscles: ["chest"], secondaryMuscles: [], movementPattern: "", equipment: "", measurement: m, unilateral: unilateral, met: 4.5, about: "", approximate: false, updatedAt: .now, deleted: false, dirty: false)
}

func freshDB(_ exercises: Exercise...) throws -> AppDatabase {
    let db = try AppDatabase.inMemory()
    try db.writer.write { d in for e in exercises { try e.save(d) } }
    return db
}

/// Logs one finished workout of `sets` (weight, reps) for an exercise, `daysAgo` before `now`.
@discardableResult
func logWorkout(_ db: AppDatabase, _ e: Exercise, _ sets: [(String, Int)], daysAgo: Double, now: Date = .now, name: String = "Day", templateId: UUID? = nil) throws -> Workout {
    let start = now.addingTimeInterval(-daysAgo * 86400)
    let w = try db.startWorkout(name: name, items: [TemplateItem(exerciseId: e.id, groupLabel: "")], templateId: templateId, now: start)
    let we = try #require(try db.writer.read { d in try WorkoutExercise.filter(Column("workoutId") == w.id).fetchOne(d) })
    _ = try db.writer.write { d in try LoggedSet.filter(Column("workoutExerciseId") == we.id).deleteAll(d) }
    for (i, (weight, reps)) in sets.enumerated() {
        var s = try db.addSet(to: we.id, done: true, now: start)
        s.weight = weight; s.reps = reps; s.unit = .kg; s.position = i + 1
        try db.update(s, now: start)
    }
    try db.finishWorkout(w.id, name: name, notes: "", at: start.addingTimeInterval(3600), now: start.addingTimeInterval(3600))
    return w
}

/// The sets of a workout exercise, in order.
func sets(_ db: AppDatabase, of weId: UUID) throws -> [LoggedSet] {
    try db.writer.read { d in try LoggedSet.filter(Column("workoutExerciseId") == weId && Column("deleted") == false).order(Column("position")).fetchAll(d) }
}

// Apps the tests drive. Each has its own Keychain entry and no network monitor or
// notifications, so tests run side by side without touching the simulator's state.

/// An app on its own store, signed out, with no server behind it: what happens on the phone alone.
@MainActor
func offlineApp(_ db: AppDatabase) -> AppModel {
    AppModel(db: db, api: APIClient(baseURL: "https://milo.test", http: FakeServer()),
             keychain: Keychain(service: "dev.hishaam.milo.tests.\(UUID().uuidString)"), onDevice: false)
}

/// An app on an in-memory store, signed in, talking to `server`.
@MainActor
func signedInApp(_ server: FakeServer = FakeServer(), db: AppDatabase? = nil) throws -> AppModel {
    let app = AppModel(
        db: try db ?? .inMemory(),
        api: APIClient(baseURL: "https://milo.test", http: server),
        keychain: Keychain(service: "dev.hishaam.milo.tests.\(UUID().uuidString)"),
        onDevice: false)
    try app.session.signedIn(token: "token-1", user: .init(id: "u1", username: "hishaam"))
    return app
}
