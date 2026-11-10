import Foundation
import GRDB

// What the Progress tab and an exercise's screen read: sessions over time, new bests,
// workouts a week, body weight. All computed here from the local rows.

/// One workout's done sets for an exercise, summed up.
nonisolated struct ExerciseSession: Identifiable, Sendable {
    var workout: Workout
    var sets: [LoggedSet]
    var summary: Maths.Summary
    var id: UUID { workout.id }

    /// The heaviest working set, for "30 kg × 15".
    var topSet: LoggedSet? {
        sets.filter(\.isWorking).max { (Maths.toKg($0.weight, $0.unit) ?? 0, $0.reps ?? 0) < (Maths.toKg($1.weight, $1.unit) ?? 0, $1.reps ?? 0) }
    }
}

/// An exercise with every session of it, newest first.
nonisolated struct ExerciseProgress: Sendable {
    var exercise: Exercise
    var unit: WeightUnit
    var restS: Int?
    var sessions: [ExerciseSession]
    var templateCount: Int
}

nonisolated struct WeekCount: Identifiable, Sendable {
    var start: Date
    var count: Int
    var id: Date { start }
}

nonisolated struct RecentBest: Identifiable, Sendable {
    var exercise: Exercise
    var date: Date
    /// "45 lb × 10".
    var text: String
    var id: String { "\(exercise.id)\(date.timeIntervalSince1970)" }
}

nonisolated struct ExerciseTrend: Identifiable, Sendable {
    var exercise: Exercise
    var unit: WeightUnit
    var topKg: Double?
    var sessions: Int
    /// Top set per session, oldest first, for the sparkline.
    var points: [Double]
    var id: UUID { exercise.id }
}

nonisolated struct BodyTrend: Sendable {
    var latestKg: Double
    var changeKg: Double?
    var since: Date?
}

nonisolated struct ProgressData: Sendable {
    var weeks: [WeekCount] = []
    var bests: [RecentBest] = []
    var top: [ExerciseTrend] = []
    var exerciseCount = 0
    var body: BodyTrend?
    var display: WeightUnit = .kg
}

