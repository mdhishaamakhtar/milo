import Foundation
import GRDB

// Insights on past workouts, and the user's own exercises and templates.

/// What a past workout's exercise card says beside its sets.
nonisolated struct ExerciseInsight: Hashable, Sendable {
    /// The set that beat every earlier workout's heaviest.
    var heaviestSet: UUID?
    /// When an earlier workout went heavier: its date and top set.
    var earlierTop: LoggedSet?
    var earlierTopDate: Date?
}

nonisolated extension AppDatabase {
    /// "Heaviest yet" and "Top set on 10 Oct was…" for each loaded exercise in a workout.
    func insights(_ db: Database, for detail: WorkoutDetail) throws -> [UUID: ExerciseInsight] {
        let start = detail.workout.startedAt
        var out: [UUID: ExerciseInsight] = [:]
        for e in detail.entries where e.exercise.measurement == .repsLoad {
            func load(_ s: LoggedSet) -> Double? { s.isWorking && (s.reps ?? 0) > 0 ? Maths.toKg(s.weight, s.unit) : nil }
            var earlier: (kg: Double, date: Date, set: LoggedSet)?
            for past in try history(db, exerciseId: e.exercise.id) where past.workout.startedAt < start {
                for s in past.sets {
                    if let kg = load(s), kg > (earlier?.kg ?? 0) { earlier = (kg, past.workout.startedAt, s) }
                }
            }
            let mine = e.sets.compactMap { s in load(s).map { (set: s, kg: $0) } }
            guard let top = mine.map(\.kg).max(), let earlier else { continue }
            if top > earlier.kg + 1e-9 {
                out[e.id] = ExerciseInsight(heaviestSet: mine.first { $0.kg == top }?.set.id)
            } else if earlier.kg > top + 1e-9 {
                out[e.id] = ExerciseInsight(earlierTop: earlier.set, earlierTopDate: earlier.date)
            }
        }
        return out
    }
}

// MARK: Templates and exercises

nonisolated extension AppDatabase {
    /// Exercises often done in the same workouts as these, most often first.
    func companions(_ db: Database, of ids: [UUID], limit: Int = 2) throws -> [Exercise] {
        guard !ids.isEmpty else { return [] }
        let marks = Array(repeating: "?", count: ids.count).joined(separator: ",")
        let found = try UUID.fetchAll(db, sql: """
            SELECT b.exerciseId FROM workoutExercise a JOIN workoutExercise b ON b.workoutId = a.workoutId
            JOIN workout w ON w.id = a.workoutId
            WHERE a.exerciseId IN (\(marks)) AND b.exerciseId NOT IN (\(marks))
              AND a.deleted = 0 AND b.deleted = 0 AND w.deleted = 0
            GROUP BY b.exerciseId ORDER BY count(DISTINCT a.workoutId) DESC LIMIT ?
            """, arguments: StatementArguments(ids + ids + [limit] as [any DatabaseValueConvertible]))
        return try found.compactMap { id in try Exercise.fetchOne(db, key: id).flatMap { $0.deleted ? nil : $0 } }
    }

    /// How many finished workouts and templates use an exercise.
    func uses(_ db: Database, exerciseId: UUID) throws -> (workouts: Int, templates: Int) {
        let workouts = try Int.fetchOne(db, sql: """
            SELECT count(DISTINCT we.workoutId) FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
            WHERE we.exerciseId = ? AND we.deleted = 0 AND w.deleted = 0
            """, arguments: [exerciseId]) ?? 0
        let templates = try Template.filter(Column("deleted") == false).fetchAll(db)
            .filter { $0.items.contains { $0.exerciseId == exerciseId } }.count
        return (workouts, templates)
    }

    /// A new exercise of your own.
    @discardableResult
    func createExercise(name: String, equipment: String, measurement: MeasurementKind, unilateral: Bool, primaryMuscles: [String] = [], now: Date = Date()) throws -> Exercise {
        let e = Exercise(
            id: UUID(), builtin: false, name: name, aliases: [], primaryMuscles: primaryMuscles, secondaryMuscles: [],
            movementPattern: "", equipment: equipment, measurement: measurement, unilateral: unilateral,
            met: nil, about: "", approximate: false, updatedAt: now, deleted: false, dirty: true)
        try writer.write { db in try e.save(db) }
        return e
    }

    func save(_ exercise: Exercise, now: Date = Date()) throws {
        guard !exercise.builtin else { return }
        var e = exercise
        e.updatedAt = now
        e.dirty = true
        try writer.write { db in try e.save(db) }
    }

    /// Takes one of your exercises off your list. Past workouts keep showing it.
    func deleteExercise(_ id: UUID, now: Date = Date()) throws {
        try writer.write { db in
            guard let e = try Exercise.fetchOne(db, key: id), !e.builtin else { return }
            try e.tombstone(at: now).save(db)
        }
    }
}