nonisolated extension AppDatabase {
    /// Monday-first weeks, as the History calendar shows them.
    private static var weeks: Calendar {
        var c = Calendar.current
        c.firstWeekday = 2
        return c
    }

    private static let day: DateFormatter = {
        let f = DateFormatter()
        f.calendar = Calendar(identifier: .gregorian)
        f.locale = Locale(identifier: "en_US_POSIX")
        f.dateFormat = "yyyy-MM-dd"
        return f
    }()

    /// Every finished session of an exercise, newest first.
    func sessions(_ db: Database, exerciseId: UUID, measurement: MeasurementKind) throws -> [ExerciseSession] {
        try history(db, exerciseId: exerciseId).compactMap { h in
            let done = h.sets.filter(\.done)
            guard !done.isEmpty else { return nil }
            let bodyKg = try latestBodyKg(db, before: h.workout.startedAt) ?? 0
            return ExerciseSession(workout: h.workout, sets: done, summary: Maths.summarize(done.map { $0.input(measurement) }, bodyKg: bodyKg))
        }
    }

    func exerciseProgress(_ db: Database, id: UUID) throws -> ExerciseProgress? {
        guard let e = try Exercise.fetchOne(db, key: id) else { return nil }
        let (unit, rest) = try unit(db, for: id)
        return ExerciseProgress(
            exercise: e, unit: unit, restS: rest,
            sessions: try sessions(db, exerciseId: id, measurement: e.measurement),
            templateCount: try uses(db, exerciseId: id).templates)
    }

    /// The Progress tab: twelve weeks of workouts, recent bests, most-done exercises, body weight.
    func progress(_ db: Database, now: Date = .now) throws -> ProgressData {
        let cal = Self.weeks
        let thisWeek = cal.dateInterval(of: .weekOfYear, for: now)!.start
        let from = cal.date(byAdding: .weekOfYear, value: -11, to: thisWeek)!
        let finished = try finishedWorkouts(db)
        var counts: [Date: Int] = [:]
        for w in finished where w.workout.startedAt >= from {
            counts[cal.dateInterval(of: .weekOfYear, for: w.workout.startedAt)!.start, default: 0] += 1
        }
        var data = ProgressData(display: try settings(db).displayUnit)
        data.weeks = (0..<12).map { i in
            let start = cal.date(byAdding: .weekOfYear, value: i, to: from)!
            return WeekCount(start: start, count: counts[start] ?? 0)
        }

        let done = try UUID.fetchAll(db, sql: """
            SELECT DISTINCT we.exerciseId FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
            WHERE we.deleted = 0 AND w.deleted = 0 AND w.endedAt IS NOT NULL
            """)
        data.exerciseCount = try myExerciseCount(db)

        let bodyKg = try latestBodyKg(db) ?? 0
        var trends: [(trend: ExerciseTrend, recent: Int)] = []
        for id in done {
            guard let e = try Exercise.fetchOne(db, key: id) else { continue }
            let (unit, _) = try unit(db, for: id)
            let sessions = try sessions(db, exerciseId: id, measurement: e.measurement)
            guard !sessions.isEmpty else { continue }

            if e.measurement == .repsLoad || e.measurement == .assisted {
                let oldestFirst = sessions.reversed().map { Maths.Session(date: Self.day.string(from: $0.workout.startedAt), sets: $0.sets.map { $0.input(e.measurement) }) }
                for best in Maths.newBests(Array(oldestFirst), bodyKg: bodyKg) where best.kind == .heaviest {
                    guard let date = Self.day.date(from: best.date), date >= from else { continue }
                    let weight = "\(Format.kgIn(best.value, unit)) \(unit.rawValue)"
                    data.bests.append(RecentBest(exercise: e, date: date, text: best.reps.map { "\(weight) × \($0)" } ?? weight))
                }
            }
            let tops = sessions.compactMap(\.summary.topLoadKg)
            trends.append((
                ExerciseTrend(exercise: e, unit: unit, topKg: tops.max(), sessions: sessions.count, points: Array(tops.prefix(12).reversed())),
                sessions.filter { $0.workout.startedAt >= from }.count))
        }
        // Each exercise's latest best, newest first.
        var seen = Set<UUID>()
        data.bests = Array(data.bests.sorted { $0.date > $1.date }.filter { seen.insert($0.exercise.id).inserted }.prefix(4))
        data.top = Array(trends.sorted { ($0.recent, $0.trend.sessions) > ($1.recent, $1.trend.sessions) }.prefix(3).map(\.trend))

        let weights = try BodyMeasurement.filter(Column("deleted") == false && Column("weightKg") != nil)
            .order(Column("measuredAt")).fetchAll(db)
        if let latest = weights.last, let kg = latest.weightKg {
            let first = weights.first { $0.measuredAt >= from } ?? weights.first
            let change = first.flatMap { f in f.id == latest.id ? nil : f.weightKg.map { kg - $0 } }
            data.body = BodyTrend(latestKg: kg, changeKg: change, since: change == nil ? nil : first?.measuredAt)
        }
        return data
    }
}

/// An exercise in the list, with how many sets you've done of it.
nonisolated struct ExerciseRow: Identifiable, Sendable {
    var exercise: Exercise
    var sets: Int
    var id: UUID { exercise.id }
}

nonisolated extension AppDatabase {
    /// Your exercises: the ones you've done, and the ones you made.
    func myExerciseCount(_ db: Database) throws -> Int {
        try Int.fetchOne(db, sql: """
            SELECT count(*) FROM exercise e WHERE e.deleted = 0 AND (e.builtin = 0 OR EXISTS (
                SELECT 1 FROM workoutExercise we JOIN workout w ON w.id = we.workoutId
                WHERE we.exerciseId = e.id AND we.deleted = 0 AND w.deleted = 0 AND w.endedAt IS NOT NULL))
            """) ?? 0
    }

    func exerciseRows(_ db: Database) throws -> [ExerciseRow] {
        let counts = try setCounts(db)
        return try allExercises(db).map { ExerciseRow(exercise: $0, sets: counts[$0.id] ?? 0) }
    }
}
